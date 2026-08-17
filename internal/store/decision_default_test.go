package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestDecisionRoundTripsDefaultFields(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	after := int64(1800000)

	saved, err := s.AskDecision(ctx, AskInput{
		GoalID:         goalID,
		Kind:           domain.DecisionKind("decision"),
		Question:       "A か B か",
		Options:        []domain.Option{{Label: "A"}, {Label: "B"}},
		DefaultOption:  "A",
		DefaultAfterMs: &after,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetDecision(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultOption != "A" {
		t.Fatalf("DefaultOption = %q, want A", got.DefaultOption)
	}
	if got.DefaultAfterMs == nil || *got.DefaultAfterMs != after {
		t.Fatalf("DefaultAfterMs = %v, want %d", got.DefaultAfterMs, after)
	}
	if got.DefaultAppliedAt != nil {
		t.Fatal("DefaultAppliedAt should be nil before it fires")
	}
}

func TestOpenMigratesDecisionDefaultColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE decisions (
  id TEXT PRIMARY KEY,
  goal_id TEXT NOT NULL,
  task_id TEXT,
  kind TEXT NOT NULL,
  question TEXT NOT NULL,
  options TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL,
  answer_label TEXT NOT NULL DEFAULT '',
  answer_text TEXT NOT NULL DEFAULT '',
  answered_by TEXT NOT NULL DEFAULT '',
  answered_at TEXT,
  applied_at TEXT,
  run_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
PRAGMA user_version = 1;
INSERT INTO decisions (id, goal_id, kind, question, options, status, created_at)
VALUES ('old-decision', 'old-goal', 'decision', 'old question', '[]', 'open', '2026-08-18T00:00:00Z');
`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := s.GetDecision(context.Background(), "old-decision")
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultOption != "" {
		t.Fatalf("migrated DefaultOption = %q, want empty", got.DefaultOption)
	}
	if got.DefaultAfterMs != nil {
		t.Fatalf("migrated DefaultAfterMs = %v, want nil", got.DefaultAfterMs)
	}
	if got.DefaultAppliedAt != nil {
		t.Fatalf("migrated DefaultAppliedAt = %v, want nil", got.DefaultAppliedAt)
	}
}
