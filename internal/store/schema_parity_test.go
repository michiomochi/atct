package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	atlas "ariga.io/atlas/sql/schema"
	"ariga.io/atlas/sql/sqlite"
)

func TestSchemaParity(t *testing.T) {
	migrationDB := openMigrationTestDB(t)
	if err := applyEmbeddedMigrations(migrationDB); err != nil {
		t.Fatalf("apply embedded migrations: %v", err)
	}

	declaredDB := materializeSchemaSQL(t)
	assertSchemaParity(t, migrationDB, declaredDB)
}

// TestSchemaParityDrift compares a copy of a live database against the schema
// its own schema_migrations records say it should have. Migrations the copy has
// not applied yet are not drift: a release always ships migrations before the
// running daemon applies them. What this catches is a live database whose shape
// no recorded migration explains -- the `runs` table that survived the historical
// bridge in migrateSchema is the case that motivated it.
func TestSchemaParityDrift(t *testing.T) {
	driftDBPath := os.Getenv("ATCT_DRIFT_DB")
	if driftDBPath == "" {
		t.Skip("ATCT_DRIFT_DB is not set")
	}

	driftDB, err := sql.Open("sqlite", driftDBPath)
	if err != nil {
		t.Fatalf("open drift database: %v", err)
	}
	driftDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = driftDB.Close() })

	expectedDB := materializeRecordedMigrations(t, driftDB)
	assertSchemaParity(t, driftDB, expectedDB)
}

// materializeRecordedMigrations builds the schema the given database claims to
// have, by applying exactly the embedded migrations its schema_migrations table
// records as applied.
func materializeRecordedMigrations(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()

	rows, err := db.Query(`SELECT filename FROM ` + schemaMigrationsTable)
	if err != nil {
		t.Fatalf("read applied migrations: %v", err)
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var filename string
		if err := rows.Scan(&filename); err != nil {
			t.Fatalf("scan applied migration: %v", err)
		}
		applied[filename] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read applied migrations: %v", err)
	}

	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	expected := openMigrationTestDB(t)
	for _, migration := range migrations {
		if !applied[migration.filename] {
			continue
		}
		if _, err := expected.Exec(migration.sql); err != nil {
			t.Fatalf("apply %s: %v", migration.filename, err)
		}
		delete(applied, migration.filename)
	}
	for filename := range applied {
		t.Errorf("database records %s, which is not an embedded migration", filename)
	}
	return expected
}

func materializeSchemaSQL(t *testing.T) *sql.DB {
	t.Helper()

	contents, err := os.ReadFile("../../schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}

	db := openMigrationTestDB(t)
	if _, err := db.Exec(string(contents)); err != nil {
		t.Fatalf("materialize schema.sql: %v", err)
	}
	return db
}

func assertSchemaParity(t *testing.T, fromDB, toDB *sql.DB) {
	t.Helper()

	from := inspectSQLiteSchema(t, fromDB)
	to := inspectSQLiteSchema(t, toDB)
	normalizeSchema(from)
	normalizeSchema(to)

	drv, err := sqlite.Open(fromDB)
	if err != nil {
		t.Fatalf("open Atlas driver: %v", err)
	}
	changes, err := drv.SchemaDiff(from, to)
	if err != nil {
		t.Fatalf("diff schemas: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("schema drift: %s", describeSchemaChanges(changes))
	}
}

func inspectSQLiteSchema(t *testing.T, db *sql.DB) *atlas.Schema {
	t.Helper()

	drv, err := sqlite.Open(db)
	if err != nil {
		t.Fatalf("open Atlas driver: %v", err)
	}
	schema, err := drv.InspectSchema(context.Background(), "main", &atlas.InspectOptions{
		Exclude: []string{"schema_migrations"},
	})
	if err != nil {
		t.Fatalf("inspect SQLite schema: %v", err)
	}
	return schema
}

// normalizeSchema removes the two ways Atlas reports a difference between
// schemas that mean the same thing to SQLite.
//
// CHECK expressions are compared as raw text, so indentation alone shows up as
// a DropCheck/AddCheck pair. schema.sql indents its goals CHECK two spaces less
// than 0018_integer_primary_keys.sql does.
//
// Implicit indexes created by an inline UNIQUE column constraint are named
// sqlite_autoindex_<table>_<n> by SQLite, and Atlas's diff wants to rename them
// to an explicit name derived from their columns. It does this on one side only,
// so a schema compared against itself reports a rename. Canonicalizing the name
// on both sides removes the false positive while keeping a genuinely missing
// UNIQUE visible as an absent index.
func normalizeSchema(schema *atlas.Schema) {
	for _, table := range schema.Tables {
		for _, attr := range table.Attrs {
			if check, ok := attr.(*atlas.Check); ok {
				check.Expr = strings.Join(strings.Fields(check.Expr), " ")
			}
		}
		for _, index := range table.Indexes {
			if !strings.HasPrefix(index.Name, "sqlite_autoindex_") {
				continue
			}
			columns := make([]string, 0, len(index.Parts))
			for _, part := range index.Parts {
				if part.C != nil {
					columns = append(columns, part.C.Name)
				}
			}
			if len(columns) == 0 {
				continue
			}
			index.Name = table.Name + "_" + strings.Join(columns, "_")
		}
	}
}

func describeSchemaChanges(changes []atlas.Change) string {
	descriptions := make([]string, 0, len(changes))
	for _, change := range changes {
		descriptions = append(descriptions, describeSchemaChange(change))
	}
	return strings.Join(descriptions, ", ")
}

func describeSchemaChange(change atlas.Change) string {
	switch c := change.(type) {
	case *atlas.AddTable:
		return "AddTable " + c.T.Name
	case *atlas.DropTable:
		return "DropTable " + c.T.Name
	case *atlas.ModifyTable:
		parts := make([]string, 0, len(c.Changes))
		for _, sub := range c.Changes {
			parts = append(parts, describeTableChange(c.T.Name, sub))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%T", change)
	}
}

func describeTableChange(table string, change atlas.Change) string {
	switch c := change.(type) {
	case *atlas.AddColumn:
		return fmt.Sprintf("AddColumn %s.%s", table, c.C.Name)
	case *atlas.DropColumn:
		return fmt.Sprintf("DropColumn %s.%s", table, c.C.Name)
	case *atlas.ModifyColumn:
		return fmt.Sprintf("ModifyColumn %s.%s", table, c.To.Name)
	case *atlas.AddIndex:
		return fmt.Sprintf("AddIndex %s.%s", table, c.I.Name)
	case *atlas.DropIndex:
		return fmt.Sprintf("DropIndex %s.%s", table, c.I.Name)
	case *atlas.ModifyIndex:
		return fmt.Sprintf("ModifyIndex %s.%s", table, c.To.Name)
	case *atlas.AddCheck:
		return fmt.Sprintf("AddCheck %s %s", table, c.C.Expr)
	case *atlas.DropCheck:
		return fmt.Sprintf("DropCheck %s %s", table, c.C.Expr)
	case *atlas.ModifyCheck:
		return fmt.Sprintf("ModifyCheck %s %s", table, c.To.Expr)
	case *atlas.AddForeignKey:
		return fmt.Sprintf("AddForeignKey %s.%s", table, c.F.Symbol)
	case *atlas.DropForeignKey:
		return fmt.Sprintf("DropForeignKey %s.%s", table, c.F.Symbol)
	case *atlas.ModifyForeignKey:
		return fmt.Sprintf("ModifyForeignKey %s.%s", table, c.To.Symbol)
	default:
		return fmt.Sprintf("%s %T", table, change)
	}
}
