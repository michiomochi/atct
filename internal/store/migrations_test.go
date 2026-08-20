package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaSQLCompletionReportCheckMatchesGoLimit(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "schema.sql"))
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}
	schema := string(contents)
	for _, field := range []string{
		"work_done",
		"now_possible",
		"how_to_verify",
		"surprises",
		"needs_review",
		"next_steps",
	} {
		literal := fmt.Sprintf("length(%s) <= %d", field, completionReportMaxLength)
		if !strings.Contains(schema, literal) {
			t.Errorf("schema.sql is missing %q", literal)
		}
	}
}

func TestEmptyDatabaseAppliesBaselineMigration(t *testing.T) {
	db := openMigrationTestDB(t)

	if err := applyEmbeddedMigrations(db); err != nil {
		t.Fatalf("apply embedded migrations: %v", err)
	}
	assertUserVersion(t, db, schemaVersion)
	for _, filename := range []string{
		"0001_baseline.sql",
		"0002_task_description.sql",
		"0003_unique_task_sort_order.sql",
		"0004_agent_sessions.sql",
	} {
		assertMigrationRecorded(t, db, filename)
	}
	for _, table := range []string{"projects", "agent_sessions", "goals", "tasks", "decisions", "schema_migrations"} {
		assertTableExists(t, db, table)
	}
}

