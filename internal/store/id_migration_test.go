package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigratesUUIDPrimaryKeysToSequentialIDs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "uuid-primary-keys.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	raw.SetMaxOpenConns(1)

	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		raw.Close()
		t.Fatalf("load embedded migrations: %v", err)
	}
	for _, migration := range migrations {
		if migration.filename == "0018_integer_primary_keys.sql" {
			break
		}
		if _, err := raw.Exec(migration.sql); err != nil {
			raw.Close()
			t.Fatalf("apply fixture migration %s: %v", migration.filename, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, migration.filename, "2026-08-26T00:00:00Z"); err != nil {
			raw.Close()
			t.Fatalf("record fixture migration %s: %v", migration.filename, err)
		}
	}
	if _, err := raw.Exec(`PRAGMA user_version = 6`); err != nil {
		raw.Close()
		t.Fatalf("set fixture schema version: %v", err)
	}

	// A historical bootstrap can recreate this otherwise-dead table after 0004.
	if _, err := raw.Exec(`CREATE TABLE runs (id TEXT PRIMARY KEY, project_id TEXT REFERENCES projects(id), registered_at TEXT NOT NULL)`); err != nil {
		raw.Close()
		t.Fatalf("create ghost runs table: %v", err)
	}
	if _, err := raw.Exec(`
INSERT INTO projects (id, name, root_path, created_at, claimed_by) VALUES
  ('11111111-0000-0000-0000-000000000000', 'earliest', '/earliest', '2026-08-20T00:00:00Z', 'session-1'),
  ('22222222-0000-0000-0000-000000000000', 'same-time-first', '/same-time-first', '2026-08-21T00:00:00Z', ''),
  ('33333333-0000-0000-0000-000000000000', 'same-time-second', '/same-time-second', '2026-08-21T00:00:00Z', '');
INSERT INTO agent_sessions (id, project_id, registered_at, pid, started_at, session_key) VALUES
  ('session-1', '11111111-0000-0000-0000-000000000000', '2026-08-22T00:00:00Z', 1, '2026-08-22T00:00:00Z', 'session-key-1'),
  ('session-2', '22222222-0000-0000-0000-000000000000', '2026-08-22T00:00:01Z', 2, '2026-08-22T00:00:01Z', 'session-key-2');
INSERT INTO goals (id, project_id, derived_from_goal_id, content, status, creator, created_at, updated_at) VALUES
  ('44444444-0000-0000-0000-000000000000', '11111111-0000-0000-0000-000000000000', NULL, 'parent goal', 'active', 'human', '2026-08-20T00:00:00Z', '2026-08-20T00:00:00Z'),
  ('55555555-0000-0000-0000-000000000000', '22222222-0000-0000-0000-000000000000', '44444444-0000-0000-0000-000000000000', 'child goal', 'active', 'human', '2026-08-21T00:00:00Z', '2026-08-21T00:00:00Z');
INSERT INTO tasks (id, goal_id, title, description, status, declare_key, sort_order, created_at, updated_at) VALUES
  ('66666666-0000-0000-0000-000000000000', '55555555-0000-0000-0000-000000000000', 'first task', '', 'todo', 'task-key-1', 0, '2026-08-20T00:00:00Z', '2026-08-20T00:00:00Z'),
  ('77777777-0000-0000-0000-000000000000', '55555555-0000-0000-0000-000000000000', 'second task', '', 'todo', 'task-key-2', 1, '2026-08-21T00:00:00Z', '2026-08-21T00:00:00Z');
INSERT INTO decisions (id, goal_id, task_id, kind, question, status, agent_session_id, created_at) VALUES
  ('88888888-0000-0000-0000-000000000000', '55555555-0000-0000-0000-000000000000', '66666666-0000-0000-0000-000000000000', 'decision', 'Proceed?', 'open', 'session-2', '2026-08-20T00:00:00Z');
INSERT INTO task_commits (task_id, sha, subject, created_at) VALUES
  ('66666666-0000-0000-0000-000000000000', 'abc123', 'fixture commit', '2026-08-20T00:00:00Z');
INSERT INTO task_handoffs (id, task_id, requested_by, received_by, requested_at, received_at) VALUES
  ('fixture-task-handoff', '66666666-0000-0000-0000-000000000000', 'session-1', 'session-2', '2026-08-22T00:00:00Z', '2026-08-22T00:00:01Z');
INSERT INTO goal_handoffs (id, goal_id, requested_by, received_by, requested_at, received_at) VALUES
  ('fixture-goal-handoff', '55555555-0000-0000-0000-000000000000', 'session-1', 'session-2', '2026-08-22T00:00:00Z', '2026-08-22T00:00:01Z');
`); err != nil {
		raw.Close()
		t.Fatalf("insert UUID fixture: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	migrated, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer migrated.Close()

	for _, table := range []string{"projects", "goals", "tasks", "decisions"} {
		assertIntegerPrimaryKey(t, migrated.DB(), table)
		assertLegacyIDRemoved(t, migrated.DB(), table)
	}
	assertIntegerPrimaryKey(t, migrated.DB(), "agent_sessions")
	assertTextPrimaryKey(t, migrated.DB(), "task_handoffs")
	assertTextPrimaryKey(t, migrated.DB(), "goal_handoffs")

	for _, want := range []struct {
		table string
		id    int64
		value string
	}{
		{"projects", 1, "earliest"},
		{"projects", 2, "same-time-first"},
		{"projects", 3, "same-time-second"},
		{"goals", 1, "parent goal"},
		{"goals", 2, "child goal"},
		{"tasks", 1, "first task"},
		{"tasks", 2, "second task"},
		{"decisions", 1, "Proceed?"},
	} {
		column := "name"
		if want.table == "goals" {
			column = "content"
		} else if want.table == "tasks" {
			column = "title"
		} else if want.table == "decisions" {
			column = "question"
		}
		var got string
		query := `SELECT ` + column + ` FROM ` + want.table + ` WHERE id = ?`
		if err := migrated.DB().QueryRow(query, want.id).Scan(&got); err != nil {
			t.Fatalf("read migrated %s id %d: %v", want.table, want.id, err)
		}
		if got != want.value {
			t.Errorf("migrated %s id %d value = %q, want %q", want.table, want.id, got, want.value)
		}
	}

	for _, want := range []struct {
		key string
		id  int64
	}{
		{"session-key-1", 1},
		{"session-key-2", 2},
	} {
		var got int64
		if err := migrated.DB().QueryRow(`SELECT id FROM agent_sessions WHERE session_key = ?`, want.key).Scan(&got); err != nil {
			t.Fatalf("look up migrated agent session %s: %v", want.key, err)
		}
		if got != want.id {
			t.Errorf("migrated agent session for %s = %d, want %d", want.key, got, want.id)
		}
	}

	var claimedBy, decisionSessionID, requestedBy, receivedBy int64
	if err := migrated.DB().QueryRow(`SELECT claimed_by FROM projects WHERE name = 'earliest'`).Scan(&claimedBy); err != nil {
		t.Fatalf("read migrated project claim: %v", err)
	}
	if claimedBy != 1 {
		t.Errorf("migrated project claim = %d, want 1", claimedBy)
	}
	if err := migrated.DB().QueryRow(`SELECT agent_session_id FROM decisions WHERE id = 1`).Scan(&decisionSessionID); err != nil {
		t.Fatalf("read migrated decision session: %v", err)
	}
	if decisionSessionID != 2 {
		t.Errorf("migrated decision session = %d, want 2", decisionSessionID)
	}
	if err := migrated.DB().QueryRow(`SELECT requested_by, received_by FROM task_handoffs WHERE id = ?`, "fixture-task-handoff").Scan(&requestedBy, &receivedBy); err != nil {
		t.Fatalf("read migrated task handoff sessions: %v", err)
	}
	if requestedBy != 1 || receivedBy != 2 {
		t.Errorf("migrated task handoff sessions = (%d, %d), want (1, 2)", requestedBy, receivedBy)
	}
	if err := migrated.DB().QueryRow(`SELECT requested_by, received_by FROM goal_handoffs WHERE id = ?`, "fixture-goal-handoff").Scan(&requestedBy, &receivedBy); err != nil {
		t.Fatalf("read migrated goal handoff sessions: %v", err)
	}
	if requestedBy != 1 || receivedBy != 2 {
		t.Errorf("migrated goal handoff sessions = (%d, %d), want (1, 2)", requestedBy, receivedBy)
	}

	assertNoForeignKeyViolations(t, migrated.DB())
	assertForeignKeyCount(t, migrated.DB(), 13)
	assertTableAbsent(t, migrated.DB(), "runs")
}

func assertIntegerPrimaryKey(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var columnType string
	if err := db.QueryRow(`SELECT type FROM pragma_table_info(?) WHERE name = 'id'`, table).Scan(&columnType); err != nil {
		t.Fatalf("read %s id type: %v", table, err)
	}
	if columnType != "INTEGER" {
		t.Errorf("%s.id type = %q, want INTEGER", table, columnType)
	}
}

func assertTextPrimaryKey(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var columnType string
	if err := db.QueryRow(`SELECT type FROM pragma_table_info(?) WHERE name = 'id'`, table).Scan(&columnType); err != nil {
		t.Fatalf("read %s id type: %v", table, err)
	}
	if columnType != "TEXT" {
		t.Errorf("%s.id type = %q, want TEXT", table, columnType)
	}
}

func assertNoForeignKeyViolations(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("run foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check found an orphaned row")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read foreign_key_check: %v", err)
	}
}

func assertForeignKeyCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	for _, table := range []string{"projects", "agent_sessions", "goals", "tasks", "decisions", "task_commits", "task_handoffs", "goal_handoffs"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list(?)`, table).Scan(&count); err != nil {
			t.Fatalf("count %s foreign keys: %v", table, err)
		}
		got += count
	}
	if got != want {
		t.Errorf("foreign key count = %d, want %d", got, want)
	}
}

func assertTableAbsent(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("check %s table: %v", table, err)
	}
	if count != 0 {
		t.Errorf("%s table count = %d, want 0", table, count)
	}
}
