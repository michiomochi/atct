package store

import (
	"context"
	"testing"
)

func newTestGoal(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()
	ns, err := s.CreateNamespace(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}
	g, err := s.CreateGoal(ctx, ns.ID, "goal", "")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	return g.ID
}

func TestDeclareTasksIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	titles := []string{"Design", "Implement", "Test"}

	first, err := s.DeclareTasks(ctx, goalID, "codex", "key-1", titles)
	if err != nil {
		t.Fatalf("first DeclareTasks: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("first returned %d tasks, want 3", len(first))
	}

	second, err := s.DeclareTasks(ctx, goalID, "codex", "key-1", titles)
	if err != nil {
		t.Fatalf("second DeclareTasks: %v", err)
	}
	if len(second) != 3 {
		t.Fatalf("second returned %d tasks, want 3", len(second))
	}

	all, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("stored %d tasks after duplicate declare, want 3", len(all))
	}
	if all[0].ID != first[0].ID {
		t.Fatalf("task id changed on re-declare: %s -> %s", first[0].ID, all[0].ID)
	}
}
