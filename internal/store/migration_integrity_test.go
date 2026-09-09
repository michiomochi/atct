package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestMigrationIntegrityVerifierRejectsOrphan(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "orphan.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) = %v", dbPath, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		db.Close()
		t.Fatalf("disable foreign keys for orphan fixture: %v", err)
	}
	if err := insertFixtureRow(db, "tasks", map[string]any{
		"id":          fixtureIDValue(t, db, "tasks", "orphan-task", int64(1)),
		"legacy_id":   "orphan-task",
		"goal_id":     fixtureIDValue(t, db, "goals", "missing-goal", int64(999)),
		"title":       "orphan task",
		"status":      "todo",
		"declare_key": "orphan-key",
		"created_at":  "2026-08-26T00:00:00Z",
		"updated_at":  "2026-08-26T00:00:00Z",
	}); err != nil {
		db.Close()
		t.Fatalf("create orphan fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close orphan fixture: %v", err)
	}

	_, err = verifyMigrationIntegrity(dbPath)
	if err == nil {
		t.Fatal("verifyMigrationIntegrity succeeded for an orphan task")
	}
	if !strings.Contains(err.Error(), "foreign key violations") {
		t.Fatalf("verifyMigrationIntegrity error = %q, want foreign key violation", err)
	}
}

func TestMigrationIntegrityVerifierPreservesFixtureRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fixture.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) = %v", dbPath, err)
	}
	if err := insertFixtureRow(s.DB(), "projects", map[string]any{
		"id":         fixtureIDValue(t, s.DB(), "projects", "fixture-project", int64(1)),
		"legacy_id":  "fixture-project",
		"name":       "fixture",
		"root_path":  "/fixture",
		"created_at": "2026-08-26T00:00:00Z",
	}); err != nil {
		s.Close()
		t.Fatalf("insert fixture project: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}

	report, err := verifyMigrationIntegrity(dbPath)
	if err != nil {
		t.Fatalf("verifyMigrationIntegrity(%q) = %v", dbPath, err)
	}
	t.Logf("migration integrity report: %s", report)
}

func TestGoal254MigrationPreservesCanonicalRowsAndRemovesOrchestrationState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "goal-254.db")
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
		if migration.filename == "0036_remove_legacy_orchestration.sql" {
			break
		}
		if _, err := raw.Exec(migration.sql); err != nil {
			raw.Close()
			t.Fatalf("apply fixture migration %s: %v", migration.filename, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, migration.filename, "2026-09-09T00:00:00Z"); err != nil {
			raw.Close()
			t.Fatalf("record fixture migration %s: %v", migration.filename, err)
		}
	}
	if _, err := raw.Exec(`
INSERT INTO projects (id, name, root_path, created_at)
VALUES (1, 'goal 254 fixture', '/goal-254', '2026-09-09T00:00:00Z');
INSERT INTO agent_sessions (id, project_id, session_key, registered_at, pid, started_at)
VALUES (1, 1, 'goal-254-requester', '2026-09-09T00:00:00Z', 1, '2026-09-09T00:00:00Z'),
       (2, 1, 'goal-254-receiver', '2026-09-09T00:00:00Z', 2, '2026-09-09T00:00:00Z');
INSERT INTO goals (id, project_id, content, status, created_at, updated_at)
VALUES (1, 1, 'migrate handoff lifecycle', 'active', '2026-09-09T00:00:00Z', '2026-09-09T00:00:00Z');
INSERT INTO tasks (id, goal_id, title, status, declare_key, created_at, updated_at)
VALUES (1, 1, 'preserve canonical rows', 'todo', 'goal-254-fixture-task', '2026-09-09T00:00:00Z', '2026-09-09T00:00:00Z');
INSERT INTO task_handoffs (id, task_id, requested_by, received_by, requested_at, received_at)
VALUES ('goal-254-task-handoff', 1, 1, 2, '2026-09-09T00:00:00Z', '2026-09-09T00:01:00Z');
INSERT INTO goal_handoffs (id, goal_id, requested_by, received_by, requested_at, received_at)
VALUES ('goal-254-goal-handoff', 1, 1, 2, '2026-09-09T00:00:00Z', '2026-09-09T00:01:00Z');
INSERT INTO plan_handoffs (id, goal_id, review_requested_by, review_requested_at, review_request_report)
VALUES ('goal-254-plan-handoff', 1, 2, '2026-09-09T00:02:00Z', 'approved plan');
INSERT INTO task_create_handoffs (id, plan_handoff_id, goal_id, requested_by, received_by, completed_by, requested_at, received_at, completed_at, request_report, complete_report)
VALUES ('goal-254-task-create', 'goal-254-plan-handoff', 1, 1, 2, 2, '2026-09-09T00:02:00Z', '2026-09-09T00:03:00Z', '2026-09-09T00:04:00Z', 'create tasks', 'created tasks');
INSERT INTO task_create_handoff_tasks (handoff_id, task_id)
VALUES ('goal-254-task-create', 1);
INSERT INTO decisions (id, goal_id, task_id, kind, question, options, status, agent_session_id, created_at)
VALUES (1, 1, 1, 'implementation', 'keep this decision?', '[{"label":"yes"}]', 'open', 2, '2026-09-09T00:00:00Z');
INSERT INTO orchestration_scope (scope_key, project_id, goal_id, task_id, role, agent_session_id, source_generation, created_at, updated_at)
VALUES ('task:1:goal-254-task-handoff', 1, 1, 1, 'executor', 2, '2026-09-09T00:01:00Z', '2026-09-09T00:01:00Z', '2026-09-09T00:01:00Z');
INSERT INTO orchestration_delivery_leases (scope_key, target_role, holder_monitor_id, fencing_token, expires_at, updated_at)
VALUES ('task:1:goal-254-task-handoff', 'executor', 'goal-254-monitor', 1, '2026-09-09T00:02:00Z', '2026-09-09T00:01:00Z');
INSERT INTO orchestration_delivery_receipts (scope_key, delivery_key, generation, target_role, holder_monitor_id, fencing_token, status, created_at, updated_at)
VALUES ('task:1:goal-254-task-handoff', 'review', '2026-09-09T00:01:00Z', 'executor', 'goal-254-monitor', 1, 'accepted', '2026-09-09T00:01:00Z', '2026-09-09T00:01:00Z');
INSERT INTO orchestration_blockers (blocker_id, project_id, goal_id, task_id, scope_key, kind, source_id, generation, owner_role, instruction, opened_at)
VALUES ('goal-254-blocker', 1, 1, 1, 'task:1:goal-254-task-handoff', 'human_decision', 'decision:1', '2026-09-09T00:01:00Z', 'subcommander', 'wait', '2026-09-09T00:01:00Z');
INSERT INTO orchestration_review_work (review_work_id, project_id, goal_id, task_id, kind, handoff_id, requester_session_id, requester_scope_key, expected_reviewer_role, reviewer_scope_key, state, review_requested_generation, active, opened_at, updated_at)
VALUES ('goal-254-review-work', 1, 1, 1, 'task', 'goal-254-task-handoff', 2, 'task:1:goal-254-task-handoff', 'subcommander', 'goal:1:goal-254-goal-handoff', 'requested', '2026-09-09T00:01:00Z', 1, '2026-09-09T00:01:00Z', '2026-09-09T00:01:00Z');
`); err != nil {
		raw.Close()
		t.Fatalf("insert goal 254 migration fixture: %v", err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 6`); err != nil {
		raw.Close()
		t.Fatalf("set fixture schema version: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	migrated, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated fixture: %v", err)
	}
	defer migrated.Close()

	assertMigrationRecorded(t, migrated.DB(), "0035_handoff_only_lifecycle.sql")
	assertMigrationRecorded(t, migrated.DB(), "0036_remove_legacy_orchestration.sql")
	assertMigrationRecorded(t, migrated.DB(), "0037_simplify_task_create_handoff.sql")
	assertTableExists(t, migrated.DB(), "task_create_handoffs")
	assertTableAbsent(t, migrated.DB(), "task_create_handoff_tasks")
	taskCreateColumns := migrationTableColumns(t, migrated.DB(), "task_create_handoffs")
	if _, ok := taskCreateColumns["plan_handoff_id"]; ok {
		t.Error("task_create_handoffs still has plan_handoff_id")
	}
	var taskCreateGoalID int64
	if err := migrated.DB().QueryRow(`SELECT goal_id FROM task_create_handoffs WHERE id = ?`, "goal-254-task-create").Scan(&taskCreateGoalID); err != nil || taskCreateGoalID != 1 {
		t.Fatalf("preserved task-create handoff goal = %d, %v; want 1", taskCreateGoalID, err)
	}
	if _, err := migrated.DB().Exec(`INSERT INTO task_create_handoffs (id, goal_id) VALUES (?, ?)`, "duplicate-goal", 1); err == nil {
		t.Error("task_create_handoffs accepted a duplicate goal_id")
	}
	for _, table := range []string{
		"orchestration_scope", "orchestration_delivery_leases",
		"orchestration_delivery_receipts", "orchestration_review_work",
		"orchestration_blockers",
	} {
		assertTableAbsent(t, migrated.DB(), table)
	}

	for _, fixture := range []struct {
		table string
		id    any
	}{
		{"task_handoffs", "goal-254-task-handoff"},
		{"goal_handoffs", "goal-254-goal-handoff"},
		{"plan_handoffs", "goal-254-plan-handoff"},
		{"decisions", 1},
	} {
		var count int
		if err := migrated.DB().QueryRow(`SELECT COUNT(*) FROM `+fixture.table+` WHERE id = ?`, fixture.id).Scan(&count); err != nil {
			t.Fatalf("read preserved %s row: %v", fixture.table, err)
		}
		if count != 1 {
			t.Errorf("preserved %s rows = %d, want 1", fixture.table, count)
		}
	}
	for _, table := range []string{"task_handoffs", "goal_handoffs", "plan_handoffs"} {
		columns := migrationTableColumns(t, migrated.DB(), table)
		for _, column := range []string{"review_rejection_received_by", "review_rejection_received_at"} {
			if _, ok := columns[column]; !ok {
				t.Errorf("%s is missing %s", table, column)
			}
		}
	}
}

func TestMigrationIntegrityVerifierOnCopiedDatabaseFromEnvironment(t *testing.T) {
	sourcePath := os.Getenv("ATCT_MIGRATION_CHECK_DB")
	if sourcePath == "" {
		t.Skip("set ATCT_MIGRATION_CHECK_DB to a copied SQLite database to check a production snapshot")
	}

	copyPath := filepath.Join(t.TempDir(), "atct.db")
	if err := copySQLiteDatabase(sourcePath, copyPath); err != nil {
		t.Fatalf("copy SQLite database: %v", err)
	}
	report, err := verifyMigrationIntegrity(copyPath)
	if err != nil {
		t.Fatalf("verifyMigrationIntegrity(%q) = %v", sourcePath, err)
	}
	t.Logf("migration integrity report: %s", report)
}

type migrationIntegrityReport struct {
	beforeCounts  map[string]int64
	afterCounts   map[string]int64
	removedTables []string
	addedTables   []string
	removedFKs    []string
	addedFKs      []string
	afterFKCounts []foreignKeyOrphanCount
}

func (r migrationIntegrityReport) String() string {
	return fmt.Sprintf("rows before=%v after=%v; tables removed=%v added=%v; foreign keys removed=%v added=%v; foreign-key orphan counts=%v", r.beforeCounts, r.afterCounts, r.removedTables, r.addedTables, r.removedFKs, r.addedFKs, r.afterFKCounts)
}

type fixtureColumn struct {
	name       string
	columnType string
}

func fixtureIDValue(t *testing.T, db *sql.DB, table, textID string, integerID int64) any {
	t.Helper()
	columns, err := fixtureTableColumns(db, table)
	if err != nil {
		t.Fatalf("read %s fixture columns: %v", table, err)
	}
	for _, column := range columns {
		if column.name == "id" {
			if strings.EqualFold(column.columnType, "INTEGER") {
				return integerID
			}
			return textID
		}
	}
	t.Fatalf("%s has no id column", table)
	return nil
}

func insertFixtureRow(db *sql.DB, table string, values map[string]any) error {
	columns, err := fixtureTableColumns(db, table)
	if err != nil {
		return err
	}
	var names, placeholders []string
	args := make([]any, 0, len(values))
	for _, column := range columns {
		value, present := values[column.name]
		if !present {
			continue
		}
		names = append(names, quoteSQLiteIdentifier(column.name))
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	if len(names) == 0 {
		return fmt.Errorf("no fixture values match columns in %s", table)
	}
	_, err = db.Exec("INSERT INTO "+quoteSQLiteIdentifier(table)+" ("+strings.Join(names, ", ")+") VALUES ("+strings.Join(placeholders, ", ")+")", args...)
	return err
}

func fixtureTableColumns(db *sql.DB, table string) ([]fixtureColumn, error) {
	rows, err := db.Query("PRAGMA table_info(" + quoteSQLiteString(table) + ")")
	if err != nil {
		return nil, fmt.Errorf("read columns for %s: %w", table, err)
	}
	defer rows.Close()
	var columns []fixtureColumn
	for rows.Next() {
		var cid, notNull, primaryKey int
		var column fixtureColumn
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &column.name, &column.columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("scan columns for %s: %w", table, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read columns for %s: %w", table, err)
	}
	return columns, nil
}

func verifyMigrationIntegrity(dbPath string) (migrationIntegrityReport, error) {
	var report migrationIntegrityReport
	beforeDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return report, fmt.Errorf("open database before migration: %w", err)
	}
	before, err := migrationDataTableCounts(beforeDB)
	beforeFKs, foreignKeyErr := foreignKeyConstraintsForTables(beforeDB, before)
	if err == nil && foreignKeyErr != nil {
		err = foreignKeyErr
	}
	if closeErr := beforeDB.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return report, fmt.Errorf("inspect database before migration: %w", err)
	}
	report.beforeCounts = before

	s, err := Open(dbPath)
	if err != nil {
		return report, fmt.Errorf("run migration: %w", err)
	}
	if err := s.Close(); err != nil {
		return report, fmt.Errorf("close migrated database: %w", err)
	}

	afterDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return report, fmt.Errorf("open database after migration: %w", err)
	}
	defer afterDB.Close()
	after, err := migrationDataTableCounts(afterDB)
	if err != nil {
		return report, fmt.Errorf("count rows after migration: %w", err)
	}
	report.afterCounts = after
	afterFKs, err := foreignKeyConstraintsForTables(afterDB, after)
	if err != nil {
		return report, err
	}
	report.removedTables, report.addedTables = mapKeyDifference(before, after)
	report.removedFKs, report.addedFKs = foreignKeyDifference(beforeFKs, afterFKs)
	report.afterFKCounts, err = foreignKeyOrphanCounts(afterDB)
	if err != nil {
		return report, err
	}
	if err := compareSharedMigrationDataTableCounts(before, after); err != nil {
		return report, err
	}

	violations, err := foreignKeyViolations(afterDB)
	if err != nil {
		return report, err
	}
	if len(violations) != 0 {
		return report, fmt.Errorf("foreign key violations: %s", strings.Join(violations, "; "))
	}
	return report, nil
}

func migrationDataTableCounts(db *sql.DB) (map[string]int64, error) {
	rows, err := db.Query(`
SELECT name
FROM sqlite_schema
WHERE type = 'table'
  AND name NOT LIKE 'sqlite_%'
  AND name <> 'schema_migrations'
ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list data tables: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf("scan data table: %w", err)
		}
		var count int64
		if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteSQLiteIdentifier(table)).Scan(&count); err != nil {
			return nil, fmt.Errorf("count rows in %s: %w", table, err)
		}
		counts[table] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list data tables: %w", err)
	}
	return counts, nil
}

