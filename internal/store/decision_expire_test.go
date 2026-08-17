package store

import (
	"context"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func newExpiringDecision(t *testing.T, s *Store, afterMs int64) domain.Decision {
	t.Helper()

	defaultAfterMs := afterMs
	d, err := s.AskDecision(context.Background(), AskInput{
		GoalID:         newTestGoal(t, s),
		Kind:           domain.KindDecision,
		Question:       "Choose an option",
		Options:        []domain.Option{{Label: "A"}, {Label: "B"}},
		DefaultOption:  "A",
		DefaultAfterMs: &defaultAfterMs,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	return d
}

func TestApplyExpiredDefaultsFiresAfterDeadline(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	d := newExpiringDecision(t, s, 1000)

	count, err := s.ApplyExpiredDefaults(ctx, d.CreatedAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ApplyExpiredDefaults: %v", err)
	}
	if count != 1 {
		t.Fatalf("ApplyExpiredDefaults returned %d, want 1", count)
	}

	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered {
		t.Fatalf("status = %q, want %q", got.Status, domain.DecisionAnswered)
	}
	if got.AnswerLabel != "A" {
		t.Fatalf("answer label = %q, want %q", got.AnswerLabel, "A")
	}
	if got.DefaultAppliedAt == nil {
		t.Fatal("default_applied_at is nil")
	}
	if got.AnsweredAt == nil {
		t.Fatal("answered_at is nil")
	}
}

func TestApplyExpiredDefaultsSkipsAnsweredDecision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	d := newExpiringDecision(t, s, 1000)

	if _, err := s.AnswerDecision(ctx, AnswerInput{
		DecisionID:  d.ID,
		AnswerLabel: "B",
		AnsweredBy:  "human",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}

	count, err := s.ApplyExpiredDefaults(ctx, d.CreatedAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ApplyExpiredDefaults: %v", err)
	}
	if count != 0 {
		t.Fatalf("ApplyExpiredDefaults returned %d, want 0", count)
	}

	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered {
		t.Fatalf("status = %q, want %q", got.Status, domain.DecisionAnswered)
	}
	if got.AnswerLabel != "B" {
		t.Fatalf("answer label = %q, want %q", got.AnswerLabel, "B")
	}
	if got.DefaultAppliedAt != nil {
		t.Fatalf("default_applied_at = %v, want nil", got.DefaultAppliedAt)
	}
}

func TestApplyExpiredDefaultsIgnoresDecisionWithoutDefault(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	d, err := s.AskDecision(ctx, AskInput{
		GoalID:   newTestGoal(t, s),
		Kind:     domain.KindDecision,
		Question: "Choose an option",
		Options:  []domain.Option{{Label: "A"}, {Label: "B"}},
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	count, err := s.ApplyExpiredDefaults(ctx, d.CreatedAt.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ApplyExpiredDefaults: %v", err)
	}
	if count != 0 {
		t.Fatalf("ApplyExpiredDefaults returned %d, want 0", count)
	}

	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionOpen {
		t.Fatalf("status = %q, want %q", got.Status, domain.DecisionOpen)
	}
}
