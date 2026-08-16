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

func TestPollAdoptsDecisionByExplicitIDAcrossRuns(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d := answeredDecisionForRun(t, s, goalID, "run-a")
	got, err := s.PollDecisions(ctx, "run-b", d.ID)
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(got) != 1 || got[0].ID != d.ID || got[0].Status != domain.DecisionApplied {
		t.Fatalf("poll returned %+v, want decision %q applied", got, d.ID)
	}
}

func TestPollWithoutDecisionIDKeepsRunFilter(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d := answeredDecisionForRun(t, s, goalID, "run-a")
	got, err := s.PollDecisions(ctx, "run-b", "")
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("poll returned %+v, want no decisions", got)
	}

	unapplied, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		t.Fatalf("ListUnappliedDecisions: %v", err)
	}
	if len(unapplied) != 1 || unapplied[0].ID != d.ID {
		t.Fatalf("unapplied decisions = %+v, want decision %q", unapplied, d.ID)
	}
}

func TestPollAdoptionPreservesOriginalRunID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d := answeredDecisionForRun(t, s, goalID, "run-a")
	got, err := s.PollDecisions(ctx, "run-b", d.ID)
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("poll returned %d decisions, want 1", len(got))
	}
	if got[0].RunID != "run-a" {
		t.Fatalf("adopted decision run_id = %q, want original run-a", got[0].RunID)
	}
}

func answeredDecisionForRun(t *testing.T, s *Store, goalID, runID string) domain.Decision {
	t.Helper()
	ctx := context.Background()
	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: "What should we do?", RunID: runID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{
		DecisionID: d.ID, AnswerText: "A", AnsweredBy: "human",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	return d
}