func compareSharedMigrationDataTableCounts(before, after map[string]int64) error {
	tables := make(map[string]struct{}, len(before))
	for table := range before {
		if _, existsAfter := after[table]; existsAfter {
			tables[table] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(tables))
	for table := range tables {
		ordered = append(ordered, table)
	}
	sort.Strings(ordered)
	for _, table := range ordered {
		beforeCount := before[table]
		afterCount := after[table]
		if beforeCount != afterCount {
			return fmt.Errorf("row count changed for %s: before=%d after=%d", table, beforeCount, afterCount)
		}
	}
	return nil
}

func mapKeyDifference(before, after map[string]int64) (removed, added []string) {
	for table := range before {
		if _, exists := after[table]; !exists {
			removed = append(removed, table)
		}
	}
	for table := range after {
		if _, existed := before[table]; !existed {
			added = append(added, table)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	return removed, added
}

func foreignKeyViolations(db *sql.DB) ([]string, error) {
	pragmaRows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return nil, fmt.Errorf("run PRAGMA foreign_key_check: %w", err)
	}
	defer pragmaRows.Close()

	var pragmaViolations []string
	for pragmaRows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var foreignKeyID int
		if err := pragmaRows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return nil, fmt.Errorf("scan PRAGMA foreign_key_check: %w", err)
		}
		pragmaViolations = append(pragmaViolations, fmt.Sprintf("%s rowid=%v parent=%s fk=%d", table, rowID, parent, foreignKeyID))
	}
	if err := pragmaRows.Err(); err != nil {
		return nil, fmt.Errorf("run PRAGMA foreign_key_check: %w", err)
	}

	counts, err := foreignKeyOrphanCounts(db)
	if err != nil {
		return nil, err
	}
	var countedViolations []string
	for _, count := range counts {
		if count.orphans != 0 {
			countedViolations = append(countedViolations, count.String())
		}
	}
	if len(pragmaViolations) == 0 {
		return countedViolations, nil
	}
	return append(pragmaViolations, countedViolations...), nil
}

