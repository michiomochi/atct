package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigratesClaimColumnsOnlyForExistingSessions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "claim-migration.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	raw.SetMaxOpenConns(1)

	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		raw.Close()
		t.Fatalf("load embedded migrations: %v", err)
	}
	for _, migration := range migrations {
		if migration.filename == "0016_drop_claim_columns.sql" {
			break
		}
		if _, err := raw.Exec(migration.sql); err != nil {
			raw.Close()
			t.Fatalf("apply fixture migration %s: %v", migration.filename, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, migration.filename, "2026-08-24T00:00:00Z"); err != nil {
			raw.Close()
			t.Fatalf("record fixture migration %s: %v", migration.filename, err)
		}
	}
	if _, err := raw.Exec(`PRAGMA user_version = 6`); err != nil {
		raw.Close()
		t.Fatalf("set fixture schema version: %v", err)
	}
	if _, err := raw.Exec(`
INSERT INTO projects (id, name, root_path, created_at)
VALUES
	('live-project', 'live project', '/live-project', '2026-08-24T00:00:00Z'),
	('ghost-project', 'ghost project', '/ghost-project', '2026-08-24T00:00:00Z');
INSERT INTO agent_sessions (id, project_id, registered_at)
VALUES ('live-session', 'live-project', '2026-08-24T00:01:00Z');
INSERT INTO goals (id, project_id, content, status, claimed_by, claimed_at, created_at, updated_at)
VALUES
	('live-goal', 'live-project', 'live goal', 'active', 'live-session', '2026-08-24T00:10:00Z', '2026-08-24T00:00:00Z', '2026-08-24T00:10:00Z'),
	('ghost-goal', 'ghost-project', 'ghost goal', 'active', 'ghost-session', '2026-08-24T00:11:00Z', '2026-08-24T00:00:00Z', '2026-08-24T00:11:00Z');
INSERT INTO tasks (id, goal_id, title, status, declare_key, claimed_by, claimed_at, created_at, updated_at)
VALUES
	('live-task', 'live-goal', 'live task', 'todo', 'live-task-key', 'live-session', '2026-08-24T00:20:00Z', '2026-08-24T00:00:00Z', '2026-08-24T00:20:00Z'),
	('ghost-task', 'ghost-goal', 'ghost task', 'todo', 'ghost-task-key', 'ghost-session', '2026-08-24T00:21:00Z', '2026-08-24T00:00:00Z', '2026-08-24T00:21:00Z');
`); err != nil {
		raw.Close()
		t.Fatalf("insert claim fixtures: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	migrated, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open pre-claim-columns database: %v", err)
	}
	defer migrated.Close()

	assertLiveHandoff := func(table, entityColumn, entityID, claimedAt string) {
		t.Helper()
		var count int
		countQuery := `SELECT COUNT(*) FROM ` + table + ` WHERE ` + entityColumn + ` = ?`
		if err := migrated.DB().QueryRow(countQuery, entityID).Scan(&count); err != nil {
			t.Fatalf("count %s rows: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s rows for %s = %d, want 1", table, entityID, count)
		}

		var requestedBy, receivedBy, receivedAt string
		rowQuery := `SELECT requested_by, received_by, received_at FROM ` + table + ` WHERE ` + entityColumn + ` = ?`
		if err := migrated.DB().QueryRow(rowQuery, entityID).Scan(&requestedBy, &receivedBy, &receivedAt); err != nil {
			t.Fatalf("read %s row: %v", table, err)
		}
		if requestedBy != "live-session" || receivedBy != "live-session" {
			t.Errorf("%s session IDs = (%q, %q), want (live-session, live-session)", table, requestedBy, receivedBy)
		}
		if receivedAt != claimedAt {
			t.Errorf("%s received_at = %q, want %q", table, receivedAt, claimedAt)
		}
	}
	assertLiveHandoff("task_handoffs", "task_id", "live-task", "2026-08-24T00:20:00Z")
	assertLiveHandoff("goal_handoffs", "goal_id", "live-goal", "2026-08-24T00:10:00Z")

	assertNoHandoff := func(table, entityColumn, entityID string) {
		t.Helper()
		var count int
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE ` + entityColumn + ` = ?`
		if err := migrated.DB().QueryRow(query, entityID).Scan(&count); err != nil {
			t.Fatalf("count %s rows: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s rows for %s = %d, want 0", table, entityID, count)
		}
	}
	assertNoHandoff("task_handoffs", "task_id", "ghost-task")
	assertNoHandoff("goal_handoffs", "goal_id", "ghost-goal")

	for _, table := range []string{"tasks", "goals"} {
		rows, err := migrated.DB().Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatalf("inspect %s columns: %v", table, err)
		}
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				t.Fatalf("scan %s column: %v", table, err)
			}
			if name == "claimed_by" || name == "claimed_at" {
				t.Errorf("%s still has removed column %q", table, name)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("iterate %s columns: %v", table, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close %s column rows: %v", table, err)
		}
	}
}
