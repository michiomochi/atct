package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func TestListAppliedDecisionsReturnsOnlyAppliedDecisionsForGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	applied := createAppliedHistoryDecision(t, s, goalID, "applied", time.Time{})
	open, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: "open", RunID: "open-run",
	})
	if err != nil {
		t.Fatalf("AskDecision open: %v", err)
	}
	answered, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: "answered", RunID: "answered-run",
	})
	if err != nil {
		t.Fatalf("AskDecision answered: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{
		DecisionID: answered.ID, AnswerLabel: "yes", AnswerText: "answered but not applied",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}

	otherProject, err := s.CreateProject(ctx, "other-history", "/repos/other-history")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, otherProject.ID, "other-goal", "")
	if err != nil {
		t.Fatalf("CreateGoal other: %v", err)
	}
	createAppliedHistoryDecision(t, s, otherGoal.ID, "other goal", time.Time{})

	got, omitted, err := s.ListAppliedDecisions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListAppliedDecisions: %v", err)
	}
	if omitted != 0 {
		t.Fatalf("omitted = %d, want 0", omitted)
	}
	if len(got) != 1 || got[0].ID != applied.ID {
		t.Fatalf("applied decisions = %+v, want only %q", got, applied.ID)
	}
	for _, d := range got {
		if d.ID == open.ID || d.ID == answered.ID {
			t.Fatalf("non-applied decision %q was returned", d.ID)
		}
	}
}

func TestListAppliedDecisionsReturnsNewestFirst(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)

	oldest := createAppliedHistoryDecision(t, s, goalID, "oldest", base)
	middle := createAppliedHistoryDecision(t, s, goalID, "middle", base.Add(time.Hour))
	newest := createAppliedHistoryDecision(t, s, goalID, "newest", base.Add(2*time.Hour))

	got, omitted, err := s.ListAppliedDecisions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListAppliedDecisions: %v", err)
	}
	if omitted != 0 {
		t.Fatalf("omitted = %d, want 0", omitted)
	}
	wantIDs := []string{newest.ID, middle.ID, oldest.ID}
	if len(got) != len(wantIDs) {
		t.Fatalf("returned %d decisions, want %d", len(got), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if got[i].ID != wantID {
			t.Errorf("decision %d = %q, want %q", i, got[i].ID, wantID)
		}
	}
}

func TestListAppliedDecisionsLimitsToTwentyAndReportsOmittedCount(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	created := make([]domain.Decision, 0, 21)

	for i := 0; i < 21; i++ {
		created = append(created, createAppliedHistoryDecision(
			t, s, goalID, fmt.Sprintf("decision-%02d", i), base.Add(time.Duration(i)*time.Minute),
		))
	}

	got, omitted, err := s.ListAppliedDecisions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListAppliedDecisions: %v", err)
	}
	if len(got) != 20 {
		t.Fatalf("returned %d decisions, want 20", len(got))
	}
	if omitted != 1 {
		t.Fatalf("omitted = %d, want 1", omitted)
	}
	if got[0].ID != created[20].ID {
		t.Fatalf("first decision = %q, want newest %q", got[0].ID, created[20].ID)
	}
	if got[19].ID != created[1].ID {
		t.Fatalf("last decision = %q, want %q", got[19].ID, created[1].ID)
	}
}

func createAppliedHistoryDecision(t *testing.T, s *Store, goalID, question string, answeredAt time.Time) domain.Decision {
	t.Helper()
	ctx := context.Background()
	runID := fmt.Sprintf("history-run-%s", question)
	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: question, RunID: runID,
	})
	if err != nil {
		t.Fatalf("AskDecision %q: %v", question, err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{
		DecisionID: d.ID, AnswerLabel: "yes", AnswerText: "answer for " + question,
	}); err != nil {
		t.Fatalf("AnswerDecision %q: %v", question, err)
	}
	if _, err := s.PollDecisions(ctx, runID, ""); err != nil {
		t.Fatalf("PollDecisions %q: %v", question, err)
	}
	if !answeredAt.IsZero() {
		appliedAt := answeredAt.Add(time.Minute)
		if _, err := s.db.ExecContext(ctx,
			`UPDATE decisions SET answered_at = ?, applied_at = ? WHERE id = ?`,
			answeredAt.UTC().Format(time.RFC3339), appliedAt.UTC().Format(time.RFC3339), d.ID); err != nil {
			t.Fatalf("set history timestamps %q: %v", question, err)
		}
	}
	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision %q: %v", question, err)
	}
	return got
}
