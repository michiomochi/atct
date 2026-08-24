package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var migrationFilenamePattern = regexp.MustCompile(`^[0-9]{4}_[A-Za-z0-9][A-Za-z0-9_-]*\.sql$`)

const schemaMigrationsTable = "schema_migrations"

type embeddedMigration struct {
	filename string
	number   int
	sql      string
}

type migrationState struct {
	userVersion        int
	hasMigrationsTable bool
	applied            map[string]string
}

var requiredV6Columns = map[string][]string{
	"projects": {
		"id",
		"name",
		"root_path",
		"created_at",
	},
	"runs": {
		"id",
		"project_id",
		"registered_at",
	},
	"goals": {
		"id",
		"project_id",
		"title",
		"description",
		"status",
		"result_summary",
		"work_done",
		"now_possible",
		"how_to_verify",
		"surprises",
		"needs_review",
		"next_steps",
		"created_at",
		"updated_at",
	},
	"tasks": {
		"id",
		"goal_id",
		"title",
		"status",
		"agent",
		"files",
		"sort_order",
		"declare_key",
		"claimed_by",
		"claimed_at",
		"created_at",
		"updated_at",
	},
	"decisions": {
		"id",
		"goal_id",
		"task_id",
		"kind",
		"question",
		"options",
		"status",
		"default_option",
		"default_after_ms",
		"default_applied_at",
		"answer_label",
		"answer_text",
		"answered_at",
		"applied_at",
		"run" + "_id",
		"created_at",
	},
}

var requiredCurrentV6Columns = map[string][]string{
	"projects": {
		"id",
		"name",
		"root_path",
		"created_at",
	},
	"agent_sessions": {
		"id",
		"project_id",
		"registered_at",
		"pid",
		"started_at",
	},
	"goals": {
		"id",
		"project_id",
		"content",
		"status",
		"result_summary",
		"work_done",
		"now_possible",
		"how_to_verify",
		"surprises",
		"needs_review",
		"next_steps",
		"created_at",
		"updated_at",
	},
	"tasks": {
		"id",
		"goal_id",
		"title",
		"status",
		"agent",
		"files",
		"sort_order",
		"declare_key",
		"claimed_by",
		"claimed_at",
		"created_at",
		"updated_at",
	},
	"decisions": {
		"id",
		"goal_id",
		"task_id",
		"kind",
		"question",
		"options",
		"status",
		"default_option",
		"default_after_ms",
		"default_applied_at",
		"answer_label",
		"answer_text",
		"answered_at",
		"applied_at",
		"agent_session_id",
		"created_at",
	},
}

func configureDatabase(db *sql.DB) error {
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("exec %q: %w", pragma, err)
		}
	}
	return nil
}

