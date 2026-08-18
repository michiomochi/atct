package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestDecisionRoundTripsDefaultFields(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "default-fields")
	after := int64(1800000)

	saved, err := s.AskDecision(ctx, AskInput{
		GoalID:         goalID,
		TaskID:         taskID,
		Kind:           domain.DecisionKind("decision"),
		Question:       "A か B か",
		Options:        []domain.Option{{Label: "A"}, {Label: "B"}},
		DefaultOption:  "A",
		DefaultAfterMs: &after,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetDecision(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultOption != "A" {
		t.Fatalf("DefaultOption = %q, want A", got.DefaultOption)
	}
	if got.DefaultAfterMs == nil || *got.DefaultAfterMs != after {
		t.Fatalf("DefaultAfterMs = %v, want %d", got.DefaultAfterMs, after)
	}
	if got.DefaultAppliedAt != nil {
		t.Fatal("DefaultAppliedAt should be nil before it fires")
	}
}

func TestOpenMigratesDecisionDefaultColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
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
  goal_id TEXT NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  agent TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  declare_key TEXT NOT NULL,
  claimed_by TEXT NOT NULL DEFAULT '',
  claimed_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE decisions (
  id TEXT PRIMARY KEY,
  goal_id TEXT NOT NULL,
  task_id TEXT,
  kind TEXT NOT NULL,
  question TEXT NOT NULL,
  options TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL,
  answer_label TEXT NOT NULL DEFAULT '',
  answer_text TEXT NOT NULL DEFAULT '',
  answered_by TEXT NOT NULL DEFAULT '',
  answered_at TEXT,
  applied_at TEXT,
  run_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
PRAGMA user_version = 1;
INSERT INTO projects (id, name, root_path, created_at)
VALUES ('old-project', 'old project', '/old-project', '2026-08-18T00:00:00Z');
INSERT INTO goals (id, project_id, title, status, created_at, updated_at)
VALUES ('old-goal', 'old-project', 'old goal', 'active', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z');
INSERT INTO tasks (id, goal_id, title, status, declare_key, created_at, updated_at)
VALUES ('old-task', 'old-goal', 'old task', 'todo', 'old-task', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z');
INSERT INTO decisions (id, goal_id, task_id, kind, question, options, status, created_at)
VALUES ('old-decision', 'old-goal', 'old-task', 'decision', 'old question', '[]', 'open', '2026-08-18T00:00:00Z');
`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := s.GetDecision(context.Background(), "old-decision")
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultOption != "" {
		t.Fatalf("migrated DefaultOption = %q, want empty", got.DefaultOption)
	}
	if got.DefaultAfterMs != nil {
		t.Fatalf("migrated DefaultAfterMs = %v, want nil", got.DefaultAfterMs)
	}
	if got.DefaultAppliedAt != nil {
		t.Fatalf("migrated DefaultAppliedAt = %v, want nil", got.DefaultAppliedAt)
	}
}
