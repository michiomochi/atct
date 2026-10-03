package store

import (
	"context"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestUnappliedDecisionListsSkipClosedGoals(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "unapplied-closed-goals", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ids := map[domain.GoalStatus]int64{}
	goals := map[domain.GoalStatus]int64{}
	for _, status := range []domain.GoalStatus{domain.GoalActive, domain.GoalDone, domain.GoalDropped} {
		goal, err := s.CreateGoal(ctx, project.ID, "goal "+string(status), "test")
		if err != nil {
			t.Fatalf("CreateGoal(%s): %v", status, err)
		}
		d, err := s.AskDecision(ctx, AskInput{GoalID: goal.ID, Kind: domain.KindDecision, Question: "q", AgentSessionID: testSessionID("closed-" + string(status))})
		if err != nil {
			t.Fatalf("AskDecision(%s): %v", status, err)
		}
		if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: d.ID, AnswerText: "a"}); err != nil {
			t.Fatalf("AnswerDecision(%s): %v", status, err)
		}
		if status != domain.GoalActive {
			if _, err := s.DB().ExecContext(ctx, `UPDATE goals SET status = ?, work_done = 'x', now_possible = 'x', how_to_verify = 'x', surprises = 'x', needs_review = 'x' WHERE id = ?`, string(status), goal.ID); err != nil {
				t.Fatalf("set goal status %s: %v", status, err)
			}
		}
		ids[status], goals[status] = d.ID, goal.ID
	}

	all, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		t.Fatalf("ListUnappliedDecisions: %v", err)
	}
	byProject, err := s.ListUnappliedDecisionsForProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListUnappliedDecisionsForProject: %v", err)
	}
	for name, got := range map[string][]domain.Decision{"all": all, "project": byProject} {
		if len(got) != 1 || got[0].ID != ids[domain.GoalActive] {
			t.Fatalf("%s list = %v, want only the active-goal decision %d", name, got, ids[domain.GoalActive])
		}
	}
	for status, goalID := range goals {
		got, err := s.ListUnappliedDecisionsForGoal(ctx, goalID)
		if err != nil {
			t.Fatalf("ListUnappliedDecisionsForGoal(%s): %v", status, err)
		}
		want := 0
		if status == domain.GoalActive {
			want = 1
		}
		if len(got) != want {
			t.Fatalf("goal %s: got %d unapplied decisions, want %d", status, len(got), want)
		}
	}
}
