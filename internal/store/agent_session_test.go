package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectIDForAgentSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-project", 0); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "run-project", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}

	got, err := s.ProjectIDForAgentSession(ctx, "run-project")
	if err != nil {
		t.Fatalf("ProjectIDForAgentSession: %v", err)
	}
	if got != project.ID {
		t.Fatalf("ProjectIDForAgentSession = %q, want %q", got, project.ID)
	}

	if err := s.RegisterAgentSession(ctx, "run-unassociated", 0); err != nil {
		t.Fatalf("RegisterAgentSession(unassociated): %v", err)
	}
	if _, err := s.ProjectIDForAgentSession(ctx, "run-unassociated"); err == nil || !strings.Contains(err.Error(), "not associated") {
		t.Fatalf("ProjectIDForAgentSession(unassociated) error = %v, want not associated error", err)
	}
	if _, err := s.ProjectIDForAgentSession(ctx, "run-missing"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("ProjectIDForAgentSession(missing) error = %v, want not registered error", err)
	}
}

func TestProjectIDForTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-key", []string{"task"}, []string{"Complete the declared task and make its result observable to the run."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	got, err := s.ProjectIDForTask(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("ProjectIDForTask: %v", err)
	}
	if got != project.ID {
		t.Fatalf("ProjectIDForTask = %q, want %q", got, project.ID)
	}
	if _, err := s.ProjectIDForTask(ctx, "task-missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ProjectIDForTask(missing) error = %v, want not found error", err)
	}
}

func TestAgentSessionCleanupRemovesOldRecordsWithoutRemovingProjects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	old := time.Now().UTC().Add(-90 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO agent_sessions (id, project_id, registered_at) VALUES (?, ?, ?), (?, NULL, ?)
	`, "run-old-project", project.ID, old, "run-old-unbound", old); err != nil {
		t.Fatalf("insert old agent sessions: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-current", 0); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "run-current", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}

	latest, err := s.LatestAgentSessionID(ctx, project.ID)
	if err != nil {
		t.Fatalf("LatestAgentSessionID: %v", err)
	}
	if latest != "run-current" {
		t.Fatalf("LatestAgentSessionID = %q, want %q", latest, "run-current")
	}
	var oldCount int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_sessions WHERE id IN (?, ?)`, "run-old-project", "run-old-unbound").Scan(&oldCount); err != nil {
		t.Fatalf("count old agent sessions: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("old agent session count = %d, want 0", oldCount)
	}
	var projectCount int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID).Scan(&projectCount); err != nil {
		t.Fatalf("count project: %v", err)
	}
	if projectCount != 1 {
		t.Fatalf("project count = %d, want 1", projectCount)
	}
}

func TestOpenMigratesV4DatabaseWithoutLosingHumanData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	const v4Schema = `
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
CREATE UNIQUE INDEX idx_tasks_declare_key ON tasks(goal_id, declare_key);
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
  created_at TEXT NOT NULL,
  CHECK (kind <> 'decision' OR status NOT IN ('open', 'answered') OR (task_id IS NOT NULL AND task_id <> ''))
);
CREATE INDEX idx_decisions_open ON decisions(status, goal_id);
PRAGMA user_version = 4;
`
	if _, err := raw.Exec(v4Schema); err != nil {
		raw.Close()
		t.Fatalf("create v4 database: %v", err)
	}
	const createdAt = "2026-08-01T00:00:00Z"
	if _, err := raw.Exec(`INSERT INTO projects (id, name, root_path, created_at) VALUES (?, ?, ?, ?)`, "project-human", "human project", "/repos/human", createdAt); err != nil {
		raw.Close()
		t.Fatalf("insert project: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO goals (id, project_id, title, description, status, result_summary, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "goal-human", "project-human", "Human goal", "Keep this goal", "active", "", createdAt, createdAt); err != nil {
		raw.Close()
		t.Fatalf("insert goal: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO tasks (id, goal_id, title, status, agent, files, sort_order, declare_key, claimed_by, claimed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "task-human", "goal-human", "Human task", "todo", "human", `["src/main.go"]`, 0, "human-key", "human-run", createdAt, createdAt, createdAt); err != nil {
		raw.Close()
		t.Fatalf("insert task: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO decisions (id, goal_id, task_id, kind, question, options, status, default_option, default_after_ms, default_applied_at, answer_label, answer_text, answered_at, applied_at, run_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "decision-human", "goal-human", "task-human", "decision", "Keep this decision", `[{"label":"yes"}]`, "answered", "", nil, nil, "yes", "Keep it", createdAt, nil, "human-run", createdAt); err != nil {
		raw.Close()
		t.Fatalf("insert decision: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open v4 database: %v", err)
	}
	defer s.Close()
	var version int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read migrated schema version: %v", err)
	}
	// Compare against the constant so a new migration does not silently leave
	// this test asserting the previous version.
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	var agentSessionTable string
	if err := s.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'agent_sessions'`).Scan(&agentSessionTable); err != nil {
		t.Fatalf("find agent_sessions table: %v", err)
	}
	if agentSessionTable != "agent_sessions" {
		t.Fatalf("agent_sessions table = %q, want agent_sessions", agentSessionTable)
	}
	var projectName, goalTitle, taskTitle, taskFiles, decisionQuestion, decisionAnswer string
	if err := s.DB().QueryRow(`SELECT name FROM projects WHERE id = ?`, "project-human").Scan(&projectName); err != nil {
		t.Fatalf("read migrated project: %v", err)
	}
	if err := s.DB().QueryRow(`SELECT title FROM goals WHERE id = ?`, "goal-human").Scan(&goalTitle); err != nil {
		t.Fatalf("read migrated goal: %v", err)
	}
	if err := s.DB().QueryRow(`SELECT title, files FROM tasks WHERE id = ?`, "task-human").Scan(&taskTitle, &taskFiles); err != nil {
		t.Fatalf("read migrated task: %v", err)
	}
	if err := s.DB().QueryRow(`SELECT question, answer_text FROM decisions WHERE id = ?`, "decision-human").Scan(&decisionQuestion, &decisionAnswer); err != nil {
		t.Fatalf("read migrated decision: %v", err)
	}
	if projectName != "human project" || goalTitle != "Human goal" || taskTitle != "Human task" || taskFiles != `["src/main.go"]` || decisionQuestion != "Keep this decision" || decisionAnswer != "Keep it" {
		t.Fatalf("migrated human data changed: project=%q goal=%q task=%q files=%q decision=%q answer=%q", projectName, goalTitle, taskTitle, taskFiles, decisionQuestion, decisionAnswer)
	}
}
