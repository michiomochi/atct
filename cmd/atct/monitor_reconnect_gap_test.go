package main

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

// beforeEventsTransport runs hook once, just before the first SSE request
// reaches the daemon: after the watch's initial reconcile, before it subscribes.
type beforeEventsTransport struct {
	base http.RoundTripper
	once sync.Once
	hook func()
}

func (t *beforeEventsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/api/events" {
		t.once.Do(t.hook)
	}
	return t.base.RoundTrip(req)
}

// TestWatchDeliversRejectionEmittedBeforeSSEConnect: a rejection that lands
// between the initial reconcile and the SSE subscription is not on the stream,
// so the watch must reconcile again once connected instead of waiting for the
// 30s periodic reconcile.
func TestWatchDeliversRejectionEmittedBeforeSSEConnect(t *testing.T) {
	ctx, cancel, db, server, client, root := startWorkflowMonitorDaemon(t)
	defer cancel()

	commanderID := registerWorkflowMonitorSession(t, ctx, client, root, "gap-commander", "gap-commander-token")
	subcommanderID := registerWorkflowMonitorSession(t, ctx, client, root, "gap-subcommander", "gap-sub-token")
	executorID := registerWorkflowMonitorSession(t, ctx, client, root, "gap-executor", "gap-executor-token")

	projects, err := db.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = (%#v, %v), want one project", projects, err)
	}
	callWorkflowMonitorRPC(t, ctx, client, "project.claim", map[string]any{
		"project_id": projects[0].ID, "agent_session_id": commanderID,
	}, &domain.Project{})
	var goal domain.Goal
	callWorkflowMonitorRPC(t, ctx, client, "goal.create", map[string]any{
		"cwd": root, "content": "rejection before SSE connect", "creator": "human",
	}, &goal)
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.request", map[string]any{
		"handoff_id": "gh-gap", "goal_id": goal.ID, "requested_by": commanderID, "request_report": "go",
	}, &store.GoalHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.receive", map[string]any{
		"handoff_id": "gh-gap", "goal_id": goal.ID, "received_by": subcommanderID,
	}, &map[string]any{})
	tasks, err := db.CreateTasks(ctx, goal.ID, "claude", "gap", []string{"task"}, []string{"criteria"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("CreateTasks = (%#v, %v)", tasks, err)
	}
	task := tasks[0]
	const handoffID = "th-gap"
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.request", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "requested_by": subcommanderID, "request_report": "do it",
	}, &store.TaskHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.receive", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "received_by": executorID,
	}, &map[string]any{})
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "requested_by": executorID, "review_request_report": "done",
	}, &store.TaskHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.receive", map[string]any{
		"handoff_id": handoffID, "task_id": task.ID, "received_by": subcommanderID,
	}, &map[string]any{})

	reject := func() { rejectGapHandoff(t, ctx, client, handoffID, task.ID, subcommanderID) }
	watchClient := &http.Client{Transport: &beforeEventsTransport{base: server.Client().Transport, hook: reject}}
	snapshot, projectIDGetter := watchSnapshotWithProject(watchClient, []string{server.URL}, root)
	stdout := &lockedBuffer{}
	writer := monitorActionWriter{writer: stdout}
	scope := watchScope{
		Role: "executor", ProjectID: strconv.FormatInt(projects[0].ID, 10),
		GoalID: strconv.FormatInt(goal.ID, 10), TaskID: strconv.FormatInt(task.ID, 10),
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go func() {
		_ = watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(
			watchCtx, io.Discard, watchClient, 5*time.Millisecond, snapshot, nil, projectIDGetter,
			scope, nil, writer.Sink,
		)
	}()

	count := func() int {
		n := 0
		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.Contains(line, "review rejected") && strings.Contains(line, handoffID) {
				n++
			}
		}
		return n
	}
	deadline := time.Now().Add(5 * time.Second)
	for count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count() == 0 {
		t.Fatalf("rejection emitted before the SSE connect was not delivered within 5s; stdout:\n%s", stdout.String())
	}
	time.Sleep(300 * time.Millisecond)
	if got := count(); got != 1 {
		t.Fatalf("rejection wakeup delivered %d times, want exactly once; stdout:\n%s", got, stdout.String())
	}
}

func rejectGapHandoff(t *testing.T, ctx context.Context, client *mcpshim.Client, handoffID string, taskID, reviewerID int64) {
	t.Helper()
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.reject", map[string]any{
		"handoff_id": handoffID, "task_id": taskID, "reviewer_id": reviewerID, "reject_report": "fix it",
	}, &store.TaskHandoff{})
}
