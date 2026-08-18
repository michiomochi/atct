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
	assertMigrationRecorded(t, db, "0001_baseline.sql")
	for _, table := range []string{"projects", "runs", "goals", "tasks", "decisions", "schema_migrations"} {
		assertTableExists(t, db, table)
	}
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
