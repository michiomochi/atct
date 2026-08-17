package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

func TestParseArgsAcceptsContext(t *testing.T) {
	cfg, err := parseArgs([]string{"context"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "context" {
		t.Fatalf("subcommand = %q, want context", cfg.subcommand)
	}
}

func TestRenderContextOmitsInactiveGoals(t *testing.T) {
	got := renderContext([]contextGoal{
		{Goal: domain.Goal{ID: "done-goal", Title: "Finished", Status: domain.GoalDone}},
	}, nil)
	if got != "" {
		t.Fatalf("renderContext = %q, want empty output", got)
	}
}

func TestRenderContextIncludesGoalDetails(t *testing.T) {
	got := renderContext([]contextGoal{{
		Goal: domain.Goal{
			ID:          "goal-123",
			Title:       "Ship context",
			Description: "Expose the current ATCT state to the next session.",
			Status:      domain.GoalActive,
		},
	}}, nil)
	for _, want := range []string{
		"Goal: Ship context",
		"Description: Expose the current ATCT state to the next session.",
		"goal_id: goal-123",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context does not contain %q: %q", want, got)
		}
	}
}

func TestRenderContextIncludesActionableTasksAndIDs(t *testing.T) {
	got := renderContext([]contextGoal{{
		Goal: domain.Goal{ID: "goal-123", Title: "Ship context", Status: domain.GoalActive},
		Tasks: []domain.Task{
			{ID: "task-todo", Title: "Declare tests", Status: domain.TaskTodo},
			{ID: "task-doing", Title: "Implement command", Status: domain.TaskDoing},
			{ID: "task-done", Title: "Review design", Status: domain.TaskDone},
		},
	}}, nil)
	for _, want := range []string{
		"[todo] Declare tests (task_id: task-todo)",
		"[doing] Implement command (task_id: task-doing)",
		"atct_task_claim",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context does not contain %q: %q", want, got)
		}
	}
	if strings.Contains(got, "task-done") || strings.Contains(got, "Review design") {
		t.Fatalf("done task should not be listed: %q", got)
	}
	manyTasks := contextGoal{Goal: domain.Goal{ID: "goal-many", Title: "Many tasks", Status: domain.GoalActive}}
	for i := 0; i < 6; i++ {
		manyTasks.Tasks = append(manyTasks.Tasks, domain.Task{
			ID: "task-many-" + string(rune('a'+i)), Title: "Task", Status: domain.TaskTodo,
		})
	}
	manyOutput := renderContext([]contextGoal{manyTasks}, nil)
	if lines := len(strings.Split(strings.TrimSuffix(manyOutput, "\n"), "\n")); lines > 30 {
		t.Fatalf("context has %d lines, want at most 30: %q", lines, manyOutput)
	}
}

func TestRenderContextIncludesUnappliedDecisionsAndPollTool(t *testing.T) {
	got := renderContext([]contextGoal{{
		Goal: domain.Goal{ID: "goal-123", Title: "Ship context", Status: domain.GoalActive},
	}}, []domain.Decision{
		{
			ID: "decision-456", GoalID: "goal-123", Question: "Which output format should be used?",
			AnswerLabel: "compact", AnswerText: "Use compact lines.", Status: domain.DecisionAnswered,
			AnsweredAt: ptrTime(time.Unix(1, 0)),
		},
		{ID: "decision-applied", GoalID: "goal-123", Question: "Already handled", Status: domain.DecisionApplied},
	})
	for _, want := range []string{
		"decision_id: decision-456",
		"Which output format should be used?",
		"compact",
		"atct_decision_poll",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context does not contain %q: %q", want, got)
		}
	}
	if strings.Contains(got, "decision-applied") || strings.Contains(got, "Already handled") {
		t.Fatalf("applied decision should not be listed: %q", got)
	}

	noTasks := renderContext([]contextGoal{{
		Goal: domain.Goal{ID: "goal-empty", Title: "No tasks", Status: domain.GoalActive},
	}}, nil)
	if !strings.Contains(noTasks, "atct_task_declare") || strings.Contains(noTasks, "atct_task_claim") {
		t.Fatalf("no-task state has wrong next tool: %q", noTasks)
	}
	withTodo := renderContext([]contextGoal{{
		Goal:  domain.Goal{ID: "goal-todo", Title: "Todo", Status: domain.GoalActive},
		Tasks: []domain.Task{{ID: "task-todo", Title: "A task", Status: domain.TaskTodo}},
	}}, nil)
	if !strings.Contains(withTodo, "atct_task_claim") || strings.Contains(withTodo, "atct_task_declare") {
		t.Fatalf("todo state has wrong next tool: %q", withTodo)
	}
}

func TestContextIsSilentForUnregisteredProject(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	got, err := contextText(dir, t.TempDir())
	if err != nil {
		t.Fatalf("contextText: %v", err)
	}
	if got != "" {
		t.Fatalf("contextText = %q, want empty output", got)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
