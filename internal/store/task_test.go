package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newTestGoal(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	g, err := s.CreateGoal(ctx, ns.ID, "goal", "")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	return g.ID
}

func TestDeclareTasksRejectsEmptyDescription(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	_, err := s.DeclareTasks(context.Background(), goalID, "codex", "empty-description", []string{"Implement the task"}, []string{""})
	if err == nil || !strings.HasPrefix(err.Error(), "declare tasks:") {
		t.Fatalf("DeclareTasks error = %v, want declare tasks error for empty description", err)
	}
}

func TestDeclareTasksRejectsWhitespaceOnlyDescription(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	_, err := s.DeclareTasks(context.Background(), goalID, "codex", "whitespace-description", []string{"Implement the task"}, []string{" \t\n"})
	if err == nil || !strings.HasPrefix(err.Error(), "declare tasks:") {
		t.Fatalf("DeclareTasks error = %v, want declare tasks error for whitespace-only description", err)
	}
}

func TestDeclareTasksRejectsDescriptionTitleLengthMismatch(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	_, err := s.DeclareTasks(context.Background(), goalID, "codex", "description-length", []string{"Design the change", "Implement the change"}, []string{"Describe the schema change"})
	if err == nil || !strings.HasPrefix(err.Error(), "declare tasks:") {
		t.Fatalf("DeclareTasks error = %v, want declare tasks error for mismatched descriptions", err)
	}
}

func TestDeclareTasksKeepsOriginalDescriptionOnRedeclare(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	titles := []string{"Add the task column", "Validate task declarations"}
	original := []string{
		"Persist the task explanation alongside its title.",
		"Reject declarations that do not explain completion and assumptions.",
	}
	changed := []string{
		"This replacement explanation must not overwrite the stored value.",
		"A second declaration keeps the first explanation for each task.",
	}

	first, err := s.DeclareTasks(context.Background(), goalID, "codex", "description-idempotency", titles, original)
	if err != nil {
		t.Fatalf("first DeclareTasks: %v", err)
	}
	second, err := s.DeclareTasks(context.Background(), goalID, "codex", "description-idempotency", titles, changed)
	if err != nil {
		t.Fatalf("second DeclareTasks: %v", err)
	}
	if len(first) != len(original) || len(second) != len(original) {
		t.Fatalf("DeclareTasks returned %d and %d tasks, want %d", len(first), len(second), len(original))
	}
	for i, want := range original {
		if second[i].Description != want {
			t.Fatalf("re-declared task %d description = %q, want original %q", i, second[i].Description, want)
		}
	}
}

func TestDeclareTasksPersistsDescriptions(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	titles := []string{"Add the migration", "Read descriptions from the store"}
	want := []string{
		"Add a non-null default so existing task rows remain valid.",
		"Return each stored explanation when tasks are listed.",
	}

	if _, err := s.DeclareTasks(context.Background(), goalID, "codex", "description-persistence", titles, want); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	tasks, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != len(want) {
		t.Fatalf("ListTasks returned %d tasks, want %d", len(tasks), len(want))
	}
	for i, description := range want {
		if tasks[i].Description != description {
			t.Fatalf("task %d description = %q, want %q", i, tasks[i].Description, description)
		}
	}
}

func TestDeclareTasksIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	titles := []string{"Design", "Implement", "Test"}
	descriptions := []string{
		"Define the implementation boundaries and data flow.",
		"Implement the requested change in the Go store.",
		"Verify the behavior with focused and full tests.",
	}

	first, err := s.DeclareTasks(ctx, goalID, "codex", "key-1", titles, descriptions)
	if err != nil {
		t.Fatalf("first DeclareTasks: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("first returned %d tasks, want 3", len(first))
	}

	second, err := s.DeclareTasks(ctx, goalID, "codex", "key-1", titles, descriptions)
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

func declareOneTaskWithFiles(t *testing.T, s *Store, goalID, key, title string, files []string) string {
	t.Helper()
	description := "Complete the task titled " + title + " and verify its declared files."
	tasks, err := s.DeclareTasks(context.Background(), goalID, "codex", key, []string{title}, []string{description}, [][]string{files})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	for _, task := range tasks {
		if task.DeclareKey == key+"#0" {
			return task.ID
		}
	}
	t.Fatalf("DeclareTasks did not return task for key %q: %+v", key, tasks)
	return ""
}

func TestDeclareTasksPersistsFiles(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	want := []string{"internal/store/task.go", "internal/domain/model.go"}
	taskID := declareOneTaskWithFiles(t, s, goalID, "files-1", "declare files", want)

	tasks, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("ListTasks returned %+v, want task %s", tasks, taskID)
	}
	if !reflect.DeepEqual(tasks[0].Files, want) {
		t.Fatalf("task files = %#v, want %#v", tasks[0].Files, want)
	}
}

