package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemon"
	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

type workflowMonitorActionRecorder struct {
	bridge *codexMonitorBridge

	mu      sync.Mutex
	actions []watchAgentAction
}

type workflowMonitorTransport struct {
	base http.RoundTripper

	mu              sync.Mutex
	events          int
	monitorBindings int
}

func (t *workflowMonitorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	switch {
	case req.URL.Path == "/api/events":
		t.events++
	case strings.HasPrefix(req.URL.Path, "/api/monitor-bindings/"):
		t.monitorBindings++
	}
	t.mu.Unlock()
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func (t *workflowMonitorTransport) eventCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.events
}

func (t *workflowMonitorTransport) waitForEvents(tst *testing.T, count int) {
	tst.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		t.mu.Lock()
		got := t.events
		t.mu.Unlock()
		if got >= count {
			return
		}
		select {
		case <-deadline.C:
			tst.Fatalf("SSE connections = %d, want at least %d", got, count)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (t *workflowMonitorTransport) waitForMonitorBindings(tst *testing.T, count int) {
	tst.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		t.mu.Lock()
		got := t.monitorBindings
		t.mu.Unlock()
		if got >= count {
			return
		}
		select {
		case <-deadline.C:
			tst.Fatalf("monitor binding requests = %d, want at least %d", got, count)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (r *workflowMonitorActionRecorder) sink(ctx context.Context) watchAgentActionSink {
	delegate := r.bridge.ActionSinkWithContext(ctx)
	return func(action watchAgentAction) error {
		r.mu.Lock()
		r.actions = append(r.actions, action)
		r.mu.Unlock()
		return delegate(action)
	}
}

func (r *workflowMonitorActionRecorder) snapshot() []watchAgentAction {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]watchAgentAction(nil), r.actions...)
}

func (r *workflowMonitorActionRecorder) has(eventName, subject string) bool {
	for _, action := range r.snapshot() {
		if action.eventName == eventName && strings.Contains(action.line, subject) {
			return true
		}
	}
	return false
}

type workflowMonitorWatch struct {
	recorder *workflowMonitorActionRecorder
	done     <-chan error
	scopes   <-chan watchScope
	stopped  <-chan watchScope
}

func startWorkflowMonitorWatch(ctx context.Context, client *http.Client, baseURL, cwd string, scope watchScope) workflowMonitorWatch {
	bridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "thread-e2e")
	bridge.SetActive(true)
	recorder := &workflowMonitorActionRecorder{bridge: bridge}
	snapshot, projectID := watchSnapshotWithProject(client, []string{baseURL}, cwd)
	done := make(chan error, 1)
	go func() {
		done <- watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(
			ctx, io.Discard, client, 5*time.Millisecond, snapshot, nil, projectID,
			scope, nil, recorder.sink(ctx),
		)
	}()
	return workflowMonitorWatch{recorder: recorder, done: done}
}

// startWorkflowMonitorBoundWatch runs the monitor the way production does:
// runMonitorBindingLoop polls /api/monitor-bindings/<token> and starts or stops
// one scoped watch per server-derived scope. started/stopped report each
// scope's watch lifetime.
func startWorkflowMonitorBoundWatch(ctx context.Context, client *http.Client, baseURL, cwd, token string) workflowMonitorWatch {
	bridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "thread-e2e-"+token)
	bridge.SetActive(true)
	recorder := &workflowMonitorActionRecorder{bridge: bridge}
	snapshot, projectID := watchSnapshotWithProject(client, []string{baseURL}, cwd)
	started := make(chan watchScope, 16)
	stopped := make(chan watchScope, 16)
	done := make(chan error, 1)
	go func() {
		done <- runMonitorBindingLoop(ctx, client, []string{baseURL}, token, func(scopeCtx context.Context, scope watchScope) error {
			started <- scope
			defer func() { stopped <- scope }()
			return watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(
				scopeCtx, io.Discard, client, 5*time.Millisecond, snapshot, nil, projectID,
				scope, nil, recorder.sink(scopeCtx),
			)
		})
	}()
	return workflowMonitorWatch{recorder: recorder, done: done, scopes: started, stopped: stopped}
}

func waitWorkflowMonitorScope(t *testing.T, scopes <-chan watchScope, want func(watchScope) bool) watchScope {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case scope := <-scopes:
			if want(scope) {
				return scope
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for monitored scope replacement")
		}
	}
}

func waitWorkflowMonitorAction(t *testing.T, recorder *workflowMonitorActionRecorder, eventName, subject string) watchAgentAction {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		for _, action := range recorder.snapshot() {
			if action.eventName == eventName && strings.Contains(action.line, subject) {
				return action
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s containing %q; actions = %#v", eventName, subject, recorder.snapshot())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func assertNoWorkflowMonitorAction(t *testing.T, recorder *workflowMonitorActionRecorder, eventName, subject string) {
	t.Helper()
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	for {
		if recorder.has(eventName, subject) {
			t.Fatalf("unexpected %s containing %q; actions = %#v", eventName, subject, recorder.snapshot())
		}
		select {
		case <-deadline.C:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func callWorkflowMonitorRPC(t *testing.T, ctx context.Context, client *mcpshim.Client, method string, params any, out any) {
	t.Helper()
	if err := client.Call(ctx, method, params, out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func registerWorkflowMonitorSession(t *testing.T, ctx context.Context, client *mcpshim.Client, cwd, key string, monitorToken ...string) int64 {
	t.Helper()
	var registered struct {
		AgentSessionID int64 `json:"agent_session_id"`
	}
	callWorkflowMonitorRPC(t, ctx, client, "run.register", map[string]any{
		"pid": os.Getpid(),
		"cwd": cwd,
	}, &registered)
	var identified struct {
		AgentSessionID int64 `json:"agent_session_id"`
	}
	identify := map[string]any{
		"agent_session_id": registered.AgentSessionID,
		"session_key":      key,
		"cwd":              cwd,
	}
	if len(monitorToken) > 0 {
		identify["monitor_token"] = monitorToken[0]
	}
	callWorkflowMonitorRPC(t, ctx, client, "session.identify", identify, &identified)
	return identified.AgentSessionID
}

func startWorkflowMonitorDaemon(t *testing.T) (context.Context, context.CancelFunc, *store.Store, *httptest.Server, *mcpshim.Client, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dir, err := os.MkdirTemp("/private/tmp", "atct260-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	db, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	project, err := db.CreateProject(context.Background(), "atct", root)
	if err != nil {
		db.Close()
		t.Fatalf("CreateProject: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	socketPath := filepath.Join(dir, "atct.sock")
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- daemon.NewWithVersion(db, "test", socketPath).Serve(ctx, socketPath) }()
	ready := false
	for i := 0; i < 100; i++ {
		conn, dialErr := net.Dial("unix", socketPath)
		if dialErr == nil {
			_ = conn.Close()
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		cancel()
		_ = db.Close()
		t.Fatalf("daemon socket %q did not become ready", socketPath)
	}

	server := httptest.NewServer(daemon.NewWithVersion(db, "test", socketPath).HTTPHandler())
	client := mcpshim.NewClient(socketPath)
	t.Cleanup(func() {
		server.Close()
		cancel()
		select {
		case err := <-daemonDone:
			if err != nil {
				t.Errorf("daemon Serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Errorf("daemon Serve did not stop after context cancellation")
		}
		if err := db.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	_ = project
	return ctx, cancel, db, server, client, root
}

func approveWorkflowMonitorDecision(t *testing.T, client *http.Client, baseURL string, decisionID int64) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/decisions/"+strconv.FormatInt(decisionID, 10)+"/approve", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new approval request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("approve goal review: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("approve goal review status = %s, body = %s", resp.Status, body)
	}
}

func waitWorkflowMonitorScopeStopped(t *testing.T, stopped <-chan watchScope, want func(watchScope) bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case scope := <-stopped:
			if want(scope) {
				return
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for the binding to drop a scope")
		}
	}
}

func stopWorkflowMonitorWatch(t *testing.T, name string, watch workflowMonitorWatch, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	select {
	case err := <-watch.done:
		if err != nil && !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("%s watch: %v", name, err)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("%s watch did not stop", name)
	}
}

// TestWorkflowMonitorEndToEndContract drives commander, subcommander and two
// executor monitors through the real /api/monitor-bindings/<token> path and
// runMonitorBindingLoop. A binding only carries a scope once its session has
// received a handoff, so a worker has no binding scope before receipt; plain
// scoped watches stand in for the worker scopes there, and everything after the
// receive is observed through the binding.
func TestWorkflowMonitorEndToEndContract(t *testing.T) {
	ctx, cancel, db, server, client, root := startWorkflowMonitorDaemon(t)
	defer cancel()

	const (
		commanderToken = "workflow-e2e-monitor-commander"
		subToken       = "workflow-e2e-monitor-sub"
		executorAToken = "workflow-e2e-monitor-a"
		executorBToken = "workflow-e2e-monitor-b"
	)
	commanderID := registerWorkflowMonitorSession(t, ctx, client, root, "workflow-e2e-commander", commanderToken)
	subcommanderID := registerWorkflowMonitorSession(t, ctx, client, root, "workflow-e2e-subcommander", subToken)
	executorAID := registerWorkflowMonitorSession(t, ctx, client, root, "workflow-e2e-executor-a", executorAToken)
	executorBID := registerWorkflowMonitorSession(t, ctx, client, root, "workflow-e2e-executor-b", executorBToken)

	projects, err := db.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = (%#v, %v), want one project", projects, err)
	}
	project := projects[0]
	callWorkflowMonitorRPC(t, ctx, client, "project.claim", map[string]any{
		"project_id":       project.ID,
		"agent_session_id": commanderID,
	}, &domain.Project{})

	var goal domain.Goal
	callWorkflowMonitorRPC(t, ctx, client, "goal.create", map[string]any{
		"cwd":     root,
		"content": "prove workflow monitor delivery",
		"creator": "human",
	}, &goal)

	projectID := strconv.FormatInt(project.ID, 10)
	goalID := strconv.FormatInt(goal.ID, 10)
	watchTransport := &workflowMonitorTransport{base: server.Client().Transport}
	watchClient := &http.Client{Transport: watchTransport}
	watchCtx, stopWatches := context.WithCancel(ctx)
	defer stopWatches()

	commanderWatch := startWorkflowMonitorBoundWatch(watchCtx, watchClient, server.URL, root, commanderToken)
	waitWorkflowMonitorScope(t, commanderWatch.scopes, func(scope watchScope) bool {
		return scope == watchScope{Role: "commander", ProjectID: projectID, MonitorToken: commanderToken}
	})
	subStaticCtx, stopSubStatic := context.WithCancel(watchCtx)
	subStatic := startWorkflowMonitorWatch(subStaticCtx, watchClient, server.URL, root, watchScope{
		ProjectID: projectID, GoalID: goalID, Role: "subcommander",
	})
	watchTransport.waitForEvents(t, 2)

	// 1. goal startup: the commander made the request, so it is not echoed back; the subcommander has no scope yet.
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.request", map[string]any{
		"handoff_id":     "gh-workflow-e2e",
		"goal_id":        goal.ID,
		"requested_by":   commanderID,
		"request_report": "goal ready for implementation",
	}, &store.GoalHandoff{})
	assertNoWorkflowMonitorAction(t, commanderWatch.recorder, "goal.handoff.request", "gh-workflow-e2e")
	assertNoWorkflowMonitorAction(t, subStatic.recorder, "goal.handoff.request", "gh-workflow-e2e")

	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.receive", map[string]any{
		"handoff_id": "gh-workflow-e2e", "goal_id": goal.ID, "received_by": subcommanderID,
	}, &map[string]any{})
	stopWorkflowMonitorWatch(t, "subcommander static", subStatic, stopSubStatic)
	subcommanderWatch := startWorkflowMonitorBoundWatch(watchCtx, watchClient, server.URL, root, subToken)
	waitWorkflowMonitorScope(t, subcommanderWatch.scopes, func(scope watchScope) bool {
		return scope == watchScope{Role: "subcommander", ProjectID: projectID, GoalID: goalID, MonitorToken: subToken}
	})

	// 2. design review: only the commander.
	callWorkflowMonitorRPC(t, ctx, client, "goal.update_request_report", map[string]any{
		"goal_id": goal.ID, "spec": "# Spec", "plan": "# Plan", "agent_session_id": subcommanderID,
	}, &domain.Goal{})
	callWorkflowMonitorRPC(t, ctx, client, "plan.handoff.review.request", map[string]any{
		"handoff_id": "ph-workflow-e2e", "goal_id": goal.ID, "requested_by": subcommanderID,
		"review_request_report": "plan is ready for review",
	}, &store.PlanHandoff{})
	waitWorkflowMonitorAction(t, commanderWatch.recorder, "plan.handoff.review.request", "ph-workflow-e2e")
	assertNoWorkflowMonitorAction(t, subcommanderWatch.recorder, "plan.handoff.review.request", "ph-workflow-e2e")

	callWorkflowMonitorRPC(t, ctx, client, "plan.handoff.review.receive", map[string]any{
		"handoff_id": "ph-workflow-e2e", "goal_id": goal.ID, "received_by": commanderID,
	}, &map[string]any{})
	callWorkflowMonitorRPC(t, ctx, client, "plan.handoff.complete", map[string]any{
		"handoff_id": "ph-workflow-e2e", "goal_id": goal.ID, "agent_session_id": commanderID,
		"complete_report": "plan accepted",
	}, &store.PlanHandoff{})

	// 3. task creation: only the subcommander, and no executor task exists yet.
	taskCreateHandoffs, err := db.ListTaskCreateHandoffs(ctx, goal.ID)
	if err != nil || len(taskCreateHandoffs) != 1 {
		t.Fatalf("ListTaskCreateHandoffs = (%#v, %v), want one handoff", taskCreateHandoffs, err)
	}
	taskCreateHandoffID := taskCreateHandoffs[0].ID
	waitWorkflowMonitorAction(t, subcommanderWatch.recorder, "task.create_handoff.request", taskCreateHandoffID)
	assertNoWorkflowMonitorAction(t, commanderWatch.recorder, "task.create_handoff.request", taskCreateHandoffID)
	if existing, err := db.ListTasks(ctx, goal.ID); err != nil || len(existing) != 0 {
		t.Fatalf("ListTasks before task.create = (%#v, %v), want none", existing, err)
	}
	callWorkflowMonitorRPC(t, ctx, client, "task.create_handoff.receive", map[string]any{
		"handoff_id": taskCreateHandoffID, "received_by": subcommanderID,
	}, &map[string]any{})
	var tasks []domain.Task
	callWorkflowMonitorRPC(t, ctx, client, "task.create", map[string]any{
		"handoff_id": taskCreateHandoffID, "goal_id": goal.ID, "agent": "codex",
		"idempotency_key": "workflow-monitor-e2e", "titles": []string{"executor A", "executor B"},
		"descriptions": []string{"A completion criteria", "B completion criteria"}, "agent_session_id": subcommanderID,
	}, &tasks)
	if len(tasks) != 2 {
		t.Fatalf("created tasks = %d, want two", len(tasks))
	}

	// 4. executor work: an unreceived task handoff wakes the subcommander;
	// workers have no scope before receipt, so neither executor hears it.
	taskIDs := []string{strconv.FormatInt(tasks[0].ID, 10), strconv.FormatInt(tasks[1].ID, 10)}
	staticCtx, stopStatics := context.WithCancel(watchCtx)
	statics := []workflowMonitorWatch{
		startWorkflowMonitorWatch(staticCtx, watchClient, server.URL, root, watchScope{
			ProjectID: projectID, GoalID: goalID, TaskID: taskIDs[0], Role: "executor",
		}),
		startWorkflowMonitorWatch(staticCtx, watchClient, server.URL, root, watchScope{
			ProjectID: projectID, GoalID: goalID, TaskID: taskIDs[1], Role: "executor",
		}),
	}
	watchTransport.waitForEvents(t, 5)
	taskHandoffIDs := []string{"th-workflow-e2e-a", "th-workflow-e2e-b"}
	executorIDs := []int64{executorAID, executorBID}
	for i, task := range tasks {
		callWorkflowMonitorRPC(t, ctx, client, "task.handoff.request", map[string]any{
			"handoff_id": taskHandoffIDs[i], "task_id": task.ID, "requested_by": subcommanderID,
			"request_report": "task ready for implementation",
		}, &store.TaskHandoff{})
		waitWorkflowMonitorAction(t, subcommanderWatch.recorder, "task.handoff.request", "handoff_id: "+taskHandoffIDs[i])
		assertNoWorkflowMonitorAction(t, statics[0].recorder, "task.handoff.request", "handoff_id: "+taskHandoffIDs[i])
		assertNoWorkflowMonitorAction(t, statics[1].recorder, "task.handoff.request", "handoff_id: "+taskHandoffIDs[i])
		assertNoWorkflowMonitorAction(t, commanderWatch.recorder, "task.handoff.request", "handoff_id: "+taskHandoffIDs[i])
		callWorkflowMonitorRPC(t, ctx, client, "task.handoff.receive", map[string]any{
			"handoff_id": taskHandoffIDs[i], "task_id": task.ID, "received_by": executorIDs[i],
		}, &map[string]any{})
	}
	stopStatics()
	for i, watch := range statics {
		select {
		case err := <-watch.done:
			if err != nil && !strings.Contains(err.Error(), "context canceled") {
				t.Fatalf("executor %d static watch: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("executor %d static watch did not stop before binding", i)
		}
	}
	// Each executor monitor now follows its own received task through the binding.
	executorAWatch := startWorkflowMonitorBoundWatch(watchCtx, watchClient, server.URL, root, executorAToken)
	executorBWatch := startWorkflowMonitorBoundWatch(watchCtx, watchClient, server.URL, root, executorBToken)
	scopeA := waitWorkflowMonitorScope(t, executorAWatch.scopes, func(scope watchScope) bool {
		return scope == watchScope{Role: "executor", ProjectID: projectID, GoalID: goalID, TaskID: taskIDs[0], MonitorToken: executorAToken}
	})
	scopeB := waitWorkflowMonitorScope(t, executorBWatch.scopes, func(scope watchScope) bool {
		return scope == watchScope{Role: "executor", ProjectID: projectID, GoalID: goalID, TaskID: taskIDs[1], MonitorToken: executorBToken}
	})
	state := decodeReconciliation(t, server.Client(), server.URL, project.ID)
	if !watchLivenessActionable(scopeA, state) || !watchLivenessActionable(scopeB, state) {
		t.Fatalf("received executor tasks must be liveness-actionable; state = %#v", state)
	}

	// 5. task review: only the subcommander hears it; executor A's liveness is
	// suppressed while executor B's task stays actionable.
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.request", map[string]any{
		"handoff_id": taskHandoffIDs[0], "task_id": tasks[0].ID, "requested_by": executorAID,
		"review_request_report": "executor A implemented and tested",
	}, &store.TaskHandoff{})
	waitWorkflowMonitorAction(t, subcommanderWatch.recorder, "task.handoff.review.request", "handoff_id: "+taskHandoffIDs[0])
	assertNoWorkflowMonitorAction(t, commanderWatch.recorder, "task.handoff.review.request", "handoff_id: "+taskHandoffIDs[0])
	assertNoWorkflowMonitorAction(t, executorAWatch.recorder, "task.handoff.review.request", "handoff_id: "+taskHandoffIDs[0])
	assertNoWorkflowMonitorAction(t, executorBWatch.recorder, "task.handoff.review.request", "handoff_id: "+taskHandoffIDs[0])
	assertNoWorkflowMonitorAction(t, executorAWatch.recorder, "monitor.liveness", "")
	state = decodeReconciliation(t, server.Client(), server.URL, project.ID)
	if watchLivenessActionable(scopeA, state) {
		t.Fatal("executor A stayed liveness-actionable after its review request")
	}
	if !watchLivenessActionable(scopeB, state) {
		t.Fatal("executor B lost liveness while only executor A requested review")
	}

	// The subcommander completes A: the binding must drop A's scope, and only A's.
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.receive", map[string]any{
		"handoff_id": taskHandoffIDs[0], "task_id": tasks[0].ID, "received_by": subcommanderID,
	}, &map[string]any{})
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.complete", map[string]any{
		"handoff_id": taskHandoffIDs[0], "task_id": tasks[0].ID, "agent_session_id": subcommanderID,
		"complete_report": "executor A accepted",
	}, &store.TaskHandoff{})
	waitWorkflowMonitorScopeStopped(t, executorAWatch.stopped, func(scope watchScope) bool { return scope == scopeA })
	select {
	case scope := <-executorBWatch.stopped:
		t.Fatalf("executor B scope %#v stopped when only executor A completed", scope)
	default:
	}
	assertNoWorkflowMonitorAction(t, executorAWatch.recorder, "monitor.liveness", "")

	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.request", map[string]any{
		"handoff_id": taskHandoffIDs[1], "task_id": tasks[1].ID, "requested_by": executorBID,
		"review_request_report": "executor B implemented and tested",
	}, &store.TaskHandoff{})
	waitWorkflowMonitorAction(t, subcommanderWatch.recorder, "task.handoff.review.request", "handoff_id: "+taskHandoffIDs[1])
	assertNoWorkflowMonitorAction(t, executorBWatch.recorder, "task.handoff.review.request", "handoff_id: "+taskHandoffIDs[1])
	assertNoWorkflowMonitorAction(t, executorAWatch.recorder, "task.handoff.review.request", "handoff_id: "+taskHandoffIDs[1])
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.review.receive", map[string]any{
		"handoff_id": taskHandoffIDs[1], "task_id": tasks[1].ID, "received_by": subcommanderID,
	}, &map[string]any{})
	callWorkflowMonitorRPC(t, ctx, client, "task.handoff.complete", map[string]any{
		"handoff_id": taskHandoffIDs[1], "task_id": tasks[1].ID, "agent_session_id": subcommanderID,
		"complete_report": "executor B accepted",
	}, &store.TaskHandoff{})
	waitWorkflowMonitorScopeStopped(t, executorBWatch.stopped, func(scope watchScope) bool { return scope == scopeB })

	// 6. goal review: only the commander; merge continuation only after approval.
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.review.request", map[string]any{
		"handoff_id": "gh-workflow-e2e", "goal_id": goal.ID, "requested_by": subcommanderID,
		"review_request_report": "all executor tasks are complete",
	}, &store.GoalHandoff{})
	waitWorkflowMonitorAction(t, commanderWatch.recorder, "goal.handoff.review.request", "gh-workflow-e2e")
	assertNoWorkflowMonitorAction(t, subcommanderWatch.recorder, "goal.handoff.review.request", "gh-workflow-e2e")
	assertNoWorkflowMonitorAction(t, executorAWatch.recorder, "goal.handoff.review.request", "gh-workflow-e2e")
	assertNoWorkflowMonitorAction(t, executorBWatch.recorder, "goal.handoff.review.request", "gh-workflow-e2e")

	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.review.receive", map[string]any{
		"handoff_id": "gh-workflow-e2e", "goal_id": goal.ID, "received_by": commanderID,
	}, &map[string]any{})
	var review domain.Decision
	callWorkflowMonitorRPC(t, ctx, client, "goal.review.request", map[string]any{
		"goal_id": goal.ID, "agent_session_id": commanderID,
		"work_done": "workflow lifecycle verified", "now_possible": "scoped monitor delivery",
		"how_to_verify": "run the focused test", "surprises": "none", "needs_review": "none",
		"next_goal_ids": []int64{},
	}, &review)

	markers := []string{"goal-review-request"}
	approveWorkflowMonitorDecision(t, server.Client(), server.URL, review.ID)
	markers = append(markers, "human-approval")
	waitWorkflowMonitorAction(t, commanderWatch.recorder, "goal.review.complete", "goal_id: "+goalID)
	assertNoWorkflowMonitorAction(t, subcommanderWatch.recorder, "goal.review.complete", "goal_id: "+goalID)
	assertNoWorkflowMonitorAction(t, executorAWatch.recorder, "goal.review.complete", "goal_id: "+goalID)
	assertNoWorkflowMonitorAction(t, executorBWatch.recorder, "goal.review.complete", "goal_id: "+goalID)
	if before, err := db.GetGoal(ctx, goal.ID); err != nil || before.Status == domain.GoalDone {
		t.Fatalf("goal before merge continuation = (%#v, %v), want not done", before, err)
	}

	callWorkflowMonitorRPC(t, ctx, client, "goal.review.complete", map[string]any{
		"goal_id": goal.ID, "agent_session_id": commanderID,
	}, &domain.Goal{})
	markers = append(markers, "merge-continuation")
	if markers[1] != "human-approval" || markers[2] != "merge-continuation" {
		t.Fatalf("approval/merge marker order = %#v", markers)
	}
	completed, err := db.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal after completion: %v", err)
	}
	if completed.Status != domain.GoalDone {
		t.Fatalf("goal status = %q, want done", completed.Status)
	}

	// One delivery key per phase: reconciliation must not duplicate a turn.
	for name, watch := range map[string]workflowMonitorWatch{"commander": commanderWatch, "subcommander": subcommanderWatch} {
		seen := map[string]int{}
		for _, action := range watch.recorder.snapshot() {
			seen[action.deliveryKey]++
		}
		for key, n := range seen {
			if n > 1 {
				t.Errorf("%s received delivery %q %d times", name, key, n)
			}
		}
	}

	stopWatches()
	for name, watch := range map[string]workflowMonitorWatch{
		"commander": commanderWatch, "subcommander": subcommanderWatch,
		"executor A": executorAWatch, "executor B": executorBWatch,
	} {
		select {
		case err := <-watch.done:
			if err != nil && !strings.Contains(err.Error(), "context canceled") {
				t.Errorf("%s watch: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s watch did not stop", name)
		}
	}
}

