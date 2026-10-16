package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

// TestGoalNextActionsMatchPerGoalDerivation checks the batch next actions and the
// batch awaiting-review set against the per-goal reads, over goals in several
// states: a goal review waiting, an open task handoff, an open decision, a plain
// active goal, a done goal, and a dropped goal.
func TestGoalNextActionsMatchPerGoalDerivation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	reviewGoal, holderID := newTaskRecoveryGoal(t, s, "nb-review")
	reviewed, err := s.GetGoal(ctx, reviewGoal)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, "nb-review-goal-handoff", reviewGoal, holderID, "ready for review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}

	taskGoal, err := s.CreateGoal(ctx, reviewed.ProjectID, "Goal with an open task handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal task: %v", err)
	}
	_ = taskGoal
	tasks, err := s.CreateTasks(ctx, reviewGoal, "worker", "nb-task", []string{"open", "todo"}, []string{"desc open", "desc todo"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	if _, err := s.RequestTaskHandoff(ctx, "nb-task-open", tasks[0].ID, holderID, "open"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}

	decisionGoal, err := s.CreateGoal(ctx, reviewed.ProjectID, "Goal with an open decision", "human")
	if err != nil {
		t.Fatalf("CreateGoal decision: %v", err)
	}
	if _, err := s.AskDecision(ctx, AskInput{GoalID: decisionGoal.ID, Kind: domain.DecisionKind("question"), Question: "Which?"}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	plainGoal, err := s.CreateGoal(ctx, reviewed.ProjectID, "Plain active goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal plain: %v", err)
	}
	doneGoal, err := s.CreateGoal(ctx, reviewed.ProjectID, "Done goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal done: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE goals SET status = 'done', work_done = 'w', now_possible = 'n', how_to_verify = 'v', surprises = 's', needs_review = 'r' WHERE id = ?`, doneGoal.ID); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	droppedGoal, err := s.CreateGoal(ctx, reviewed.ProjectID, "Dropped goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal dropped: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE goals SET status = 'dropped' WHERE id = ?`, droppedGoal.ID); err != nil {
		t.Fatalf("mark dropped: %v", err)
	}

	goals, err := s.ListAllGoals(ctx)
	if err != nil {
		t.Fatalf("ListAllGoals: %v", err)
	}
	batch, err := s.GoalNextActions(ctx, goals)
	if err != nil {
		t.Fatalf("GoalNextActions: %v", err)
	}
	if len(batch) != len(goals) {
		t.Fatalf("GoalNextActions returned %d goals, want %d", len(batch), len(goals))
	}
	for _, goal := range goals {
		want, err := s.GoalNextAction(ctx, goal)
		if err != nil {
			t.Fatalf("GoalNextAction %d: %v", goal.ID, err)
		}
		if !reflect.DeepEqual(batch[goal.ID], want) {
			t.Fatalf("goal %d next action differs:\nbatch:    %+v\nper-goal: %+v", goal.ID, batch[goal.ID], want)
		}
	}

	awaiting, err := s.GoalsAwaitingHandoffReview(ctx)
	if err != nil {
		t.Fatalf("GoalsAwaitingHandoffReview: %v", err)
	}
	if !awaiting[reviewGoal] {
		t.Fatalf("goal %d with a requested goal handoff review is not awaiting review: %v", reviewGoal, awaiting)
	}
	if len(awaiting) != 1 {
		t.Fatalf("awaiting review = %v, want only goal %d", awaiting, reviewGoal)
	}
	_ = plainGoal
}
