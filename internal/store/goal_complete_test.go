package store

import (
	"context"
	"errors"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestCompleteGoalRejectedWhenOpenDecisionExists(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: "Unresolved", RunID: "run-1",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	_, err := s.CompleteGoal(ctx, goalID, "Done", "run-1")
	if !errors.Is(err, ErrGoalHasOpenDecision) {
		t.Fatalf("err = %v, want ErrGoalHasOpenDecision", err)
	}
}

func TestApproveCompletionClosesGoalImmediately(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d, err := s.CompleteGoal(ctx, goalID, "All tasks complete", "run-1")
	if err != nil {
		t.Fatalf("CompleteGoal: %v", err)
	}
	if d.Kind != domain.KindCompletion {
		t.Fatalf("kind = %q, want %q", d.Kind, domain.KindCompletion)
	}

	g, err := s.ApproveCompletion(ctx, d.ID, "human")
	if err != nil {
		t.Fatalf("ApproveCompletion: %v", err)
	}
	if g.Status != domain.GoalDone {
		t.Fatalf("goal status = %q, want %q", g.Status, domain.GoalDone)
	}
	if g.ResultSummary != "All tasks complete" {
		t.Fatalf("result_summary = %q, want %q", g.ResultSummary, "All tasks complete")
	}

	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionApplied {
		t.Fatalf("decision status = %q, want %q (approval becomes applied immediately because no follow-up work exists)",
			got.Status, domain.DecisionApplied)
	}
}

func TestRejectCompletionKeepsGoalActiveAndAwaitsAgent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d, err := s.CompleteGoal(ctx, goalID, "Thought it was done", "run-1")
	if err != nil {
		t.Fatalf("CompleteGoal: %v", err)
	}
	if err := s.RejectCompletion(ctx, d.ID, "Insufficient tests", "human"); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}

	g, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if g.Status != domain.GoalActive {
		t.Fatalf("goal status = %q, want %q", g.Status, domain.GoalActive)
	}

	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered {
		t.Fatalf("decision status = %q, want %q (rejection remains answered until the agent receives it)",
			got.Status, domain.DecisionAnswered)
	}
}