type foreignKeyOrphanCount struct {
	foreignKey string
	orphans    int64
}

func (count foreignKeyOrphanCount) String() string {
	return fmt.Sprintf("%s: %d", count.foreignKey, count.orphans)
}

func foreignKeyOrphanCounts(db *sql.DB) ([]foreignKeyOrphanCount, error) {
	tables, err := migrationDataTableCounts(db)
	if err != nil {
		return nil, err
	}
	constraints, err := foreignKeyConstraintsForTables(db, tables)
	if err != nil {
		return nil, err
	}
	if len(constraints) == 0 {
		return nil, fmt.Errorf("no foreign keys found through PRAGMA foreign_key_list")
	}
	counts := make([]foreignKeyOrphanCount, 0, len(constraints))
	for _, constraint := range constraints {
		orphans, err := countForeignKeyOrphans(db, constraint)
		if err != nil {
			return nil, err
		}
		counts = append(counts, foreignKeyOrphanCount{foreignKey: constraint.String(), orphans: orphans})
	}
	return counts, nil
}

type migrationForeignKey struct {
	childTable  string
	parentTable string
	columns     []migrationForeignKeyColumn
}

type migrationForeignKeyColumn struct {
	childColumn  string
	parentColumn string
}

func (foreignKey migrationForeignKey) String() string {
	columns := make([]string, 0, len(foreignKey.columns))
	for _, column := range foreignKey.columns {
		columns = append(columns, foreignKey.childTable+"."+column.childColumn+" -> "+foreignKey.parentTable+"."+column.parentColumn)
	}
	return strings.Join(columns, ", ")
}