func TestClaimTaskRejectsOverlappingFilesAcrossRuns(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "conflict-1", "first", []string{"internal/store/task.go"})
	secondID := declareOneTaskWithFiles(t, s, goalID, "conflict-2", "second", []string{"internal/store/task.go"})

	if _, err := s.ClaimTask(context.Background(), firstID, "run-1"); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, "run-2"); !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("second ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
}

func TestClaimTaskAllowsNonOverlappingFilesAcrossRuns(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "non-overlap-1", "first", []string{"internal/store/task.go"})
	secondID := declareOneTaskWithFiles(t, s, goalID, "non-overlap-2", "second", []string{"internal/store/schema.go"})

	if _, err := s.ClaimTask(context.Background(), firstID, "run-1"); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, "run-2"); err != nil {
		t.Fatalf("second ClaimTask: %v", err)
	}
}

func TestClaimTaskAllowsOverlappingFilesForSameRun(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "same-run-1", "first", []string{"internal/store/task.go"})
	secondID := declareOneTaskWithFiles(t, s, goalID, "same-run-2", "second", []string{"internal/store/task.go"})

	if _, err := s.ClaimTask(context.Background(), firstID, "run-1"); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, "run-1"); err != nil {
		t.Fatalf("same-run ClaimTask: %v", err)
	}
}

func TestClaimTaskIgnoresUndeclaredFiles(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "empty-files-1", "first", nil)
	secondID := declareOneTaskWithFiles(t, s, goalID, "empty-files-2", "second", []string{"internal/store/task.go"})

	if _, err := s.ClaimTask(context.Background(), firstID, "run-1"); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, "run-2"); err != nil {
		t.Fatalf("second ClaimTask: %v", err)
	}
}

