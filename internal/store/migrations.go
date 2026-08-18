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
		"run_id",
		"created_at",
	},
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
			if err := validateV6Schema(ctx, conn); err != nil {
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
	tables := make([]string, 0, len(requiredV6Columns))
	for tableName := range requiredV6Columns {
		tables = append(tables, tableName)
	}
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
  AND name IN ('projects', 'runs', 'goals', 'tasks', 'decisions')
LIMIT 1`).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check legacy schema tables: %w", err)
	}
	return true, nil
}

func validateV6Schema(ctx context.Context, conn *sql.Conn) error {
	tables := make([]string, 0, len(requiredV6Columns))
	for tableName := range requiredV6Columns {
		tables = append(tables, tableName)
	}
	sort.Strings(tables)
	for _, tableName := range tables {
		columns, err := tableColumns(ctx, conn, tableName)
		if err != nil {
			return err
		}
		missing := make([]string, 0)
		for _, columnName := range requiredV6Columns[tableName] {
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
