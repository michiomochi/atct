package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	notify *notifier
}

const schemaVersion = 4

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// The daemon is the single writer; limit connections to reduce WAL write contention.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %q: %w", pragma, err)
		}
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if err := migrateSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	return &Store{db: db, notify: newNotifier()}, nil
}

func migrateSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= schemaVersion {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()

	var invalidDecisionCount int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM decisions
		WHERE kind = 'decision'
		  AND status IN ('open', 'answered')
		  AND (task_id IS NULL OR task_id = '')`).Scan(&invalidDecisionCount); err != nil {
		return fmt.Errorf("validate decision task links: %w", err)
	}
	if invalidDecisionCount > 0 {
		return fmt.Errorf("cannot migrate decisions: found %d open/answered decision row(s) with a missing task_id; assign each decision to a task or withdraw it, then retry", invalidDecisionCount)
	}

	filesColumn, err := hasColumn(tx, "tasks", "files")
	if err != nil {
		return fmt.Errorf("inspect tasks columns: %w", err)
	}
	if !filesColumn {
		if _, err := tx.Exec(`ALTER TABLE tasks ADD COLUMN files TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("add tasks.files: %w", err)
		}
	}
	decisionColumns := []struct {
		name       string
		definition string
	}{
		{name: "default_option", definition: `TEXT NOT NULL DEFAULT ''`},
		{name: "default_after_ms", definition: `INTEGER`},
		{name: "default_applied_at", definition: `TEXT`},
	}
	for _, column := range decisionColumns {
		present, err := hasColumn(tx, "decisions", column.name)
		if err != nil {
			return fmt.Errorf("inspect decisions columns: %w", err)
		}
		if present {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE decisions ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
			return fmt.Errorf("add decisions.%s: %w", column.name, err)
		}
	}
	answeredByColumn, err := hasColumn(tx, "decisions", "answered_by")
	if err != nil {
		return fmt.Errorf("inspect decisions.answered_by: %w", err)
	}
	if answeredByColumn {
		if _, err := tx.Exec(`ALTER TABLE decisions DROP COLUMN answered_by`); err != nil {
			return fmt.Errorf("drop decisions.answered_by: %w", err)
		}
	}
	if err := rebuildDecisionsTable(tx); err != nil {
		return fmt.Errorf("rebuild decisions table: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

const decisionsMigrationColumns = `
	id, goal_id, task_id, kind, question, options, status,
	default_option, default_after_ms, default_applied_at,
	answer_label, answer_text, answered_at, applied_at, run_id, created_at`

func rebuildDecisionsTable(tx *sql.Tx) error {
	indexes, err := decisionIndexes(tx)
	if err != nil {
		return fmt.Errorf("read decisions indexes: %w", err)
	}

	if _, err := tx.Exec(`
CREATE TABLE decisions_new (
  id           TEXT PRIMARY KEY,
  goal_id      TEXT NOT NULL REFERENCES goals(id),
  task_id      TEXT REFERENCES tasks(id),
  kind         TEXT NOT NULL,
  question     TEXT NOT NULL,
  options      TEXT NOT NULL DEFAULT '[]',
  status       TEXT NOT NULL,
  default_option TEXT NOT NULL DEFAULT '',
  default_after_ms INTEGER,
  default_applied_at TEXT,
  answer_label TEXT NOT NULL DEFAULT '',
  answer_text  TEXT NOT NULL DEFAULT '',
  answered_at  TEXT,
  applied_at   TEXT,
  run_id       TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  CHECK (kind <> 'decision' OR status NOT IN ('open', 'answered') OR (task_id IS NOT NULL AND task_id <> ''))
)`); err != nil {
		return fmt.Errorf("create decisions_new: %w", err)
	}

	if _, err := tx.Exec(`INSERT INTO decisions_new (` + decisionsMigrationColumns + `) SELECT ` + decisionsMigrationColumns + ` FROM decisions`); err != nil {
		return fmt.Errorf("copy decisions: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE decisions`); err != nil {
		return fmt.Errorf("drop old decisions table: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE decisions_new RENAME TO decisions`); err != nil {
		return fmt.Errorf("rename decisions_new: %w", err)
	}
	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("recreate decisions index: %w", err)
		}
	}
	return nil
}

func decisionIndexes(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`
		SELECT sql FROM sqlite_master
		WHERE type = 'index' AND tbl_name = 'decisions' AND sql IS NOT NULL
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []string
	for rows.Next() {
		var indexSQL string
		if err := rows.Scan(&indexSQL); err != nil {
			return nil, err
		}
		indexes = append(indexes, indexSQL)
	}
	return indexes, rows.Err()
}

func hasColumn(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }
