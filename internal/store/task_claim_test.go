package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestClaimTaskAllowsExactlyOneConcurrentWinner(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "codex", "claim-key", []string{"Implement the task"}, []string{"Implement the task and confirm exactly one run can claim it."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	winners := make(chan domain.Task, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := s.ClaimTask(ctx, tasks[0].ID, fmt.Sprintf("run-%d", i))
			results <- err
			if err == nil {
				winners <- task
			}
		}()
	}
	wg.Wait()
	close(results)
	close(winners)

	var winnerCount int
	for err := range results {
		if err == nil {
			winnerCount++
			continue
		}
		if !errors.Is(err, ErrTaskAlreadyClaimed) {
			t.Fatalf("ClaimTask: %v", err)
		}
	}
	if winnerCount != 1 || len(winners) != 1 {
		t.Fatalf("concurrent claim winners = %d, want 1", winnerCount)
	}
	winner := <-winners
	if winner.ClaimedBy == "" || winner.ClaimedAt == nil {
		t.Fatalf("winner has no claim metadata: %+v", winner)
	}
}

func TestUpdateTaskReleasesClaimWhenTerminal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "codex", "release-key", []string{"Finish the task"}, []string{"Finish the task and ensure terminal status releases its claim."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-1"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	updated, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, "run-1")
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.ClaimedBy != "" || updated.ClaimedAt != nil {
		t.Fatalf("terminal task retained claim: %+v", updated)
	}
}

func TestUpdateTaskReleasesClaimWhenTodo(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "codex", "todo-release-key", []string{"Resume the task later"}, []string{"Resume the task later and verify todo status releases its claim."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, "run-1"); err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-1"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	updated, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskTodo, "run-1")
	if err != nil {
		t.Fatalf("UpdateTask todo: %v", err)
	}
	if updated.ClaimedBy != "" || updated.ClaimedAt != nil {
		t.Fatalf("todo task retained claim: %+v", updated)
	}
}

func TestUpdateTaskKeepsClaimWhenDoing(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "codex", "doing-keep-key", []string{"Keep working"}, []string{"Keep working on the task while preserving its active claim."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	claimed, err := s.ClaimTask(ctx, tasks[0].ID, "run-1")
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	updated, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, "run-1")
	if err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}
	if updated.ClaimedBy != claimed.ClaimedBy {
		t.Fatalf("claimed_by changed on doing: %q -> %q", claimed.ClaimedBy, updated.ClaimedBy)
	}
	if updated.ClaimedAt == nil {
		t.Fatalf("claimed_at cleared on doing: %+v", updated)
	}
}

func TestReleaseTaskClearsClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "codex", "human-release-key", []string{"Release a stale claim"}, []string{"Release the stale claim so another run can continue the task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "dead-run"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	released, err := s.ReleaseTask(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}
	if released.ClaimedBy != "" || released.ClaimedAt != nil {
		t.Fatalf("released task retained claim: %+v", released)
	}
}