func foreignKeyDifference(before, after []migrationForeignKey) (removed, added []string) {
	beforeSet := make(map[string]struct{}, len(before))
	for _, foreignKey := range before {
		beforeSet[foreignKey.String()] = struct{}{}
	}
	afterSet := make(map[string]struct{}, len(after))
	for _, foreignKey := range after {
		afterSet[foreignKey.String()] = struct{}{}
	}
	for foreignKey := range beforeSet {
		if _, exists := afterSet[foreignKey]; !exists {
			removed = append(removed, foreignKey)
		}
	}
	for foreignKey := range afterSet {
		if _, existed := beforeSet[foreignKey]; !existed {
			added = append(added, foreignKey)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	return removed, added
}

func foreignKeyConstraints(db *sql.DB) ([]migrationForeignKey, error) {
	tables, err := migrationDataTableCounts(db)
	if err != nil {
		return nil, err
	}
	return foreignKeyConstraintsForTables(db, tables)
}

func foreignKeyConstraintsForTables(db *sql.DB, tables map[string]int64) ([]migrationForeignKey, error) {
	var constraints []migrationForeignKey
	for table := range tables {
		rows, err := db.Query("PRAGMA foreign_key_list(" + quoteSQLiteString(table) + ")")
		if err != nil {
			return nil, fmt.Errorf("list foreign keys for %s: %w", table, err)
		}
		byID := make(map[int]*migrationForeignKey)
		for rows.Next() {
			var id, seq int
			var parent, childColumn, parentColumn, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &seq, &parent, &childColumn, &parentColumn, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan foreign key for %s: %w", table, err)
			}
			constraint := byID[id]
			if constraint == nil {
				constraint = &migrationForeignKey{childTable: table, parentTable: parent}
				byID[id] = constraint
			}
			constraint.columns = append(constraint.columns, migrationForeignKeyColumn{childColumn: childColumn, parentColumn: parentColumn})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list foreign keys for %s: %w", table, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close foreign keys for %s: %w", table, err)
		}
		for _, constraint := range byID {
			constraints = append(constraints, *constraint)
		}
	}
	sort.Slice(constraints, func(i, j int) bool {
		return constraints[i].childTable+"\x00"+constraints[i].parentTable < constraints[j].childTable+"\x00"+constraints[j].parentTable
	})
	return constraints, nil
}

