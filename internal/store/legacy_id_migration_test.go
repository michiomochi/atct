package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestMigration0020RemovesLegacyIDColumnsAndIndexes(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	assertMigrationRecorded(t, s.DB(), "0020_drop_legacy_ids.sql")
	for _, table := range []string{"projects", "goals", "tasks", "decisions"} {
		assertLegacyIDRemoved(t, s.DB(), table)
	}
}

func assertLegacyIDRemoved(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	if _, ok := migrationTableColumns(t, db, table)["legacy_id"]; ok {
		t.Errorf("%s still has legacy_id", table)
	}

	var indexCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_index_list(?) WHERE name = ?`,
		table,
		"idx_"+table+"_legacy_id",
	).Scan(&indexCount); err != nil {
		t.Fatalf("check %s legacy_id index: %v", table, err)
	}
	if indexCount != 0 {
		t.Errorf("%s legacy_id index count = %d, want 0", table, indexCount)
	}
}

func TestResolveLegacyIDReportsMappingAfterMigration0020(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	resolvers := map[string]func(context.Context, string) (int64, error){
		"project":  s.ResolveProjectID,
		"goal":     s.ResolveGoalID,
		"task":     s.ResolveTaskID,
		"decision": s.ResolveDecisionID,
	}
	for name, resolve := range resolvers {
		t.Run(name, func(t *testing.T) {
			_, err := resolve(context.Background(), "1e082f2f")
			if err == nil {
				t.Fatal("legacy ID unexpectedly accepted")
			}
			if !strings.Contains(err.Error(), "id must be a number; UUID-style ids were removed in 0020.") {
				t.Fatalf("legacy ID error = %q, want migration guidance", err)
			}
			if !strings.Contains(err.Error(), "doc/specs/2026-08-27-uuid-to-integer-mapping.md") {
				t.Fatalf("legacy ID error = %q, want mapping path", err)
			}
			if strings.Contains(err.Error(), "not found") {
				t.Fatalf("legacy ID error = %q, must not report not found", err)
			}
		})
	}
}

func TestResolveIDKeepsAcceptingNumericStringsAfterMigration0020(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	id, err := s.ResolveGoalID(context.Background(), "7")
	if err != nil {
		t.Fatalf("ResolveGoalID numeric string: %v", err)
	}
	if id != 7 {
		t.Fatalf("ResolveGoalID numeric string = %d, want 7", id)
	}
}
