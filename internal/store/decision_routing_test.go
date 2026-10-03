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
	approval0 := ask(domain.KindGoalApproval, 0)
	review0 := ask(domain.KindGoalReview, 0)
	decision0 := ask(domain.KindDecision, 0)
	other := ask(domain.KindDecision, testSessionID("routing-other"))

	roles := reconciledTargetRoles(t, s, goalID)
	for name, id := range map[string]int64{"goal_approval": approval0, "goal_review": review0, "decision": decision0} {
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

func TestListUnappliedHidesDroppedGoalApproval(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	droppedID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, droppedID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	active, err := s.CreateGoal(ctx, goal.ProjectID, "active", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	activeID := active.ID
	answer := func(goalID int64, kind domain.DecisionKind) int64 {
		t.Helper()
		d, err := s.AskDecision(ctx, AskInput{GoalID: goalID, Kind: kind, Question: "q", AgentSessionID: 0})
		if err != nil {
			t.Fatalf("AskDecision: %v", err)
		}
		if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: d.ID, AnswerText: "a"}); err != nil {
			t.Fatalf("AnswerDecision: %v", err)
		}
		return d.ID
	}
	dropped := answer(droppedID, domain.KindGoalApproval)
	activeDecision := answer(activeID, domain.KindGoalApproval)
	if _, err := s.DB().ExecContext(ctx, `UPDATE goals SET status = 'dropped' WHERE id = ?`, droppedID); err != nil {
		t.Fatalf("drop goal: %v", err)
	}

	lists := map[string]func() ([]domain.Decision, error){
		"all":          func() ([]domain.Decision, error) { return s.ListUnappliedDecisions(ctx) },
		"project":      func() ([]domain.Decision, error) { return s.ListUnappliedDecisionsForProject(ctx, goal.ProjectID) },
		"goal-dropped": func() ([]domain.Decision, error) { return s.ListUnappliedDecisionsForGoal(ctx, droppedID) },
		"goal-active":  func() ([]domain.Decision, error) { return s.ListUnappliedDecisionsForGoal(ctx, activeID) },
	}
	for name, list := range lists {
		got, err := list()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		has := map[int64]bool{}
		for _, d := range got {
			has[d.ID] = true
		}
		if has[dropped] {
			t.Errorf("%s: dropped goal's goal_approval listed", name)
		}
		if name != "goal-dropped" && !has[activeDecision] {
			t.Errorf("%s: active goal's answered goal_approval missing", name)
		}
	}
}
