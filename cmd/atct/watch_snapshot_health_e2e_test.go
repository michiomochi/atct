package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

// bindToken is the monitor token the commander's bound watch runs with.
const bindToken = "snapshot-health-e2e-commander"

// commanderProject is a test daemon with one project claimed by a commander whose
// session carries bindToken.
type commanderProject struct {
	ctx       context.Context
	cancel    context.CancelFunc
	db        *store.Store
	server    *httptest.Server
	client    *mcpshim.Client
	root      string
	projectID int64
	commander int64
}

func startCommanderProject(t *testing.T) commanderProject {
	t.Helper()
	ctx, cancel, db, server, client, root := startWorkflowMonitorDaemon(t)
	commanderID := registerWorkflowMonitorSession(t, ctx, client, root, "snapshot-health-e2e-commander", bindToken)
	projects, err := db.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		cancel()
		t.Fatalf("ListProjects = (%#v, %v), want one project", projects, err)
	}
	callWorkflowMonitorRPC(t, ctx, client, "project.claim", map[string]any{
		"project_id": projects[0].ID, "agent_session_id": commanderID,
	}, &domain.Project{})
	return commanderProject{ctx: ctx, cancel: cancel, db: db, server: server, client: client, root: root, projectID: projects[0].ID, commander: commanderID}
}

// waitForHealthState polls the monitor health history until state appears, which is
// how a test knows a bound watch has reconciled once.
func (p commanderProject) waitForHealthState(t *testing.T, state string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if containsState(monitorHealthStates(t, p.ctx, p.db, p.projectID), state) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q monitor health row within 10s", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// openDecisionFor returns the open decision the test just asked on goalID.
func (p commanderProject) openDecisionFor(t *testing.T, goalID int64) domain.Decision {
	t.Helper()
	open, err := p.db.ListAllOpenDecisions(p.ctx)
	if err != nil {
		t.Fatalf("ListAllOpenDecisions: %v", err)
	}
	for _, d := range open {
		if d.GoalID == goalID {
			return d
		}
	}
	t.Fatalf("no open decision on goal %d", goalID)
	return domain.Decision{}
}

// TestSnapshotHealthEndToEndAnsweredDecisionReachesBoundWatch asks a decision on
// the test daemon, answers it as a human would through the HTTP API, and expects the
// bound Monitor watch to print the answered line.
func TestSnapshotHealthEndToEndAnsweredDecisionReachesBoundWatch(t *testing.T) {
	p := startCommanderProject(t)
	defer p.cancel()

	watch := startBoundLoop(p.ctx, p.server, p.server.URL, t.TempDir(), p.root, bindToken, false)
	defer watch.stop(t)
	p.waitForHealthState(t, "healthy")

	var goal domain.Goal
	callWorkflowMonitorRPC(t, p.ctx, p.client, "goal.create", map[string]any{
		"cwd": p.root, "content": "decision delivery e2e", "creator": "human",
	}, &goal)
	callWorkflowMonitorRPC(t, p.ctx, p.client, "decision.ask", map[string]any{
		"goal_id": goal.ID, "question": "Deliver this answer?",
		"options": []domain.Option{{Label: "A"}, {Label: "B"}}, "agent_session_id": p.commander,
	}, &map[string]any{})
	decision := p.openDecisionFor(t, goal.ID)

	body, err := json.Marshal(map[string]string{"answer_label": "A"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.server.Client().Post(fmt.Sprintf("%s/api/decisions/%d/answer", p.server.URL, decision.ID), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("answer decision: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answer decision status = %d", resp.StatusCode)
	}

	watch.waitOutput(t, fmt.Sprintf("atct decision answered (decision_id: %d)", decision.ID))
}

// TestSnapshotHealthEndToEndFailureAndRecoveryReachBoundWatch makes /api/inbox
// answer 500 through a wrapper in front of the daemon, and expects the bound watch
// to print the failure once it has lasted two minutes, and the recovery once the
// snapshot works again. The notifier clock moves two minutes per reading, so the
// test does not wait in real time beyond one reconnect interval.
func TestSnapshotHealthEndToEndFailureAndRecoveryReachBoundWatch(t *testing.T) {
	p := startCommanderProject(t)
	defer p.cancel()

	previous := snapshotFailureNow
	fake := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	snapshotFailureNow = func() time.Time {
		fake = fake.Add(2 * time.Minute)
		return fake
	}
	t.Cleanup(func() { snapshotFailureNow = previous })

	var inboxFails atomic.Bool
	inboxFails.Store(true)
	daemon := p.server
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/inbox" && inboxFails.Load() {
			http.Error(w, "injected failure", http.StatusInternalServerError)
			return
		}
		daemon.Config.Handler.ServeHTTP(w, r)
	}))
	defer wrapper.Close()

	watch := startBoundLoopAt(p.ctx, wrapper, t.TempDir(), p.root, bindToken)
	defer watch.stop(t)

	watch.waitOutput(t, "ATCT: daemon snapshot failing for 2m: ")
	inboxFails.Store(false)
	out := watch.waitOutput(t, "ATCT: daemon snapshot recovered after ")
	if n := strings.Count(out, "ATCT: daemon snapshot failing"); n != 1 {
		t.Fatalf("failure line count = %d, want 1; output %q", n, out)
	}
}

// startBoundLoopAt runs the bound watch against the wrapper's URL, so the snapshot
// path can be made to fail while the daemon still serves the rest.
func startBoundLoopAt(ctx context.Context, server *httptest.Server, dir, cwd, token string) *boundLoopRun {
	runCtx, cancel := context.WithCancel(ctx)
	run := &boundLoopRun{out: &lockedBuffer{}, done: make(chan error, 1), cancel: cancel}
	client := &http.Client{}
	go func() {
		run.done <- runBoundWatchLoop(runCtx, dir, cwd, client, []string{server.URL}, token, run.out, false, nil)
	}()
	return run
}
