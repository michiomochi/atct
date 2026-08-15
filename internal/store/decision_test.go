package store

import (
	"context"
	"errors"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestAskDecisionStartsOpen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d, err := s.AskDecision(ctx, AskInput{
		GoalID:   goalID,
		Kind:     domain.KindDecision,
		Question: "Should retries use exponential backoff?",
		Options: []domain.Option{
			{Label: "backoff", Description: "Exponential backoff", Consequence: "Simpler implementation but duplicate execution is possible"},
			{Label: "idempotency", Description: "Add an idempotency key", Consequence: "Prevents duplicates but requires a schema change"},
		},
		RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if d.Status != domain.DecisionOpen {
		t.Fatalf("status = %q, want %q", d.Status, domain.DecisionOpen)
	}
	if len(d.Options) != 2 || d.Options[1].Consequence == "" {
		t.Fatalf("options not round-tripped: %+v", d.Options)
	}
}

func TestUpdateTaskRejectsDoneWhileDecisionOpen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	tasks, err := s.DeclareTasks(ctx, goalID, "codex", "k", []string{"Implement the task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: tasks[0].ID, Kind: domain.KindDecision,
		Question: "What should we do?", RunID: "run-1",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	_, err = s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone)
	if !errors.Is(err, ErrTaskHasOpenDecision) {
		t.Fatalf("err = %v, want ErrTaskHasOpenDecision", err)
	}

	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing); err != nil {
		t.Fatalf("UpdateTask to doing should succeed: %v", err)
	}
}
