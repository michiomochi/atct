package store

import (
	"context"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestPollMarksApplied(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: "What should we do?", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{
		DecisionID: d.ID, AnswerText: "A", AnsweredBy: "human",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}

	unapplied, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		t.Fatalf("ListUnappliedDecisions: %v", err)
	}
	if len(unapplied) != 1 {
		t.Fatalf("%d unapplied, want 1", len(unapplied))
	}

	got, err := s.PollDecisions(ctx, "run-1", "")
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(got) != 1 || got[0].Status != domain.DecisionApplied {
		t.Fatalf("poll returned %+v, want 1 applied decision", got)
	}

	again, err := s.PollDecisions(ctx, "run-1", "")
	if err != nil {
		t.Fatalf("second PollDecisions: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second poll returned %d decisions, want 0", len(again))
	}
}
