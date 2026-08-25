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
	taskID := newTestDecisionTask(t, s, goalID, "starts-open")

	d, err := s.AskDecision(ctx, AskInput{
		GoalID:   goalID,
		TaskID:   taskID,
		Kind:     domain.KindDecision,
		Question: "Should retries use exponential backoff?",
		Options: []domain.Option{
			{Label: "backoff", Description: "Exponential backoff", Consequence: "Simpler implementation but duplicate execution is possible"},
			{Label: "idempotency", Description: "Add an idempotency key", Consequence: "Prevents duplicates but requires a schema change"},
		},
		AgentSessionID: "run-1",
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

	tasks, err := s.DeclareTasks(ctx, goalID, "codex", "k", []string{"Implement the task"}, []string{"Implement the task and verify its behavior through the decision flow."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: tasks[0].ID, Kind: domain.KindDecision,
		Question: "What should we do?", AgentSessionID: "run-1",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	_, err = s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, "")
	if !errors.Is(err, ErrTaskHasOpenDecision) {
		t.Fatalf("err = %v, want ErrTaskHasOpenDecision", err)
	}

	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, ""); err != nil {
		t.Fatalf("UpdateTask to doing should succeed: %v", err)
	}
}
func TestListUnappliedDecisionsForGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "unapplied-decisions", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goalARecord, err := s.CreateGoal(ctx, project.ID, "Goal A", "test")
	if err != nil {
		t.Fatalf("CreateGoal A: %v", err)
	}
	goalBRecord, err := s.CreateGoal(ctx, project.ID, "Goal B", "test")
	if err != nil {
		t.Fatalf("CreateGoal B: %v", err)
	}
	goalA := goalARecord.ID
	goalB := goalBRecord.ID
	approveTestGoal(t, s, ctx, goalA)
	approveTestGoal(t, s, ctx, goalB)

	for _, tc := range []struct {
		goalID string
		label  string
	}{
		{goalID: goalA, label: "A"},
		{goalID: goalB, label: "B"},
	} {
		taskID := newTestDecisionTask(t, s, tc.goalID, "unapplied-"+tc.label)
		d, err := s.AskDecision(ctx, AskInput{
			GoalID: tc.goalID, TaskID: taskID, Kind: domain.KindDecision,
			Question: "What should we do for " + tc.label + "?", AgentSessionID: "run-" + tc.label,
		})
		if err != nil {
			t.Fatalf("AskDecision(%s): %v", tc.label, err)
		}
		if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: d.ID, AnswerText: "answer-" + tc.label}); err != nil {
			t.Fatalf("AnswerDecision(%s): %v", tc.label, err)
		}
	}

	got, err := s.ListUnappliedDecisionsForGoal(ctx, goalA)
	if err != nil {
		t.Fatalf("ListUnappliedDecisionsForGoal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d decisions for goal A, want 1", len(got))
	}
	for _, d := range got {
		if d.GoalID != goalA {
			t.Fatalf("got decision for goal %q while querying goal %q", d.GoalID, goalA)
		}
	}
}

func TestListUnappliedDecisionsForProject(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "project-unapplied-decisions", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goalARecord, err := s.CreateGoal(ctx, project.ID, "Goal A", "test")
	if err != nil {
		t.Fatalf("CreateGoal A: %v", err)
	}
	goalBRecord, err := s.CreateGoal(ctx, project.ID, "Goal B", "test")
	if err != nil {
		t.Fatalf("CreateGoal B: %v", err)
	}
	goalA := goalARecord.ID
	goalB := goalBRecord.ID
	approveTestGoal(t, s, ctx, goalA)
	approveTestGoal(t, s, ctx, goalB)

	for _, tc := range []struct {
		goalID string
		label  string
	}{
		{goalID: goalA, label: "A"},
		{goalID: goalB, label: "B"},
	} {
		taskID := newTestDecisionTask(t, s, tc.goalID, "project-unapplied-"+tc.label)
		d, err := s.AskDecision(ctx, AskInput{
			GoalID: tc.goalID, TaskID: taskID, Kind: domain.KindDecision,
			Question: "What should we do for " + tc.label + "?", AgentSessionID: "project-run-" + tc.label,
		})
		if err != nil {
			t.Fatalf("AskDecision(%s): %v", tc.label, err)
		}
		if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: d.ID, AnswerText: "project-answer-" + tc.label}); err != nil {
			t.Fatalf("AnswerDecision(%s): %v", tc.label, err)
		}
	}

	got, err := s.ListUnappliedDecisionsForProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListUnappliedDecisionsForProject: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d decisions for project, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, d := range got {
		seen[d.GoalID] = true
	}
	if !seen[goalA] || !seen[goalB] {
		t.Fatalf("got decisions for goals A/B = %v, want both %q and %q", seen, goalA, goalB)
	}
}

func approveTestGoal(t *testing.T, s *Store, ctx context.Context, goalID string) {
	t.Helper()
	approval, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindGoalApproval, Question: "Approve goal?",
		Options: []domain.Option{{Label: "approve"}}, AgentSessionID: "approval-" + goalID,
	})
	if err != nil {
		t.Fatalf("AskDecision goal approval: %v", err)
	}
	if _, err := s.ApproveGoal(ctx, approval.ID); err != nil {
		t.Fatalf("ApproveGoal: %v", err)
	}
}
