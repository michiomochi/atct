package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

// The daemon serves store.ReconcileWorkflow verbatim and watch decodes it into
// watchReconciliation. The two are matched only by field name: the store types
// carry no json tags, so watch spells the Go names out (`json:"ID"`).
//
// Case alone is safe, because encoding/json falls back to a case-insensitive
// match. Renaming is not: tagging store.GoalHandoff.ID as `json:"handoff_id"`
// leaves watch decoding an empty ID while the request still returns 200, so
// the scope filter sees nothing and no turn is ever started. These tests fail
// on that rename.
//
// The existing bridge tests feed hand-written reconcile payloads, so they
// cannot catch it. Serve the real handler over real flow state instead.

func newReconcileContractStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	project, err := s.CreateProject(context.Background(), "atct", filepath.Join(dir, "repo"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return s, project.ID
}

func newReconcileContractSession(t *testing.T, s *store.Store, projectID int64) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, id, projectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	return id
}

func claimReconcileContractProject(t *testing.T, s *store.Store, projectID, sessionID int64) {
	t.Helper()
	if _, err := s.ClaimProject(context.Background(), projectID, sessionID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
}

func TestReconcileContractDeliversRealGoalHandoffToCodexBridge(t *testing.T) {
	ctx := context.Background()
	s, projectID := newReconcileContractStore(t)
	commanderID := newReconcileContractSession(t, s, projectID)
	subcommanderID := newReconcileContractSession(t, s, projectID)
	claimReconcileContractProject(t, s, projectID, commanderID)

	goal, err := s.CreateGoal(ctx, projectID, "contract goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := s.RequestGoalHandoff(ctx, "gh-contract", goal.ID, commanderID, "deliver it"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}

	server := httptest.NewServer(httpapi.New(s).Handler())
	t.Cleanup(server.Close)

	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	lastWakeupContent := ""
	if err := reconcileWatchScope(
		ctx, server.Client(), server.URL,
		watchScope{ProjectID: strconv.FormatInt(projectID, 10)}, io.Discard,
		make(map[watchDeliveryKey]struct{}), &lastWakeupContent,
		make(map[watchWakeupDiscrepancyDeliveryKey]struct{}),
		make(map[watchWakeupDeliveryKey]struct{}),
		newWatchScopeFilter(""), bridge.LineSink(), bridge.ActionSink(),
	); err != nil {
		t.Fatalf("reconcileWatchScope: %v", err)
	}

	calls := starter.callsSnapshot()
	if len(calls) == 0 {
		t.Fatalf("an open goal handoff produced no Codex turn; the reconcile payload did not survive decoding")
	}
	_ = subcommanderID
}

// The decoded payload must carry the handoff fields watch keys its decisions
// on. A retagged store type would leave these empty while the request still
// returns 200.
func TestReconcileContractPreservesHandoffFields(t *testing.T) {
	ctx := context.Background()
	s, projectID := newReconcileContractStore(t)
	commanderID := newReconcileContractSession(t, s, projectID)
	claimReconcileContractProject(t, s, projectID, commanderID)

	goal, err := s.CreateGoal(ctx, projectID, "field contract goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := s.RequestGoalHandoff(ctx, "gh-fields", goal.ID, commanderID, "deliver it"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}

	server := httptest.NewServer(httpapi.New(s).Handler())
	t.Cleanup(server.Close)

	state := decodeReconciliation(t, server.Client(), server.URL, projectID)
	if len(state.GoalHandoffs) != 1 {
		t.Fatalf("decoded goal handoffs = %d, want 1", len(state.GoalHandoffs))
	}
	handoff := state.GoalHandoffs[0]
	if handoff.ID != "gh-fields" {
		t.Fatalf("decoded handoff ID = %q, want %q; the field names no longer line up", handoff.ID, "gh-fields")
	}
	if handoff.GoalID != goal.ID {
		t.Fatalf("decoded handoff GoalID = %d, want %d", handoff.GoalID, goal.ID)
	}
	if handoff.RequestedAt == nil {
		t.Fatal("decoded handoff RequestedAt is nil; watch cannot tell a requested handoff from an unrequested one")
	}
	if handoff.ReceivedAt != nil {
		t.Fatalf("decoded handoff ReceivedAt = %v, want nil before receipt", *handoff.ReceivedAt)
	}
	if len(state.Goals) != 1 || state.Goals[0].ID != strconv.FormatInt(goal.ID, 10) {
		t.Fatalf("decoded goals = %#v, want the one active goal", state.Goals)
	}
	if state.Goals[0].Status != string(domain.GoalActive) {
		t.Fatalf("decoded goal status = %q, want %q", state.Goals[0].Status, domain.GoalActive)
	}
}

func decodeReconciliation(t *testing.T, client *http.Client, baseURL string, projectID int64) watchReconciliation {
	t.Helper()
	var state watchReconciliation
	lastWakeupContent := ""
	if err := reconcileWatchScope(
		context.Background(), client, baseURL,
		watchScope{ProjectID: strconv.FormatInt(projectID, 10)}, io.Discard,
		make(map[watchDeliveryKey]struct{}), &lastWakeupContent,
		make(map[watchWakeupDiscrepancyDeliveryKey]struct{}),
		make(map[watchWakeupDeliveryKey]struct{}),
		newWatchScopeFilter(""), nil, &state,
	); err != nil {
		t.Fatalf("reconcileWatchScope: %v", err)
	}
	return state
}

// The watch asks every handoff which goal it belongs to, and answers a
// subcommander's "is my executor still working?" with it. Task handoffs were
// sent without a goal, so that question always answered no and the
// subcommander was prompted every minute while its executor held the task.
func TestReconcileContractCarriesTheGoalOnTaskHandoffs(t *testing.T) {
	ctx := context.Background()
	s, projectID := newReconcileContractStore(t)
	commanderID := newReconcileContractSession(t, s, projectID)
	claimReconcileContractProject(t, s, projectID, commanderID)

	goal, err := s.CreateGoal(ctx, projectID, "task handoff goal contract", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	// A task handoff needs the goal handoff above it, the way delegation works.
	if _, err := s.RequestGoalHandoff(ctx, "gh-for-task-contract", goal.ID, commanderID, "delegate"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, "gh-for-task-contract", goal.ID, commanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	tasks, err := s.CreateTasks(ctx, goal.ID, "agent", "task-handoff-contract", []string{"do it"}, []string{"A task to hand off."})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	if _, err := s.RequestTaskHandoff(ctx, "th-goal-contract", tasks[0].ID, commanderID, "deliver it"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}

	server := httptest.NewServer(httpapi.New(s).Handler())
	t.Cleanup(server.Close)

	state := decodeReconciliation(t, server.Client(), server.URL, projectID)
	if len(state.TaskHandoffs) != 1 {
		t.Fatalf("decoded task handoffs = %d, want 1", len(state.TaskHandoffs))
	}
	handoff := state.TaskHandoffs[0]
	if handoff.TaskID != tasks[0].ID {
		t.Fatalf("decoded handoff TaskID = %d, want %d", handoff.TaskID, tasks[0].ID)
	}
	if handoff.GoalID != goal.ID {
		t.Fatalf("decoded task handoff GoalID = %d, want %d; the watch cannot scope it to a goal", handoff.GoalID, goal.ID)
	}
}
