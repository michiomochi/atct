package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecisionConstraintRejectsMissingTaskID(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	for _, test := range []struct {
		status string
		taskID any
	}{
		{status: "open", taskID: nil},
		{status: "open", taskID: ""},
		{status: "answered", taskID: nil},
		{status: "answered", taskID: ""},
	} {
		_, err := s.DB().Exec(`
INSERT INTO decisions (id, goal_id, task_id, kind, question, status, created_at)
		VALUES (?, ?, ?, 'decision', 'Choose one', ?, '2026-08-18T00:00:00Z')`,
			fmt.Sprintf("missing-task-%s-%v", test.status, test.taskID), goalID, test.taskID, test.status)
		if err == nil {
			t.Fatalf("insert with status=%q and task_id=%#v succeeded, want CHECK constraint failure", test.status, test.taskID)
		}
		if !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("insert with status=%q and task_id=%#v error = %v, want CHECK constraint failure", test.status, test.taskID, err)
		}
	}
}

func TestOpenMigratesWithdrawnDecisionWithoutTaskID(t *testing.T) {
	testMigratesClosedDecisionWithoutTaskID(t, "withdrawn")
}

func TestOpenMigratesAppliedDecisionWithoutTaskID(t *testing.T) {
	testMigratesClosedDecisionWithoutTaskID(t, "applied")
}

