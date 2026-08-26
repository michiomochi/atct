package store

import (
	"context"
	"errors"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestWithdrawActiveGoalRequiresReason(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	before, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal before withdrawal: %v", err)
	}

	err = s.WithdrawActiveGoal(ctx, goalID, " \t\n")
	if err == nil {
		t.Fatal("WithdrawActiveGoal with blank reason succeeded")
	}

	after, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal after withdrawal: %v", err)
	}
	if after.Status != before.Status || after.ResultSummary != before.ResultSummary || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("goal changed after blank-reason withdrawal: before=%+v after=%+v", before, after)
	}
}

func TestWithdrawActiveGoalRejectsProposedGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "proposed", "/repos/proposed")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "proposed goal", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	before, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal before withdrawal: %v", err)
	}

	err = s.WithdrawActiveGoal(ctx, goal.ID, "no longer needed")
	if !errors.Is(err, ErrGoalNotActive) {
		t.Fatalf("err = %v, want ErrGoalNotActive", err)
	}

	after, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal after withdrawal: %v", err)
	}
	if after.Status != before.Status || after.ResultSummary != before.ResultSummary || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("proposed goal changed: before=%+v after=%+v", before, after)
	}
}

func TestWithdrawActiveGoalKeepsDoneTasks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "withdraw-done", []string{"done", "open"}, []string{"Already done.", "Still open."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask done: %v", err)
	}

	if err := s.WithdrawActiveGoal(ctx, goalID, "stopping this goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	got, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, task := range got {
		if task.ID == tasks[0].ID && task.Status != domain.TaskDone {
			t.Fatalf("done task status = %q, want %q", task.Status, domain.TaskDone)
		}
	}
}

func TestWithdrawActiveGoalDropsOpenTasksAndReleasesClaims(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "withdraw-open", []string{"todo", "doing"}, []string{"Todo work.", "Doing work."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	addTestAgentSession(t, s, "agent-run")
	for _, task := range tasks {
		if _, err := s.ClaimTask(ctx, task.ID, testSessionID("agent-run")); err != nil {
			t.Fatalf("ClaimTask(%d): %v", task.ID, err)
		}
	}
	if _, err := s.UpdateTask(ctx, tasks[1].ID, domain.TaskDoing, 0); err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}

	if err := s.WithdrawActiveGoal(ctx, goalID, "stopping this goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	got, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, want := range tasks {
		var found domain.Task
		for _, task := range got {
			if task.ID == want.ID {
				found = task
				break
			}
		}
		if found.ID == 0 {
			t.Fatalf("task %d missing after withdrawal", want.ID)
		}
		if found.Status != domain.TaskDropped {
			t.Errorf("task %d status = %q, want %q", found.ID, found.Status, domain.TaskDropped)
		}
		handoff, err := s.openTaskHandoff(ctx, found.ID)
		if err != nil {
			t.Errorf("task %d openTaskHandoff: %v", found.ID, err)
		} else if handoff != nil {
			t.Errorf("task %d handoff = %#v, want released", found.ID, handoff)
		}
	}
}

func TestWithdrawActiveGoalWithdrawsOpenDecisions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	decision, err := s.AskDecision(ctx, AskInput{
		GoalID:   goalID,
		Kind:     domain.DecisionKind("question"),
		Question: "Should this goal continue?",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	if err := s.WithdrawActiveGoal(ctx, goalID, "stopping this goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	open, err := s.ListOpenDecisions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open decisions = %+v, want none", open)
	}
	got, err := s.GetDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionWithdrawn {
		t.Fatalf("decision status = %q, want %q", got.Status, domain.DecisionWithdrawn)
	}
	if got.AnswerText != "stopping this goal" {
		t.Fatalf("decision answer_text = %q, want withdrawal reason", got.AnswerText)
	}
}