func migrateSchema(db *sql.DB) (err error) {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if version < 0 {
		return fmt.Errorf("database has invalid schema version %d", version)
	}
	if version == schemaVersion {
		return applyEmbeddedMigrations(db)
	}
	if version == 0 {
		hasTargetTables, err := hasAnyTargetSchemaTableDB(db)
		if err != nil {
			return err
		}
		if !hasTargetTables {
			return applyEmbeddedMigrations(db)
		}
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

	legacyMigrations, err := loadEmbeddedMigrations()
	if err != nil {
		return fmt.Errorf("load legacy schema bootstrap: %w", err)
	}
	if _, err := tx.Exec(legacyMigrations[0].sql); err != nil {
		return fmt.Errorf("bootstrap legacy schema: %w", err)
	}
	if len(legacyMigrations) < 4 {
		return fmt.Errorf("legacy schema bootstrap has %d migrations, want at least 4", len(legacyMigrations))
	}
	if _, err := tx.Exec(legacyMigrations[3].sql); err != nil {
		return fmt.Errorf("migrate agent sessions: %w", err)
	}

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
	for _, migration := range legacyMigrations[1:3] {
		if _, err := tx.Exec(migration.sql); err != nil {
			return fmt.Errorf("apply %s during historical bridge: %w", migration.filename, err)
		}
	}
	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
  filename TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	for _, migration := range legacyMigrations[:4] {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO schema_migrations(filename, applied_at) VALUES (?, ?)`,
			migration.filename,
			time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("record %s during historical bridge: %w", migration.filename, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return applyEmbeddedMigrations(db)
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
  result_summary TEXT NOT NULL DEFAULT '',
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

	resultSummaryExpr := `''`
	if resultSummaryColumn {
		resultSummaryExpr = `result_summary`
	}
	workDoneExpr := completionPlaceholderExpr
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
  result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
)
SELECT id, project_id, title, description, status,
  %s, %s, %s, %s, %s, %s, %s,
  created_at, updated_at
FROM goals`, resultSummaryExpr, workDoneExpr,
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

const completionPlaceholderExpr = `CASE WHEN status = 'done' THEN 'なし' ELSE '' END`

func completionValueExpr(tx *sql.Tx, column string) (string, error) {
	present, err := hasColumn(tx, "goals", column)
	if err != nil {
		return "", err
	}
	if !present {
		return completionPlaceholderExpr, nil
	}
	return completionValueExprForColumn(column), nil
}

func completionValueExprForColumn(column string) string {
	return fmt.Sprintf("CASE WHEN status = 'done' AND length(trim(%s)) > 0 THEN %s WHEN status = 'done' THEN 'なし' ELSE '' END", column, column)
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

const decisionsMigrationColumns = `
	id, goal_id, task_id, kind, question, options, status,
	default_option, default_after_ms, default_applied_at,
	answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at`

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
  agent_session_id TEXT NOT NULL DEFAULT '',
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

func loadEmbeddedMigrations() ([]embeddedMigration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	migrations := make([]embeddedMigration, 0, len(entries))
	for i, entry := range entries {
		filename := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(filename, ".sql") || !migrationFilenamePattern.MatchString(filename) {
			return nil, fmt.Errorf("invalid embedded migration filename %q", filename)
		}
		number, err := strconv.Atoi(filename[:4])
		if err != nil {
			return nil, fmt.Errorf("parse embedded migration filename %q: %w", filename, err)
		}
		if number != i+1 {
			return nil, fmt.Errorf("embedded migrations are not a linear sequence at %q: expected %04d", filename, i+1)
		}
		contents, err := fs.ReadFile(migrationFS, "migrations/"+filename)
		if err != nil {
			return nil, fmt.Errorf("read embedded migration %q: %w", filename, err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return nil, fmt.Errorf("embedded migration %q is empty", filename)
		}
		migrations = append(migrations, embeddedMigration{
			filename: filename,
			number:   number,
			sql:      string(contents),
		})
	}
	if len(migrations) == 0 || migrations[0].filename != "0001_baseline.sql" {
		return nil, fmt.Errorf("embedded migrations must start with 0001_baseline.sql")
	}
	return migrations, nil
}

func applyEmbeddedMigrations(db *sql.DB) error {
	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		return err
	}
	ctx := context.Background()

	err = withImmediateMigrationTx(ctx, db, func(conn *sql.Conn) error {
		state, err := readMigrationState(ctx, conn)
		if err != nil {
			return err
		}
		if state.userVersion > schemaVersion {
			return fmt.Errorf("database schema version %d is newer than supported version %d", state.userVersion, schemaVersion)
		}
		if state.userVersion < 0 {
			return fmt.Errorf("database has invalid schema version %d", state.userVersion)
		}

		switch state.userVersion {
		case 0:
			hasTargetTables, err := hasAnyTargetSchemaTable(ctx, conn)
			if err != nil {
				return err
			}
			if hasTargetTables {
				return fmt.Errorf("database has target tables but user_version is 0; historical migration is required")
			}
			if err := validateAppliedMigrations(state, migrations); err != nil {
				return err
			}
			if len(state.applied) != 0 {
				return fmt.Errorf("empty database already has schema migration records")
			}
			if err := execEmbeddedMigration(ctx, conn, migrations[0]); err != nil {
				return err
			}
			if err := ensureSchemaMigrationsTable(ctx, conn); err != nil {
				return err
			}
			if err := recordMigration(ctx, conn, migrations[0].filename); err != nil {
				return err
			}
			return setUserVersion(ctx, conn)

		case schemaVersion:
			if err := validateV6Schema(ctx, conn, state); err != nil {
				return err
			}
			if err := ensureSchemaMigrationsTable(ctx, conn); err != nil {
				return err
			}
			if err := validateAppliedMigrations(state, migrations); err != nil {
				return err
			}
			if _, ok := state.applied[migrations[0].filename]; !ok {
				if err := recordMigration(ctx, conn, migrations[0].filename); err != nil {
					return err
				}
			}
			return nil

		default:
			return fmt.Errorf("database schema version %d requires the historical migration bridge", state.userVersion)
		}
	})
	if err != nil {
		return err
	}

	for i := 1; i < len(migrations); i++ {
		migration := migrations[i]
		migrationIndex := i
		if err := withImmediateMigrationTx(ctx, db, func(conn *sql.Conn) error {
			state, err := readMigrationState(ctx, conn)
			if err != nil {
				return err
			}
			if state.userVersion != schemaVersion {
				return fmt.Errorf("database schema version changed to %d while applying %s", state.userVersion, migration.filename)
			}
			if !state.hasMigrationsTable {
				return fmt.Errorf("%s is missing %s", migration.filename, schemaMigrationsTable)
			}
			if err := validateAppliedMigrations(state, migrations); err != nil {
				return err
			}
			if _, ok := state.applied[migration.filename]; ok {
				return nil
			}
			for _, previous := range migrations[:migrationIndex] {
				if _, ok := state.applied[previous.filename]; !ok {
					return fmt.Errorf("cannot apply %s before %s", migration.filename, previous.filename)
				}
			}
			if err := execEmbeddedMigration(ctx, conn, migration); err != nil {
				return err
			}
			if err := recordMigration(ctx, conn, migration.filename); err != nil {
				return err
			}
			return setUserVersion(ctx, conn)
		}); err != nil {
			return err
		}
	}
	return nil
}

func withImmediateMigrationTx(ctx context.Context, db *sql.DB, fn func(*sql.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin migration connection: %w", err)
	}
	defer conn.Close()

	// foreign_keys is per-connection and a no-op inside a transaction, so it has
	// to be turned off on this pinned connection before BEGIN. A migration that
	// rebuilds a referenced table -- 0007 rebuilds goals -- fails with FOREIGN KEY
	// constraint failed without this, and only against a database that actually
	// has rows pointing at it.
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read foreign_keys pragma: %w", err)
	}
	if foreignKeys != 0 {
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return fmt.Errorf("disable foreign_keys for migration: %w", err)
		}
		defer func() {
			_, _ = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA foreign_keys=%d", foreignKeys))
		}()
	}

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}
	committed = true
	return nil
}

func readMigrationState(ctx context.Context, conn *sql.Conn) (migrationState, error) {
	var state migrationState
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&state.userVersion); err != nil {
		return migrationState{}, fmt.Errorf("read database schema version: %w", err)
	}
	hasTable, err := tableExists(ctx, conn, schemaMigrationsTable)
	if err != nil {
		return migrationState{}, err
	}
	state.hasMigrationsTable = hasTable
	state.applied = make(map[string]string)
	if !hasTable {
		return state, nil
	}

	rows, err := conn.QueryContext(ctx, "SELECT filename, applied_at FROM schema_migrations")
	if err != nil {
		return migrationState{}, fmt.Errorf("read schema migration records: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var filename, appliedAt string
		if err := rows.Scan(&filename, &appliedAt); err != nil {
			return migrationState{}, fmt.Errorf("scan schema migration record: %w", err)
		}
		if filename == "" || appliedAt == "" {
			return migrationState{}, fmt.Errorf("schema migration record has an empty filename or applied_at")
		}
		if _, exists := state.applied[filename]; exists {
			return migrationState{}, fmt.Errorf("schema migration %q is recorded more than once", filename)
		}
		state.applied[filename] = appliedAt
	}
	if err := rows.Err(); err != nil {
		return migrationState{}, fmt.Errorf("read schema migration records: %w", err)
	}
	return state, nil
}

func tableExists(ctx context.Context, conn *sql.Conn, tableName string) (bool, error) {
	var one int
	err := conn.QueryRowContext(ctx,
		"SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1",
		tableName,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check table %q: %w", tableName, err)
	}
	return true, nil
}

func hasAnyTargetSchemaTable(ctx context.Context, conn *sql.Conn) (bool, error) {
	tables := make([]string, 0, len(requiredV6Columns)+1)
	for tableName := range requiredV6Columns {
		tables = append(tables, tableName)
	}
	tables = append(tables, "agent_sessions")
	sort.Strings(tables)
	for _, tableName := range tables {
		exists, err := tableExists(ctx, conn, tableName)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

func hasAnyTargetSchemaTableDB(db *sql.DB) (bool, error) {
	var one int
	err := db.QueryRow(`
SELECT 1
FROM sqlite_master
WHERE type = 'table'
		AND name IN ('projects', 'runs', 'agent_sessions', 'goals', 'tasks', 'decisions')
LIMIT 1`).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check legacy schema tables: %w", err)
	}
	return true, nil
}

// withPreContentGoalColumns swaps the goals expectation back to title and
// description, for a database that has not reached 0007_goal_content.sql yet.
func withPreContentGoalColumns(required map[string][]string) map[string][]string {
	out := make(map[string][]string, len(required))
	for table, columns := range required {
		if table != "goals" {
			out[table] = columns
			continue
		}
		swapped := make([]string, 0, len(columns)+1)
		for _, column := range columns {
			if column == "content" {
				swapped = append(swapped, "title", "description")
				continue
			}
			swapped = append(swapped, column)
		}
		out[table] = swapped
	}
	return out
}

func validateV6Schema(ctx context.Context, conn *sql.Conn, state migrationState) error {
	// This runs before the pending migrations are applied, so the expectation has
	// to match the database as it stands, not as it will look afterwards.
	requiredColumns := requiredV6Columns
	if _, ok := state.applied["0004_agent_sessions.sql"]; ok {
		requiredColumns = requiredCurrentV6Columns
	}
	if _, ok := state.applied["0016_drop_claim_columns.sql"]; ok {
		requiredColumns = withoutTaskAndGoalClaimColumns(requiredColumns)
	}
	if _, ok := state.applied["0007_goal_content.sql"]; !ok {
		requiredColumns = withPreContentGoalColumns(requiredColumns)
	}

	tables := make([]string, 0, len(requiredColumns))
	for tableName := range requiredColumns {
		tables = append(tables, tableName)
	}
	sort.Strings(tables)
	for _, tableName := range tables {
		columns, err := tableColumns(ctx, conn, tableName)
		if err != nil {
			return err
		}
		missing := make([]string, 0)
		for _, columnName := range requiredColumns[tableName] {
			if !columns[columnName] {
				missing = append(missing, columnName)
			}
		}
		if len(missing) != 0 {
			return fmt.Errorf("table %q is missing v6 columns: %s", tableName, strings.Join(missing, ", "))
		}
	}
	return nil
}

func withoutTaskAndGoalClaimColumns(required map[string][]string) map[string][]string {
	withoutClaims := make(map[string][]string, len(required))
	for table, columns := range required {
		filtered := make([]string, 0, len(columns))
		for _, column := range columns {
			if (table == "tasks" || table == "goals") && (column == "claimed_by" || column == "claimed_at") {
				continue
			}
			filtered = append(filtered, column)
		}
		withoutClaims[table] = filtered
	}
	return withoutClaims
}

func tableColumns(ctx context.Context, conn *sql.Conn, tableName string) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteString(tableName)+")")
	if err != nil {
		return nil, fmt.Errorf("read columns for table %q: %w", tableName, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("scan columns for table %q: %w", tableName, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read columns for table %q: %w", tableName, err)
	}
	return columns, nil
}

func quoteSQLiteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func ensureSchemaMigrationsTable(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  filename TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
);`); err != nil {
		return fmt.Errorf("create %s: %w", schemaMigrationsTable, err)
	}
	columns, err := tableColumns(ctx, conn, schemaMigrationsTable)
	if err != nil {
		return err
	}
	for _, columnName := range []string{"filename", "applied_at"} {
		if !columns[columnName] {
			return fmt.Errorf("table %q is missing column %q", schemaMigrationsTable, columnName)
		}
	}
	return nil
}

func validateAppliedMigrations(state migrationState, migrations []embeddedMigration) error {
	known := make(map[string]struct{}, len(migrations))
	for _, migration := range migrations {
		known[migration.filename] = struct{}{}
	}
	for filename := range state.applied {
		if _, ok := known[filename]; !ok {
			return fmt.Errorf("database records unknown schema migration %q", filename)
		}
	}

	missingPrevious := false
	for _, migration := range migrations {
		_, applied := state.applied[migration.filename]
		if !applied {
			missingPrevious = true
			continue
		}
		if missingPrevious {
			return fmt.Errorf("schema migration %q is recorded after an unapplied migration", migration.filename)
		}
	}
	return nil
}

func execEmbeddedMigration(ctx context.Context, conn *sql.Conn, migration embeddedMigration) error {
	if _, err := conn.ExecContext(ctx, migration.sql); err != nil {
		return fmt.Errorf("execute schema migration %s: %w", migration.filename, err)
	}
	return nil
}

func recordMigration(ctx context.Context, conn *sql.Conn, filename string) error {
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)",
		filename,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record schema migration %s: %w", filename, err)
	}
	return nil
}

func setUserVersion(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set database schema version to %d: %w", schemaVersion, err)
	}
	return nil
}
