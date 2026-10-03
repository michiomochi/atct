package store

import (
	"context"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func reconciledTargetRoles(t *testing.T, s *Store, goalID int64) map[int64]string {
	t.Helper()
	goal, err := s.GetGoal(context.Background(), goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	reconciliation, err := s.ReconcileWorkflow(context.Background(), WorkflowEventQuery{ProjectID: goal.ProjectID, GoalID: goalID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	roles := map[int64]string{}
	for _, d := range reconciliation.Decisions {
		roles[d.ID] = d.TargetRole
	}
	return roles
}

func TestDecisionTargetRoleRoutesByKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	ask := func(kind domain.DecisionKind, session int64) int64 {
		t.Helper()
		d, err := s.AskDecision(ctx, AskInput{GoalID: goalID, Kind: kind, Question: "q", AgentSessionID: session})
		if err != nil {
			t.Fatalf("AskDecision(%v, %d): %v", kind, session, err)
		}
		return d.ID
	}
	review0 := ask(domain.KindGoalReview, 0)
	decision0 := ask(domain.KindDecision, 0)
	other := ask(domain.KindDecision, testSessionID("routing-other"))

	roles := reconciledTargetRoles(t, s, goalID)
	for name, id := range map[string]int64{"goal_review": review0, "decision": decision0} {
		if roles[id] != "commander" {
			t.Errorf("session 0 %s target_role = %q, want commander", name, roles[id])
		}
	}
	if roles[other] != "executor" {
		t.Errorf("unrelated session decision target_role = %q, want executor", roles[other])
	}
}

func TestDecisionTargetRoleSubcommanderUnchanged(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "routing-commander")
	addTestAgentSession(t, s, "routing-sub")
	commander := testSessionID("routing-commander")
	sub := testSessionID("routing-sub")
	if _, err := s.RequestGoalHandoff(ctx, "routing-handoff", goalID, commander, "delegate"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, "routing-handoff", goalID, sub); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	d, err := s.AskDecision(ctx, AskInput{GoalID: goalID, Kind: domain.KindDecision, Question: "q", AgentSessionID: sub})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if got := reconciledTargetRoles(t, s, goalID)[d.ID]; got != "subcommander" {
		t.Errorf("subcommander decision target_role = %q, want subcommander", got)
	}
}
