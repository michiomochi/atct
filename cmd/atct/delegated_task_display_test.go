package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// A delegated task keeps its todo status because neither atct_handoff_request
// nor atct_task_claim writes tasks.status; the open handoff is the only record
// that someone owns the work. atct wakeup already reads that record, so these
// tests pin the two surfaces a human reads directly to the same rule.

// The brief is the count a human reads to decide whether anything is idle, so a
// task whose work is owned must not be counted as todo.
func TestContextBriefExcludesHandoffOwnedTasksFromTodoCount(t *testing.T) {
	fixture := newContextCheckFixture(t)
	ctx := context.Background()

	fixture.addTask(t, "owned")
	fixture.addTask(t, "idle")

	tasks, err := fixture.db.ListTasks(ctx, fixture.goal.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("declared %d tasks, want 2", len(tasks))
	}
	owner, err := fixture.db.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := fixture.db.ClaimTask(ctx, tasks[0].ID, owner); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	reloaded, err := fixture.db.ListTasks(ctx, fixture.goal.ID)
	if err != nil {
		t.Fatalf("ListTasks after claim: %v", err)
	}
	if reloaded[0].Status != domain.TaskTodo {
		t.Fatalf("owned task status = %s, want todo; this test no longer covers the reported case", reloaded[0].Status)
	}

	got, err := contextBriefTextForProject(fixture.dir, fixture.cwd, "", false)
	if err != nil {
		t.Fatalf("contextBriefTextForProject: %v", err)
	}
	if !strings.Contains(got, "todo tasks 1") {
		t.Fatalf("brief counted the handoff-owned task %d as todo: %q", tasks[0].ID, got)
	}
}

// Next tools tells the reader what to do next. Suggesting atct_task_claim when
// every remaining task already has an owner sends the reader at work that is
// not theirs to take.
func TestRenderContextOmitsClaimToolWhenEveryTodoTaskIsOwned(t *testing.T) {
	got := renderContextForAgentSession([]contextGoal{{
		Goal: domain.Goal{ID: 42, Content: "Owned goal", Status: domain.GoalActive},
		Tasks: []domain.Task{
			{ID: 7, Title: "Owned", Status: domain.TaskTodo},
		},
		TaskHandoffs: map[int64]*store.TaskHandoff{
			7: {ReceivedBy: 99},
		},
	}}, nil, 1)

	if strings.Contains(got, "atct_task_claim") {
		t.Fatalf("context offered atct_task_claim for an owned task:\n%s", got)
	}
}
