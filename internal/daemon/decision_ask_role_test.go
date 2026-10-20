package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
)

func askDecisionAs(t *testing.T, f stopCheckFixture, agentSessionID int64) error {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"goal_id": f.goalID, "question": "ship it?", "wait_ms": 0, "agent_session_id": agentSessionID,
	})
	if err != nil {
		t.Fatalf("marshal decision.ask params: %v", err)
	}
	_, err = f.daemon.dispatch(context.Background(), rpc.Request{Method: "decision.ask", Params: params})
	return err
}

func openDecisionCount(t *testing.T, f stopCheckFixture) int {
	t.Helper()
	decisions, err := f.store.ListDecisionsForGoal(context.Background(), f.goalID)
	if err != nil {
		t.Fatalf("ListDecisionsForGoal: %v", err)
	}
	return len(decisions)
}

func TestDecisionAskRefusesSubcommander(t *testing.T) {
	f := newStopCheckFixture(t)
	defer f.store.Close()
	stopCheckGoalHandoff(t, f, true)

	err := askDecisionAs(t, f, f.subcommanderID)
	if err == nil || !strings.Contains(err.Error(), "atct_goal_handoff_entry_append") {
		t.Fatalf("subcommander decision.ask error = %v, want a refusal naming atct_goal_handoff_entry_append", err)
	}
	if n := openDecisionCount(t, f); n != 0 {
		t.Fatalf("decisions after refused ask = %d, want 0", n)
	}
}

func TestDecisionAskAllowsCommanderAndExecutor(t *testing.T) {
	f := newStopCheckFixture(t)
	defer f.store.Close()

	if err := askDecisionAs(t, f, f.executorID); err != nil {
		t.Fatalf("executor (no role) decision.ask: %v", err)
	}
	if err := askDecisionAs(t, f, f.commanderID); err != nil {
		t.Fatalf("commander decision.ask: %v", err)
	}
	if n := openDecisionCount(t, f); n != 2 {
		t.Fatalf("decisions = %d, want 2", n)
	}
}

func TestDecisionAskAllowsCommanderHoldingGoalHandoff(t *testing.T) {
	f := newStopCheckFixture(t)
	defer f.store.Close()
	// two-layer: the commander receives the goal handoff itself and stays a commander.
	handoff := stopCheckGoalHandoff(t, f, false)
	if _, err := f.store.ReceiveGoalHandoff(context.Background(), handoff.ID, f.goalID, f.commanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	role, err := f.daemon.deriveSessionRole(context.Background(), f.commanderID)
	if err != nil || role.Role != "commander" {
		t.Fatalf("role = %+v, err = %v, want commander", role, err)
	}
	if err := askDecisionAs(t, f, f.commanderID); err != nil {
		t.Fatalf("commander with goal handoff decision.ask: %v", err)
	}
	if n := openDecisionCount(t, f); n != 1 {
		t.Fatalf("decisions = %d, want 1", n)
	}
}
