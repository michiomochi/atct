package store

import (
	"context"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func TestWaitForAnswerReturnsWhenAnswered(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "notify-answer")

	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	answerErr := make(chan error, 1)
	go func() {
		_, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: d.ID, AnswerLabel: "A"})
		answerErr <- err
	}()

	got, ok, err := s.WaitForAnswer(ctx, d.ID, 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForAnswer: %v", err)
	}
	if answerErr := <-answerErr; answerErr != nil {
		t.Fatalf("AnswerDecision: %v", answerErr)
	}
	if !ok {
		t.Fatal("ok = false, want true (answer arrived within timeout)")
	}
	if got.AnswerLabel != "A" {
		t.Fatalf("answer_label = %q, want %q", got.AnswerLabel, "A")
	}
}

func TestWaitForAnswerParksOnTimeout(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "notify-timeout")

	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	_, ok, err := s.WaitForAnswer(ctx, d.ID, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForAnswer: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false (should park on timeout)")
	}
}