func countForeignKeyOrphans(db *sql.DB, foreignKey migrationForeignKey) (int64, error) {
	conditions := make([]string, 0, len(foreignKey.columns))
	nonNull := make([]string, 0, len(foreignKey.columns))
	for _, column := range foreignKey.columns {
		if column.parentColumn == "" {
			return 0, fmt.Errorf("foreign key %s -> %s omits its parent column", foreignKey.childTable, foreignKey.parentTable)
		}
		conditions = append(conditions, "parent."+quoteSQLiteIdentifier(column.parentColumn)+" = child."+quoteSQLiteIdentifier(column.childColumn))
		nonNull = append(nonNull, "child."+quoteSQLiteIdentifier(column.childColumn)+" IS NOT NULL")
	}
	query := "SELECT COUNT(*) FROM " + quoteSQLiteIdentifier(foreignKey.childTable) + " AS child WHERE " + strings.Join(nonNull, " AND ") + " AND NOT EXISTS (SELECT 1 FROM " + quoteSQLiteIdentifier(foreignKey.parentTable) + " AS parent WHERE " + strings.Join(conditions, " AND ") + ")"
	var count int64
	if err := db.QueryRow(query).Scan(&count); err != nil {
		return 0, fmt.Errorf("count orphans for %s -> %s: %w", foreignKey.childTable, foreignKey.parentTable, err)
	}
	return count, nil
}

func copySQLiteDatabase(sourcePath, destinationPath string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		contents, err := os.ReadFile(sourcePath + suffix)
		if err != nil {
			if suffix != "" && os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s: %w", sourcePath+suffix, err)
		}
		if err := os.WriteFile(destinationPath+suffix, contents, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", destinationPath+suffix, err)
		}
	}
	return nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
