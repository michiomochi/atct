package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAnsweredByFromVersionsZeroAndOne(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(fmt.Sprintf("user_version_%d", version), func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "atct.db")
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(fmt.Sprintf(`
CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  root_path TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);
CREATE TABLE goals (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  result_summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  goal_id TEXT NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  agent TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  declare_key TEXT NOT NULL,
  claimed_by TEXT NOT NULL DEFAULT '',
  claimed_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
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
PRAGMA user_version = %d;
INSERT INTO projects (id, name, root_path, created_at)
VALUES ('project-one', 'project one', '/project-one', '2026-08-18T00:00:00Z');
INSERT INTO goals (id, project_id, title, status, created_at, updated_at)
VALUES ('goal-one', 'project-one', 'goal one', 'active', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z');
INSERT INTO tasks (id, goal_id, title, status, declare_key, created_at, updated_at)
VALUES ('task-one', 'goal-one', 'task one', 'todo', 'task-one', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z');
INSERT INTO decisions (
  id, goal_id, task_id, kind, question, options, status, answer_label, answer_text,
  answered_by, answered_at, applied_at, run_id, created_at
) VALUES
  ('decision-one', 'goal-one', 'task-one', 'decision', 'Choose one', '[{"label":"A"}]', 'answered', 'A', 'first answer',
   'first human', '2026-08-18T00:01:00Z', NULL, 'run-one', '2026-08-18T00:00:00Z'),
  ('decision-two', 'goal-one', NULL, 'completion', 'Approve?', '[]', 'applied', 'approve', '',
   'second human', '2026-08-18T00:02:00Z', '2026-08-18T00:03:00Z', 'run-two', '2026-08-18T00:00:01Z');
`, version))
			if err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			s, err := Open(dbPath)
			if err != nil {
				t.Fatalf("Open migrated DB: %v", err)
			}
			defer s.Close()

			var gotVersion int
			if err := s.DB().QueryRow("PRAGMA user_version").Scan(&gotVersion); err != nil {
				t.Fatal(err)
			}
			if gotVersion != schemaVersion {
				t.Fatalf("user_version = %d, want %d", gotVersion, schemaVersion)
			}
			if hasDecisionsColumn(t, s.DB(), "answered_by") {
				t.Fatal("decisions.answered_by still exists after migration")
			}

			for _, want := range []struct {
				id, question, label, text, agentSessionID string
				status                                    string
			}{
				{id: "decision-one", question: "Choose one", label: "A", text: "first answer", agentSessionID: "run-one", status: "answered"},
				{id: "decision-two", question: "Approve?", label: "approve", text: "", agentSessionID: "run-two", status: "applied"},
			} {
				got, err := s.GetDecision(context.Background(), want.id)
				if err != nil {
					t.Fatalf("GetDecision(%q): %v", want.id, err)
				}
				if got.ID != want.id || got.Question != want.question || got.AnswerLabel != want.label ||
					got.AnswerText != want.text || string(got.Status) != want.status || got.AgentSessionID != want.agentSessionID {
					t.Fatalf("migrated decision = %+v, want id=%q question=%q label=%q text=%q status=%q run_id=%q",
						got, want.id, want.question, want.label, want.text, want.status, want.agentSessionID)
				}
			}
		})
	}
}

func hasDecisionsColumn(t *testing.T, db *sql.DB, want string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(decisions)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == want {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}