func TestDecisionConstraintAllowsTaskID(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(context.Background(), goalID, "agent", "constraint-task", []string{"task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	if _, err := s.DB().Exec(`
INSERT INTO decisions (id, goal_id, task_id, kind, question, status, created_at)
VALUES ('with-task', ?, ?, 'decision', 'Choose one', 'open', '2026-08-18T00:00:00Z')`, goalID, tasks[0].ID); err != nil {
		t.Fatalf("insert with task_id: %v", err)
	}
}

func TestCompletionDecisionAllowsMissingTaskID(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	if _, err := s.DB().Exec(`
	INSERT INTO decisions (id, goal_id, kind, question, status, created_at)
VALUES ('completion-without-task', ?, 'completion', 'Approve?', 'open', '2026-08-18T00:00:00Z')`, goalID); err != nil {
		t.Fatalf("insert completion without task_id: %v", err)
	}
}

func TestOpenMigratesDecisionsWithoutLosingData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	createLegacyDecisionsDB(t, dbPath, 3, "")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open migrated DB: %v", err)
	}
	defer s.Close()

	var count int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM decisions`).Scan(&count); err != nil {
		t.Fatalf("count migrated decisions: %v", err)
	}
	if count != 2 {
		t.Fatalf("migrated decision count = %d, want 2", count)
	}

	var taskID, kind string
	if err := s.DB().QueryRow(`SELECT task_id, kind FROM decisions WHERE id = 'decision-old'`).Scan(&taskID, &kind); err != nil {
		t.Fatalf("read migrated decision: %v", err)
	}
	if taskID != "task-old" || kind != "decision" {
		t.Fatalf("migrated decision = task_id %q, kind %q, want task-old/decision", taskID, kind)
	}

	var completionTaskID sql.NullString
	if err := s.DB().QueryRow(`SELECT task_id FROM decisions WHERE id = 'completion-old'`).Scan(&completionTaskID); err != nil {
		t.Fatalf("read migrated completion: %v", err)
	}
	if completionTaskID.Valid {
		t.Fatalf("migrated completion task_id = %q, want NULL", completionTaskID.String)
	}

	var indexName string
	if err := s.DB().QueryRow(`SELECT name FROM pragma_index_list('decisions') WHERE name = 'idx_decisions_open'`).Scan(&indexName); err != nil {
		t.Fatalf("decisions index after migration: %v", err)
	}
	if indexName != "idx_decisions_open" {
		t.Fatalf("decisions index = %q, want idx_decisions_open", indexName)
	}

	var foreignKeyTable string
	if err := s.DB().QueryRow(`SELECT fk."table" FROM pragma_foreign_key_list('decisions') AS fk WHERE fk."table" = 'tasks'`).Scan(&foreignKeyTable); err != nil {
		t.Fatalf("decisions task foreign key after migration: %v", err)
	}
	if foreignKeyTable != "tasks" {
		t.Fatalf("decisions foreign key table = %q, want tasks", foreignKeyTable)
	}
}

func TestOpenRejectsInvalidDecisionMigrationWithoutChangingData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	createLegacyDecisionsDB(t, dbPath, 3, "open")

	_, err := Open(dbPath)
	if err == nil {
		t.Fatal("Open invalid DB succeeded, want migration error")
	}
	for _, want := range []string{"1", "task_id", "withdraw"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("migration error = %q, want it to contain %q", err, want)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen invalid DB: %v", err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version after failed migration: %v", err)
	}
	if version != 3 {
		t.Fatalf("user_version after failed migration = %d, want 3", version)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM decisions WHERE id = 'decision-without-task' AND task_id IS NULL`).Scan(&count); err != nil {
		t.Fatalf("read invalid decision after failed migration: %v", err)
	}
	if count != 1 {
		t.Fatalf("invalid decision rows after failed migration = %d, want 1", count)
	}
}

func TestOpenDecisionMigrationIncrementsUserVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	createLegacyDecisionsDB(t, dbPath, 3, "")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open migrated DB: %v", err)
	}
	defer s.Close()

	var version int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version <= 3 {
		t.Fatalf("user_version = %d, want greater than 3", version)
	}
}

func newTestDecisionTask(t *testing.T, s *Store, goalID, declareKey string) string {
	t.Helper()

	tasks, err := s.DeclareTasks(context.Background(), goalID, "test-agent", declareKey, []string{"decision task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	return tasks[0].ID
}

func testMigratesClosedDecisionWithoutTaskID(t *testing.T, status string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	createLegacyDecisionsDB(t, dbPath, 3, status)

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open %s decision DB: %v", status, err)
	}
	defer s.Close()

	var gotStatus string
	var taskID sql.NullString
	if err := s.DB().QueryRow(`SELECT status, task_id FROM decisions WHERE id = 'decision-without-task'`).Scan(&gotStatus, &taskID); err != nil {
		t.Fatalf("read migrated %s decision: %v", status, err)
	}
	if gotStatus != status {
		t.Fatalf("migrated decision status = %q, want %q", gotStatus, status)
	}
	if taskID.Valid {
		t.Fatalf("migrated %s decision task_id = %q, want NULL", status, taskID.String)
	}

	var version int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version after %s migration: %v", status, err)
	}
	if version <= 3 {
		t.Fatalf("user_version after %s migration = %d, want greater than 3", status, version)
	}
}

func createLegacyDecisionsDB(t *testing.T, dbPath string, version int, missingTaskStatus string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy DB: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
PRAGMA foreign_keys = ON;
CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  root_path TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);
CREATE TABLE goals (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  result_summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  goal_id TEXT NOT NULL REFERENCES goals(id),
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  agent TEXT NOT NULL DEFAULT '',
  files TEXT NOT NULL DEFAULT '[]',
  sort_order INTEGER NOT NULL DEFAULT 0,
  declare_key TEXT NOT NULL,
  claimed_by TEXT NOT NULL DEFAULT '',
  claimed_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE decisions (
  id TEXT PRIMARY KEY,
  goal_id TEXT NOT NULL REFERENCES goals(id),
  task_id TEXT REFERENCES tasks(id),
  kind TEXT NOT NULL,
  question TEXT NOT NULL,
  options TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL,
  default_option TEXT NOT NULL DEFAULT '',
  default_after_ms INTEGER,
  default_applied_at TEXT,
  answer_label TEXT NOT NULL DEFAULT '',
  answer_text TEXT NOT NULL DEFAULT '',
  answered_at TEXT,
  applied_at TEXT,
  run_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX idx_decisions_open ON decisions(status, goal_id);
INSERT INTO projects (id, name, root_path, created_at)
VALUES ('project-old', 'old', '/old', '2026-08-18T00:00:00Z');
INSERT INTO goals (id, project_id, title, status, created_at, updated_at)
VALUES ('goal-old', 'project-old', 'old goal', 'active', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z');
INSERT INTO tasks (id, goal_id, title, status, declare_key, created_at, updated_at)
VALUES ('task-old', 'goal-old', 'old task', 'todo', 'old-task', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z');
PRAGMA user_version = ` + fmt.Sprint(version) + `;`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}

	if missingTaskStatus != "" {
		_, err = db.Exec(`
INSERT INTO decisions (id, goal_id, kind, question, status, created_at)
		VALUES ('decision-without-task', 'goal-old', 'decision', 'Choose one', ?, '2026-08-18T00:00:00Z')`, missingTaskStatus)
	} else {
		_, err = db.Exec(`
INSERT INTO decisions (id, goal_id, task_id, kind, question, status, run_id, created_at)
VALUES ('decision-old', 'goal-old', 'task-old', 'decision', 'Choose one', 'answered', 'run-old', '2026-08-18T00:00:00Z');
INSERT INTO decisions (id, goal_id, kind, question, status, run_id, created_at)
VALUES ('completion-old', 'goal-old', 'completion', 'Approve?', 'applied', 'run-old', '2026-08-18T00:00:01Z')`)
	}
	if err != nil {
		t.Fatalf("insert legacy decisions: %v", err)
	}
}
