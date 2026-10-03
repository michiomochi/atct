package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestExecutorMonitorSurvivesTaskHandoffRejection drives an executor monitor the
// way `atct watch --monitor --token` runs it for Claude Code (runMonitorBindingLoop
// writing wakeups to stdout). A rejected task handoff must keep the executor's
// scope and watch alive until the executor has received the rejection, and
// again through the resubmission.
func TestExecutorMonitorSurvivesTaskHandoffRejection(t *testing.T) {
	ctx, cancel, db, server, client, root := startWorkflowMonitorDaemon(t)
	defer cancel()

	const (
		commanderToken = "reject-e2e-commander"
		executorToken  = "reject-e2e-executor"
	)
	commanderID := registerWorkflowMonitorSession(t, ctx, client, root, "reject-e2e-commander", commanderToken)
	subcommanderID := registerWorkflowMonitorSession(t, ctx, client, root, "reject-e2e-subcommander", "reject-e2e-sub-token")
	executorID := registerWorkflowMonitorSession(t, ctx, client, root, "reject-e2e-executor", executorToken)

	projects, err := db.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = (%#v, %v), want one project", projects, err)
	}
	callWorkflowMonitorRPC(t, ctx, client, "project.claim", map[string]any{
		"project_id": projects[0].ID, "agent_session_id": commanderID,
	}, &domain.Project{})
	var goal domain.Goal
	callWorkflowMonitorRPC(t, ctx, client, "goal.create", map[string]any{
		"cwd": root, "content": "executor monitor survives rejection", "creator": "human",
	}, &goal)
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.request", map[string]any{
		"handoff_id": "gh-reject-e2e", "goal_id": goal.ID, "requested_by": commanderID, "request_report": "go",
	}, &store.GoalHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.receive", map[string]any{
		"handoff_id": "gh-reject-e2e", "goal_id": goal.ID, "received_by": subcommanderID,
	}, &map[string]any{})
	tasks, err := db.CreateTasks(ctx, goal.ID, "claude", "reject-e2e", []string{"task"}, []string{"criteria"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("CreateTasks = (%#v, %v)", tasks, err)
	}
	task := tasks[0]
	const handoffID = "th-reject-e2e"
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.request", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "requested_by": subcommanderID, "request_report": "do it",
	}, &store.TaskHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.receive", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "received_by": executorID,
	}, &map[string]any{})

	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	watchTransport := &workflowMonitorTransport{base: server.Client().Transport}
	watchClient := &http.Client{Transport: watchTransport}
	snapshot, projectIDGetter := watchSnapshotWithProject(watchClient, []string{server.URL}, root)
	stdout := &lockedBuffer{}
	writer := monitorActionWriter{writer: stdout}
	done := make(chan error, 1)
	started := make(chan watchScope, 16)
	stopped := make(chan watchScope, 16)
	go func() {
		done <- runMonitorBindingLoop(watchCtx, watchClient, []string{server.URL}, executorToken, func(scopeCtx context.Context, scope watchScope) error {
			started <- scope
			defer func() { stopped <- scope }()
			var reporters []watchHealthSink
			if reporter := newWatchHealthReporter(watchClient, []string{server.URL}, root, scope); reporter != nil {
				reporters = append(reporters, reporter)
			}
			return watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(
				scopeCtx, io.Discard, watchClient, 5*time.Millisecond, snapshot, nil, projectIDGetter,
				scope, nil, writer.Sink, reporters...,
			)
		})
	}()
	taskID := strconv.FormatInt(task.ID, 10)
	waitWorkflowMonitorScope(t, started, func(scope watchScope) bool { return scope.TaskID == taskID })
	// An event emitted between reconciliation and the SSE connect is not
	// redelivered until the next periodic reconcile, so wait for the stream.
	watchTransport.waitForEvents(t, 1)

	assertAlive := func(stage string) {
		t.Helper()
		select {
		case err := <-done:
			t.Fatalf("%s: executor watch exited (err = %v); stdout:\n%s", stage, err, stdout.String())
		case scope := <-stopped:
			t.Fatalf("%s: executor scope %#v stopped; stdout:\n%s", stage, scope, stdout.String())
		case <-time.After(500 * time.Millisecond):
		}
		var check struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		callWorkflowMonitorRPC(t, ctx, client, "session.monitor_check", map[string]any{"session_key": "reject-e2e-executor"}, &check)
		if check.Decision != "" {
			t.Fatalf("%s: monitor-check = %q (%s); stdout:\n%s", stage, check.Decision, check.Reason, stdout.String())
		}
	}

	// Review request → subcommander receives → rejects.
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "requested_by": executorID, "review_request_report": "done",
	}, &store.TaskHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.receive", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "received_by": subcommanderID,
	}, &map[string]any{})
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.reject", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "reviewer_id": subcommanderID, "reject_report": "fix it",
	}, &store.TaskHandoff{})

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stdout.String(), "reject") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stdout.String(), "reject") {
		t.Fatalf("executor never heard the rejection; stdout:\n%s", stdout.String())
	}
	assertAlive("after the rejection wakeup")

	// The executor can still bind: the binding keeps the task as its scope.
	binding, found, err := fetchMonitorBinding(ctx, watchClient, []string{server.URL}, executorToken)
	if err != nil || !found || len(binding.Assignment.Tasks) != 1 {
		t.Fatalf("binding after rejection = (%#v, %v, %v), want the rejected task kept as a scope", binding, found, err)
	}

	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.reject.receive", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "received_by": executorID,
	}, &store.TaskHandoff{})
	assertAlive("after the rejection receive")

	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "requested_by": executorID, "review_request_report": "fixed",
	}, &store.TaskHandoff{})
	assertAlive("after the resubmission")
}
