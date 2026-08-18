package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	notify *notifier
}

const schemaVersion = 6

const runRetention = 30 * 24 * time.Hour

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

func migrateSchema(db *sql.DB) (err error) {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= schemaVersion {
		return nil
	}

	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read foreign_keys pragma: %w", err)
	}
	if foreignKeys != 0 {
		if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			return fmt.Errorf("disable foreign_keys for migration: %w", err)
		}
		defer func() {
			if _, restoreErr := db.Exec(fmt.Sprintf(`PRAGMA foreign_keys=%d`, foreignKeys)); restoreErr != nil && err == nil {
				err = fmt.Errorf("restore foreign_keys pragma: %w", restoreErr)
			}
		}()
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()

	if version < 4 {
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
	}
	if version < 6 {
		if err := migrateGoalsTableV6(tx); err != nil {
			return fmt.Errorf("migrate goals table: %w", err)
		}
	}
	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS runs (
  id            TEXT PRIMARY KEY,
  project_id    TEXT REFERENCES projects(id),
  registered_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create runs table: %w", err)
	}
	if _, err := tx.Exec(`
CREATE INDEX IF NOT EXISTS idx_runs_project_registered_at
  ON runs(project_id, registered_at DESC)`); err != nil {
		return fmt.Errorf("create runs index: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func migrateGoalsTableV6(tx *sql.Tx) error {
	resultSummaryColumn, err := hasColumn(tx, "goals", "result_summary")
	if err != nil {
		return fmt.Errorf("inspect goals.result_summary: %w", err)
	}
	completionExpressions := make(map[string]string, 5)
	for _, column := range []string{
		"now_possible", "how_to_verify", "surprises", "needs_review", "next_steps",
	} {
		expression, err := completionValueExpr(tx, column)
		if err != nil {
			return fmt.Errorf("inspect goals.%s: %w", column, err)
		}
		completionExpressions[column] = expression
	}

	indexes, err := goalIndexes(tx)
	if err != nil {
		return fmt.Errorf("read goals indexes: %w", err)
	}

	if _, err := tx.Exec(fmt.Sprintf(`
CREATE TABLE goals_new (
  id           TEXT PRIMARY KEY,
  project_id   TEXT NOT NULL REFERENCES projects(id),
  title        TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL,
  work_done    TEXT NOT NULL DEFAULT '',
  now_possible TEXT NOT NULL DEFAULT '',
  how_to_verify TEXT NOT NULL DEFAULT '',
  surprises    TEXT NOT NULL DEFAULT '',
  needs_review TEXT NOT NULL DEFAULT '',
  next_steps   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  %s
)`, completionReportCheckSQL())); err != nil {
		return fmt.Errorf("create goals_new: %w", err)
	}

	workDoneExpr := `'なし'`
	if resultSummaryColumn {
		workDoneExpr = completionValueExprForColumn("result_summary")
	} else {
		workDoneExpr, err = completionValueExpr(tx, "work_done")
		if err != nil {
			return fmt.Errorf("inspect goals.work_done: %w", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`
INSERT INTO goals_new (
  id, project_id, title, description, status,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
)
SELECT id, project_id, title, description, status,
  %s, %s, %s, %s, %s, %s,
  created_at, updated_at
FROM goals`, workDoneExpr,
		completionExpressions["now_possible"],
		completionExpressions["how_to_verify"],
		completionExpressions["surprises"],
		completionExpressions["needs_review"],
		completionExpressions["next_steps"])); err != nil {
		return fmt.Errorf("copy goals: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE goals`); err != nil {
		return fmt.Errorf("drop old goals table: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE goals_new RENAME TO goals`); err != nil {
		return fmt.Errorf("rename goals_new: %w", err)
	}
	for _, indexSQL := range indexes {
		if _, err := tx.Exec(indexSQL); err != nil {
			return fmt.Errorf("recreate goals index: %w", err)
		}
	}
	return nil
}

func completionValueExpr(tx *sql.Tx, column string) (string, error) {
	present, err := hasColumn(tx, "goals", column)
	if err != nil {
		return "", err
	}
	if !present {
		return `'なし'`, nil
	}
	return completionValueExprForColumn(column), nil
}

func completionValueExprForColumn(column string) string {
	return fmt.Sprintf("CASE WHEN length(trim(%s)) > 0 THEN %s ELSE 'なし' END", column, column)
}

func goalIndexes(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`
		SELECT sql FROM sqlite_master
		WHERE type = 'index' AND tbl_name = 'goals' AND sql IS NOT NULL
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

func requireRunID(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "", fmt.Errorf("run_id is required")
	}
	return runID, nil
}

func (s *Store) RegisterRun(ctx context.Context, runID string) error {
	runID, err := requireRunID(runID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	registeredAt := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin run registration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO runs (id, project_id, registered_at)
		VALUES (?, NULL, ?)`, runID, registeredAt); err != nil {
		return fmt.Errorf("register run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM runs
		WHERE registered_at < ?`, now.Add(-runRetention).Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("clean up old runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit run registration: %w", err)
	}
	return nil
}

func (s *Store) AssociateRunWithProject(ctx context.Context, runID, projectID string) error {
	runID, err := requireRunID(runID)
	if err != nil {
		return err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return fmt.Errorf("project_id is required")
	}

	now := time.Now().UTC()
	registeredAt := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin run association: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE runs SET project_id = ?
		WHERE id = ?`, projectID, runID)
	if err != nil {
		return fmt.Errorf("associate run with project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect run association: %w", err)
	}
	if affected == 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO runs (id, project_id, registered_at)
			VALUES (?, ?, ?)`, runID, projectID, registeredAt); err != nil {
			return fmt.Errorf("insert associated run: %w", err)
		}
	}

	var currentRegisteredAt string
	if err := tx.QueryRowContext(ctx, `
		SELECT registered_at FROM runs WHERE id = ?`, runID).Scan(&currentRegisteredAt); err != nil {
		return fmt.Errorf("read associated run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM runs
		WHERE id <> ? AND registered_at < ?`, runID, now.Add(-runRetention).Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("clean up old runs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM runs
		WHERE project_id = ? AND id <> ? AND registered_at < ?`, projectID, runID, currentRegisteredAt); err != nil {
		return fmt.Errorf("clean up old project runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit run association: %w", err)
	}
	return nil
}

func (s *Store) LatestRunID(ctx context.Context, projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", fmt.Errorf("project_id is required")
	}

	var runID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM runs
		WHERE project_id = ?
		ORDER BY registered_at DESC, id DESC
		LIMIT 1`, projectID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find latest run: %w", err)
	}
	return runID, nil
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
