package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
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

type boundLoopRun struct {
	out    *lockedBuffer
	done   chan error
	cancel context.CancelFunc
}

func startBoundLoop(ctx context.Context, server *httptest.Server, baseURL, dir, cwd, token string, once bool) *boundLoopRun {
	runCtx, cancel := context.WithCancel(ctx)
	run := &boundLoopRun{out: &lockedBuffer{}, done: make(chan error, 1), cancel: cancel}
	client := &http.Client{Transport: server.Client().Transport}
	go func() {
		run.done <- runBoundWatchLoop(runCtx, dir, cwd, client, []string{baseURL}, token, run.out, once, nil)
	}()
	return run
}

func (r *boundLoopRun) waitOutput(t *testing.T, contains string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(r.out.String(), contains) {
		if time.Now().After(deadline) {
			t.Fatalf("no output containing %q within 10s; output = %q", contains, r.out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r.out.String()
}

func (r *boundLoopRun) waitReturn(t *testing.T) {
	t.Helper()
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatalf("runBoundWatchLoop returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runBoundWatchLoop did not return within 10s")
	}
}

// assertQuiet checks that nothing was printed and the loop kept running.
func (r *boundLoopRun) assertQuiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case err := <-r.done:
		t.Fatalf("runBoundWatchLoop returned (%v), want it to keep running; output = %q", err, r.out.String())
	case <-time.After(d):
	}
	if got := r.out.String(); got != "" {
		t.Fatalf("unexpected output %q", got)
	}
}

func (r *boundLoopRun) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	r.waitReturn(t)
}

func monitorHealthStates(t *testing.T, ctx context.Context, db *store.Store, projectID int64) []string {
	t.Helper()
	history, err := db.ListMonitorHealthHistory(ctx, projectID)
	if err != nil {
		t.Fatalf("ListMonitorHealthHistory: %v", err)
	}
	var states []string
	for _, h := range history {
		states = append(states, h.State)
	}
	return states
}

func containsState(states []string, want string) bool {
	for _, s := range states {
		if s == want {
			return true
		}
	}
	return false
}

// TestWatchRecordAcrossOnceAndMonitorEndToEnd drives runBoundWatchLoop against
// a test daemon: --once ends on its first delivery and leaves a re-arm grace,
// and the delivery record is shared by --once and the Monitor watch, so an
// open plan review is not shown twice for one token.
func TestWatchRecordAcrossOnceAndMonitorEndToEnd(t *testing.T) {
	ctx, cancel, db, server, client, root := startWorkflowMonitorDaemon(t)
	defer cancel()
	const token = "watch-once-e2e-commander"
	commanderID := registerWorkflowMonitorSession(t, ctx, client, root, "watch-once-e2e-commander", token)
	projects, err := db.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = (%#v, %v), want one project", projects, err)
	}
	project := projects[0]
	callWorkflowMonitorRPC(t, ctx, client, "project.claim", map[string]any{
		"project_id": project.ID, "agent_session_id": commanderID,
	}, &domain.Project{})

	dir := t.TempDir()

	// (a) nothing to report: quiet and still running.
	quiet := startBoundLoop(ctx, server, server.URL, dir, root, token, true)
	quiet.assertQuiet(t, 2*time.Second)

	// (b) the first goal ends the --once watch and leaves the re-arm grace.
	var goal domain.Goal
	callWorkflowMonitorRPC(t, ctx, client, "goal.create", map[string]any{
		"cwd": root, "content": "watch once delivery", "creator": "human",
	}, &goal)
	out := quiet.waitOutput(t, "atct goal created (goal_id: ")
	if n := strings.Count(strings.TrimSpace(out), "\n") + 1; n != 1 {
		t.Fatalf("output has %d lines, want 1: %q", n, out)
	}
	quiet.waitReturn(t)
	live, err := db.HasLiveMonitorForScope(ctx, store.MonitorLiveScope{ProjectID: project.ID, Role: "commander"})
	if err != nil || !live {
		t.Fatalf("HasLiveMonitorForScope = (%v, %v), want true during the re-arm grace", live, err)
	}
	if states := monitorHealthStates(t, ctx, db, project.ID); !containsState(states, "rearming") {
		t.Fatalf("health states = %v, want a rearming row", states)
	}
	if !db.AgentSessionLive(ctx, commanderID, time.Now().Add(60*time.Second)) {
		t.Fatal("commander session lease does not cover the re-arm grace")
	}

	// (c) an open plan review reaches a fresh --once watch.
	subID := registerWorkflowMonitorSession(t, ctx, client, root, "watch-once-e2e-sub", "watch-once-e2e-sub-token")
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.request", map[string]any{
		"handoff_id": "gh-watch-once-e2e", "goal_id": goal.ID, "requested_by": commanderID,
		"request_report": "goal ready for implementation",
	}, &store.GoalHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.receive", map[string]any{
		"handoff_id": "gh-watch-once-e2e", "goal_id": goal.ID, "received_by": subID,
	}, &map[string]any{})
	callWorkflowMonitorRPC(t, ctx, client, "goal.update_request_report", map[string]any{
		"goal_id": goal.ID, "spec": "# Spec", "plan": "# Plan", "agent_session_id": subID,
	}, &domain.Goal{})
	callWorkflowMonitorRPC(t, ctx, client, "plan.handoff.review.request", map[string]any{
		"handoff_id": "ph-watch-once-e2e", "goal_id": goal.ID, "requested_by": subID,
		"review_request_report": "plan is ready for review",
	}, &store.PlanHandoff{})
	review := startBoundLoop(ctx, server, server.URL, dir, root, token, true)
	review.waitOutput(t, "handoff_id: ph-watch-once-e2e")
	review.waitReturn(t)

	// (d) the same state dir does not repeat the open plan review; another one does.
	again := startBoundLoop(ctx, server, server.URL, dir, root, token, true)
	again.assertQuiet(t, 2*time.Second)
	again.stop(t)
	fresh := startBoundLoop(ctx, server, server.URL, t.TempDir(), root, token, true)
	fresh.waitOutput(t, "handoff_id: ph-watch-once-e2e")
	fresh.waitReturn(t)

	// (e) the Monitor watch shares the record: no repeat, new events still show,
	// the loop keeps running, and ending it reports stopped.
	monitor := startBoundLoop(ctx, server, server.URL, dir, root, token, false)
	monitor.assertQuiet(t, 2*time.Second)
	var other domain.Goal
	callWorkflowMonitorRPC(t, ctx, client, "goal.create", map[string]any{
		"cwd": root, "content": "monitor delivery", "creator": "human",
	}, &other)
	monitor.waitOutput(t, "atct goal created (goal_id: ")
	select {
	case err := <-monitor.done:
		t.Fatalf("Monitor watch returned (%v) after a delivery, want it to keep running", err)
	case <-time.After(1500 * time.Millisecond):
	}
	if strings.Contains(monitor.out.String(), "handoff_id: ph-watch-once-e2e") {
		t.Fatalf("Monitor watch repeated the open plan review: %q", monitor.out.String())
	}
	monitor.stop(t)
	states := monitorHealthStates(t, ctx, db, project.ID)
	if len(states) == 0 || states[len(states)-1] != "stopped" {
		t.Fatalf("health states after the Monitor watch ended = %v, want the last to be stopped", states)
	}
}