// TestWorkflowMonitorBoundWatchDoesNotEchoOwnHandoffEntries runs the production
// bound watch against a real daemon HTTP handler: the SSE connection must carry
// the monitor token so the daemon can drop entries the session itself wrote.
func TestWorkflowMonitorBoundWatchDoesNotEchoOwnHandoffEntries(t *testing.T) {
	ctx, cancel, db, server, client, root := startWorkflowMonitorDaemon(t)
	defer cancel()

	const (
		commanderToken = "entry-echo-commander"
		subToken       = "entry-echo-sub"
	)
	commanderID := registerWorkflowMonitorSession(t, ctx, client, root, "entry-echo-commander", commanderToken)
	subcommanderID := registerWorkflowMonitorSession(t, ctx, client, root, "entry-echo-subcommander", subToken)
	projects, err := db.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = (%#v, %v), want one project", projects, err)
	}
	callWorkflowMonitorRPC(t, ctx, client, "project.claim", map[string]any{
		"project_id": projects[0].ID, "agent_session_id": commanderID,
	}, &domain.Project{})
	var goal domain.Goal
	callWorkflowMonitorRPC(t, ctx, client, "goal.create", map[string]any{
		"cwd": root, "content": "entry echo", "creator": "human",
	}, &goal)

	projectID := strconv.FormatInt(projects[0].ID, 10)
	goalID := strconv.FormatInt(goal.ID, 10)
	watchClient := &http.Client{Transport: &workflowMonitorTransport{base: server.Client().Transport}}
	watchCtx, stopWatches := context.WithCancel(ctx)
	defer stopWatches()

	commanderWatch := startWorkflowMonitorBoundWatch(watchCtx, watchClient, server.URL, root, commanderToken)
	waitWorkflowMonitorScope(t, commanderWatch.scopes, func(scope watchScope) bool {
		return scope.Role == "commander" && scope.ProjectID == projectID
	})

	// The commander's own request entry must not come back to the commander.
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.request", map[string]any{
		"handoff_id": "gh-entry-echo", "goal_id": goal.ID, "requested_by": commanderID,
		"request_report": "goal ready for implementation",
	}, &store.GoalHandoff{})
	assertNoWorkflowMonitorAction(t, commanderWatch.recorder, "handoff_entry_added", "goal "+goalID)

	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.receive", map[string]any{
		"handoff_id": "gh-entry-echo", "goal_id": goal.ID, "received_by": subcommanderID,
	}, &map[string]any{})
	subWatch := startWorkflowMonitorBoundWatch(watchCtx, watchClient, server.URL, root, subToken)
	waitWorkflowMonitorScope(t, subWatch.scopes, func(scope watchScope) bool {
		return scope.Role == "subcommander" && scope.GoalID == goalID
	})

	// An entry the subcommander writes reaches the commander (a party), never the subcommander.
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.review.request", map[string]any{
		"handoff_id": "gh-entry-echo", "goal_id": goal.ID, "requested_by": subcommanderID,
		"review_request_report": "ready for review",
	}, &store.GoalHandoff{})
	waitWorkflowMonitorAction(t, commanderWatch.recorder, "handoff_entry_added", "author "+strconv.FormatInt(subcommanderID, 10))
	assertNoWorkflowMonitorAction(t, subWatch.recorder, "handoff_entry_added", "author "+strconv.FormatInt(subcommanderID, 10))
	assertNoWorkflowMonitorAction(t, commanderWatch.recorder, "handoff_entry_added", "author "+strconv.FormatInt(commanderID, 10))
}