func TestClaimTaskIgnoresTerminalClaims(t *testing.T) {
	for _, status := range []string{"done", "dropped"} {
		t.Run(status, func(t *testing.T) {
			s := newTestStore(t)
			goalID := newTestGoal(t, s)
			ownerID := declareOneTaskWithFiles(t, s, goalID, "terminal-owner-"+status, "owner", []string{"internal/store/task.go"})
			candidateID := declareOneTaskWithFiles(t, s, goalID, "terminal-candidate-"+status, "candidate", []string{"internal/store/task.go"})

			if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
				t.Fatalf("owner ClaimTask: %v", err)
			}
			if _, err := s.DB().Exec(`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UTC().Format(time.RFC3339Nano), ownerID); err != nil {
				t.Fatalf("mark owner %s: %v", status, err)
			}
			if _, err := s.ClaimTask(context.Background(), candidateID, "run-candidate"); err != nil {
				t.Fatalf("candidate ClaimTask: %v", err)
			}
		})
	}
}

func TestClaimTaskConflictErrorNamesTaskAndFile(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	ownerID := declareOneTaskWithFiles(t, s, goalID, "error-owner", "owner task", []string{"internal/store/task.go"})
	candidateID := declareOneTaskWithFiles(t, s, goalID, "error-candidate", "candidate task", []string{"internal/store/task.go"})

	if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), candidateID, "run-candidate")
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{ownerID, "owner task", "internal/store/task.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClaimTask error %q does not contain %q", err, want)
		}
	}
}

func TestClaimTaskConflictErrorReturnsClaimableCandidates(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	ownerID := declareOneTaskWithFiles(t, s, goalID, "candidate-owner", "owner task", []string{"internal/store/task.go"})
	blockedID := declareOneTaskWithFiles(t, s, goalID, "candidate-blocked", "blocked alternative", []string{"internal/store/task.go"})
	alternativeID := declareOneTaskWithFiles(t, s, goalID, "candidate-safe", "safe alternative", []string{"internal/store/schema.go"})
	targetID := declareOneTaskWithFiles(t, s, goalID, "candidate-target", "target task", []string{"internal/store/task.go"})

	if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, "run-target")
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{alternativeID, "safe alternative", "alternatives"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClaimTask error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), blockedID) {
		t.Fatalf("ClaimTask error %q includes a conflicting alternative %s", err, blockedID)
	}
}

func TestClaimTaskConflictCandidatesAreClaimable(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	ownerID := declareOneTaskWithFiles(t, s, goalID, "claimable-owner", "owner task", []string{"internal/store/task.go"})
	targetID := declareOneTaskWithFiles(t, s, goalID, "claimable-target", "target task", []string{"internal/store/task.go"})
	fileAlternativeID := declareOneTaskWithFiles(t, s, goalID, "claimable-file", "file alternative", []string{"internal/store/schema.go"})
	emptyAlternativeID := declareOneTaskWithFiles(t, s, goalID, "claimable-empty", "empty alternative", nil)

	if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, "run-target")
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{fileAlternativeID, "file alternative", emptyAlternativeID, "empty alternative"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClaimTask error %q does not contain candidate %q", err, want)
		}
	}
	for _, taskID := range []string{fileAlternativeID, emptyAlternativeID} {
		if _, err := s.ClaimTask(context.Background(), taskID, "run-target"); err != nil {
			t.Fatalf("candidate %s ClaimTask: %v", taskID, err)
		}
	}
}

func TestClaimTaskConflictErrorReportsNoCandidates(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	ownerID := declareOneTaskWithFiles(t, s, goalID, "no-candidate-owner", "owner task", []string{"internal/store/task.go"})
	targetID := declareOneTaskWithFiles(t, s, goalID, "no-candidate-target", "target task", []string{"internal/store/task.go"})

	if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, "run-target")
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	if !strings.Contains(err.Error(), "alternatives: []") {
		t.Fatalf("ClaimTask error %q does not report that no alternatives are available", err)
	}
}

func TestOpenMigratesTasksFilesColumnWithoutLosingData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	oldDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open old DB: %v", err)
	}
	oldSchema := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, root_path TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL)`,
		`CREATE TABLE goals (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, result_summary TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE tasks (id TEXT PRIMARY KEY, goal_id TEXT NOT NULL REFERENCES goals(id), title TEXT NOT NULL, status TEXT NOT NULL, agent TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0, declare_key TEXT NOT NULL, claimed_by TEXT NOT NULL DEFAULT '', claimed_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
	}
	for _, statement := range oldSchema {
		if _, err := oldDB.Exec(statement); err != nil {
			t.Fatalf("create old schema: %v", err)
		}
	}
	if _, err := oldDB.Exec(`INSERT INTO projects(id, name, root_path, created_at) VALUES ('project-old', 'old', '/repos/old', '2026-08-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert old project: %v", err)
	}
	if _, err := oldDB.Exec(`INSERT INTO goals(id, project_id, title, description, status, created_at, updated_at) VALUES ('goal-old', 'project-old', 'old goal', '', 'active', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert old goal: %v", err)
	}
	if _, err := oldDB.Exec(`INSERT INTO tasks(id, goal_id, title, status, agent, sort_order, declare_key, claimed_by, claimed_at, created_at, updated_at) VALUES ('task-old', 'goal-old', 'old task', 'todo', 'old-agent', 0, 'old-key', '', NULL, '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert old task: %v", err)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatalf("close old DB: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open migrated DB: %v", err)
	}
	defer s.Close()

	tasks, err := s.ListTasks(context.Background(), "goal-old")
	if err != nil {
		t.Fatalf("ListTasks after migration: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "task-old" || tasks[0].Title != "old task" {
		t.Fatalf("migrated tasks = %+v, want original task", tasks)
	}
	if len(tasks[0].Files) != 0 {
		t.Fatalf("migrated task files = %#v, want empty", tasks[0].Files)
	}
	var storedFiles string
	if err := s.DB().QueryRow(`SELECT files FROM tasks WHERE id = 'task-old'`).Scan(&storedFiles); err != nil {
		t.Fatalf("read migrated files column: %v", err)
	}
	if storedFiles != "[]" {
		t.Fatalf("migrated files value = %q, want []", storedFiles)
	}
}
