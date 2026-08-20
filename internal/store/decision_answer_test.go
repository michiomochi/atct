package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestAnswerDecisionOnlyOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "answer")

	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", AgentSessionID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.AnswerDecision(ctx, AnswerInput{
				DecisionID: d.ID, AnswerText: "A",
			})
		}(i)
	}
	wg.Wait()

	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrDecisionNotOpen) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("%d answers succeeded, want exactly 1", success)
	}

	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered {
		t.Fatalf("status = %q, want %q", got.Status, domain.DecisionAnswered)
	}
	if got.AnsweredAt == nil {
		t.Fatal("answered_at is nil")
	}
}

func TestWithdrawDecisionRemovesFromInbox(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "withdraw")

	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", AgentSessionID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if err := s.WithdrawDecision(ctx, d.ID, "Resolved independently"); err != nil {
		t.Fatalf("WithdrawDecision: %v", err)
	}

	open, err := s.ListOpenDecisions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("%d open decisions remain, want 0", len(open))
	}
}
