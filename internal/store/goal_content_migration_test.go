package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestCreateGoalRejectsBlankContent(t *testing.T) {
	s := newTestStore(t)

	for _, content := range []string{"", " \t\n "} {
		t.Run(content, func(t *testing.T) {
			if _, err := s.CreateGoal(context.Background(), "project-goal-content", content, "human"); err == nil {
				t.Fatalf("CreateGoal(%q) succeeded, want an error", content)
			}
		})
	}
}

func TestOpenMigratesGoalContentWithoutLosingData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "goal-content.db")
	createLegacyGoalContentDB(t, dbPath)

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) = %v", dbPath, err)
	}
	defer s.Close()

	rows, err := s.db.QueryContext(context.Background(), `
		SELECT id, content
		FROM goals
		ORDER BY id
	`)
	if err != nil {
		t.Fatalf("query migrated goals: %v", err)
	}
	defer rows.Close()

	got := make(map[string]string)
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			t.Fatalf("scan migrated goal: %v", err)
		}
		got[id] = content
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated goals: %v", err)
	}

	if got["goal-content-empty-desc"] != "First goal title" {
		t.Fatalf("empty-description content = %q, want %q", got["goal-content-empty-desc"], "First goal title")
	}
	if got["goal-content-space-desc"] != "Third goal title" {
		t.Fatalf("space-only-description content = %q, want %q", got["goal-content-space-desc"], "Third goal title")
	}

	withDescription := got["goal-content-with-desc"]
	if gotHeadline := domain.Headline(withDescription); gotHeadline != "Second goal title" {
		t.Fatalf("non-empty-description headline = %q, want %q", gotHeadline, "Second goal title")
	}
	if !strings.Contains(withDescription, "Explain the preserved body") {
		t.Fatalf("non-empty-description content = %q, want preserved description", withDescription)
	}
}

func createLegacyGoalContentDB(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}

	_, err = db.Exec(`
PRAGMA foreign_keys = ON;

CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  root_path TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);

CREATE TABLE agent_sessions (
  id TEXT PRIMARY KEY,
  project_id TEXT REFERENCES projects(id),
  registered_at TEXT NOT NULL,
  pid INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_agent_sessions_project_registered_at
  ON agent_sessions(project_id, registered_at DESC);

CREATE TABLE goals (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  creator TEXT NOT NULL DEFAULT 'human',
  result_summary TEXT NOT NULL DEFAULT '',
  work_done TEXT NOT NULL DEFAULT '',
  now_possible TEXT NOT NULL DEFAULT '',
  how_to_verify TEXT NOT NULL DEFAULT '',
  surprises TEXT NOT NULL DEFAULT '',
  needs_review TEXT NOT NULL DEFAULT '',
  next_steps TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK (
    status <> 'done' OR (
      length(trim(work_done)) > 0 AND length(work_done) <= 2000 AND
      length(trim(now_possible)) > 0 AND length(now_possible) <= 2000 AND
      length(trim(how_to_verify)) > 0 AND length(how_to_verify) <= 2000 AND
      length(trim(surprises)) > 0 AND length(surprises) <= 2000 AND
      length(trim(needs_review)) > 0 AND length(needs_review) <= 2000 AND
      length(trim(next_steps)) > 0 AND length(next_steps) <= 2000
    )
  )
);

CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  goal_id TEXT NOT NULL REFERENCES goals(id),
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  agent TEXT NOT NULL DEFAULT '',
  files TEXT NOT NULL DEFAULT '[]',
  description TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  declare_key TEXT NOT NULL,
  claimed_by TEXT NOT NULL DEFAULT '',
  claimed_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_tasks_declare_key
  ON tasks(goal_id, declare_key);

CREATE UNIQUE INDEX idx_tasks_goal_sort_order
  ON tasks(goal_id, sort_order);

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
  agent_session_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  CHECK (kind <> 'decision' OR status NOT IN ('open', 'answered') OR (task_id IS NOT NULL AND task_id <> ''))
);

CREATE INDEX idx_decisions_open
  ON decisions(status, goal_id);

CREATE TABLE task_commits (
  task_id TEXT NOT NULL REFERENCES tasks(id),
  sha TEXT NOT NULL,
  subject TEXT NOT NULL,
  files_changed INTEGER NOT NULL DEFAULT 0,
  insertions INTEGER NOT NULL DEFAULT 0,
  deletions INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  PRIMARY KEY (task_id, sha)
);

CREATE TABLE schema_migrations (
  filename TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
);

INSERT INTO projects (id, name, root_path, created_at)
VALUES ('project-goal-content', 'goal-content', '/tmp/goal-content', '2026-08-21T00:00:00Z');

INSERT INTO goals (
  id, project_id, title, description, status, creator,
  result_summary, work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
)
VALUES
  (
    'goal-content-empty-desc', 'project-goal-content', 'First goal title', '', 'active', 'human',
    '', '', '', '', '', '', '', '2026-08-21T00:00:00Z', '2026-08-21T00:00:00Z'
  ),
	(
	    'goal-content-with-desc', 'project-goal-content', 'Second goal title', 'Explain the preserved body', 'active', 'human',
	    '', '', '', '', '', '', '', '2026-08-21T00:00:00Z', '2026-08-21T00:00:00Z'
	),
	(
	    'goal-content-space-desc', 'project-goal-content', 'Third goal title', '   ', 'active', 'human',
	    '', '', '', '', '', '', '', '2026-08-21T00:00:00Z', '2026-08-21T00:00:00Z'
	);

PRAGMA user_version = 6;
`)
	if err != nil {
		db.Close()
		t.Fatalf("create legacy database: %v", err)
	}

	for _, filename := range []string{
		"0001_baseline.sql",
		"0002_task_description.sql",
		"0003_unique_task_sort_order.sql",
		"0004_agent_sessions.sql",
		"0005_goal_creator.sql",
		"0006_task_commits.sql",
	} {
		if _, err := db.Exec(
			"INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)",
			filename,
			"2026-08-21T00:00:00Z",
		); err != nil {
			db.Close()
			t.Fatalf("record legacy migration %s: %v", filename, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
}
