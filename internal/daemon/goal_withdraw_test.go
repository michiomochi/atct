package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
)

func withdrawGoal(t *testing.T, fixture goalListFixture, goalID, agentSessionID int64, reason string) error {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"goal_id":          goalID,
		"agent_session_id": agentSessionID,
		"reason":           reason,
	})
	if err != nil {
		t.Fatalf("marshal goal.withdraw params: %v", err)
	}
	_, err = fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.withdraw", Params: params})
	return err
}

// Until now an agent could only end a goal by completing it, which needs a
// human to approve a report for work that is being abandoned rather than
// finished. Withdrawing existed, but only over HTTP from the web UI.
func TestGoalWithdrawLetsTheCommanderDropAnActiveGoal(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	goalID := fixture.active[0].ID
	commanderID := daemonTestSessionID(t, fixture.store, "withdraw-commander")
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	if err := withdrawGoal(t, fixture, goalID, commanderID, "superseded by goal 300"); err != nil {
		t.Fatalf("goal.withdraw: %v", err)
	}

	goal, err := fixture.store.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if goal.Status != domain.GoalDropped {
		t.Fatalf("goal status = %q, want %q", goal.Status, domain.GoalDropped)
	}
	if !strings.Contains(goal.ResultSummary, "superseded by goal 300") {
		t.Fatalf("result summary = %q, want the withdrawal reason", goal.ResultSummary)
	}
}

// Withdrawing throws away work that other sessions may be doing, so it belongs
// to the one role that owns the project.
func TestGoalWithdrawRefusesAnyoneButTheCommander(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	goalID := fixture.active[0].ID
	commanderID := daemonTestSessionID(t, fixture.store, "withdraw-owner")
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	other := daemonTestSessionID(t, fixture.store, "withdraw-outsider")

	err := withdrawGoal(t, fixture, goalID, other, "not mine to drop")
	if err == nil {
		t.Fatal("goal.withdraw accepted a session that is not the commander")
	}
	goal, getErr := fixture.store.GetGoal(ctx, goalID)
	if getErr != nil {
		t.Fatalf("GetGoal: %v", getErr)
	}
	if goal.Status != domain.GoalActive {
		t.Fatalf("goal status = %q after a refused withdrawal, want it untouched", goal.Status)
	}
}

// A goal dropped without a reason leaves no record of why the work stopped.
func TestGoalWithdrawRequiresAReason(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	goalID := fixture.active[0].ID
	commanderID := daemonTestSessionID(t, fixture.store, "withdraw-no-reason")
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	if err := withdrawGoal(t, fixture, goalID, commanderID, "   "); err == nil {
		t.Fatal("goal.withdraw accepted an empty reason")
	}
}