func TestAgentSessionMigrationRenamesLegacySchema(t *testing.T) {
	db := openMigrationTestDB(t)
	if _, err := db.Exec(embeddedBaselineSQL(t)); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO projects (id, name, root_path, created_at)
VALUES ('project-legacy', 'legacy project', '/legacy', '2026-08-20T00:00:00Z');
INSERT INTO goals (id, project_id, title, status, created_at, updated_at)
VALUES ('goal-legacy', 'project-legacy', 'Legacy goal', 'active', '2026-08-20T00:00:00Z', '2026-08-20T00:00:00Z');
INSERT INTO runs (id, project_id, registered_at)
VALUES ('session-one', 'project-legacy', '2026-08-20T00:01:00Z'),
       ('session-two', NULL, '2026-08-20T00:02:00Z');
INSERT INTO decisions (id, goal_id, kind, question, status, run_id, created_at)
VALUES ('decision-legacy', 'goal-legacy', 'completion', 'Continue?', 'open', 'session-one', '2026-08-20T00:03:00Z');
PRAGMA user_version = 6;
`); err != nil {
		t.Fatalf("insert legacy rows: %v", err)
	}

	var beforeSessions, beforeDecisions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&beforeSessions); err != nil {
		t.Fatalf("count legacy runs: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM decisions`).Scan(&beforeDecisions); err != nil {
		t.Fatalf("count legacy decisions: %v", err)
	}

	if err := applyEmbeddedMigrations(db); err != nil {
		t.Fatalf("apply agent session migration: %v", err)
	}

	assertTableExists(t, db, "agent_sessions")
	var oldTableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'runs'`).Scan(&oldTableCount); err != nil {
		t.Fatalf("check old runs table: %v", err)
	}
	if oldTableCount != 0 {
		t.Fatalf("old runs table count = %d, want 0", oldTableCount)
	}

	agentSessionColumns := migrationTableColumns(t, db, "agent_sessions")
	for _, column := range []string{"id", "project_id", "registered_at", "pid", "started_at"} {
		if _, ok := agentSessionColumns[column]; !ok {
			t.Errorf("agent_sessions is missing column %q", column)
		}
	}
	decisionColumns := migrationTableColumns(t, db, "decisions")
	if _, ok := decisionColumns["agent_session_id"]; !ok {
		t.Error("decisions is missing column agent_session_id")
	}
	if _, ok := decisionColumns["run_id"]; ok {
		t.Error("decisions still has column run_id")
	}

	var afterSessions, afterDecisions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_sessions`).Scan(&afterSessions); err != nil {
		t.Fatalf("count migrated agent sessions: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM decisions`).Scan(&afterDecisions); err != nil {
		t.Fatalf("count migrated decisions: %v", err)
	}
	if afterSessions != beforeSessions {
		t.Fatalf("agent_sessions row count = %d, want %d", afterSessions, beforeSessions)
	}
	if afterDecisions != beforeDecisions {
		t.Fatalf("decisions row count = %d, want %d", afterDecisions, beforeDecisions)
	}

	rows, err := db.Query(`SELECT pid, started_at FROM agent_sessions ORDER BY id`)
	if err != nil {
		t.Fatalf("read migrated agent session defaults: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pid int
		var startedAt string
		if err := rows.Scan(&pid, &startedAt); err != nil {
			t.Fatalf("scan migrated agent session defaults: %v", err)
		}
		if pid != 0 || startedAt != "" {
			t.Errorf("migrated agent session defaults = pid %d, started_at %q; want 0, empty", pid, startedAt)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read migrated agent session defaults: %v", err)
	}

	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_agent_sessions_project_registered_at'`).Scan(&indexCount); err != nil {
		t.Fatalf("check agent session index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("agent session index count = %d, want 1", indexCount)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&indexCount); err != nil {
		t.Fatalf("count schema migrations: %v", err)
	}
	if indexCount != 4 {
		t.Fatalf("schema migration count = %d, want 4", indexCount)
	}
	assertMigrationRecorded(t, db, "0004_agent_sessions.sql")
}

func TestExistingV6DatabaseRecordsBaselineWithoutExecutingIt(t *testing.T) {
	db := openMigrationTestDB(t)
	baseline := embeddedBaselineSQL(t)
	if _, err := db.Exec(baseline); err != nil {
		t.Fatalf("create v6 schema: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE schema_migrations`); err != nil {
		t.Fatalf("remove migration table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, root_path, created_at) VALUES ('p1', 'project', '/project', 'now')`); err != nil {
		t.Fatalf("insert sentinel row: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 6`); err != nil {
		t.Fatalf("set schema version: %v", err)
	}

	if err := applyEmbeddedMigrations(db); err != nil {
		t.Fatalf("adopt existing v6 schema: %v", err)
	}
	assertUserVersion(t, db, schemaVersion)
	assertMigrationRecorded(t, db, "0001_baseline.sql")
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = 'p1'`).Scan(&count); err != nil {
		t.Fatalf("read sentinel row: %v", err)
	}
	if count != 1 {
		t.Fatalf("sentinel row count = %d, want 1", count)
	}
}

func TestV3DatabaseRunsHistoricalBridgeAndRecordsBaseline(t *testing.T) {
	db := openMigrationTestDB(t)
	if _, err := db.Exec(embeddedBaselineSQL(t)); err != nil {
		t.Fatalf("create legacy fixture: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE schema_migrations`); err != nil {
		t.Fatalf("remove migration table: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatalf("set legacy schema version: %v", err)
	}

	if err := migrateSchema(db); err != nil {
		t.Fatalf("migrate v3 database: %v", err)
	}
	assertUserVersion(t, db, schemaVersion)
	assertMigrationRecorded(t, db, "0001_baseline.sql")
}

func TestOpeningFutureSchemaVersionReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 7`); err != nil {
		db.Close()
		t.Fatalf("set future schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	if _, err := Open(path); err == nil {
		t.Fatal("Open succeeded for unsupported schema version 7")
	}
}

func openMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func embeddedBaselineSQL(t *testing.T) string {
	t.Helper()
	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	return migrations[0].sql
}

func assertUserVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&got); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if got != want {
		t.Fatalf("schema version = %d, want %d", got, want)
	}
}

func assertMigrationRecorded(t *testing.T, db *sql.DB, filename string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, filename).Scan(&count); err != nil {
		t.Fatalf("read migration record: %v", err)
	}
	if count != 1 {
		t.Fatalf("migration %q record count = %d, want 1", filename, count)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("check table %q: %v", table, err)
	}
	if count != 1 {
		t.Fatalf("table %q does not exist", table)
	}
}

func migrationTableColumns(t *testing.T, db *sql.DB, table string) map[string]struct{} {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("read columns for %q: %v", table, err)
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column for %q: %v", table, err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read columns for %q: %v", table, err)
	}
	return columns
}
