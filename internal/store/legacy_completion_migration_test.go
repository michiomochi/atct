package store

import (
	"database/sql"
	"testing"
)

func TestLegacyCompletionMigrationWithdrawsOnlyOpenRows(t *testing.T) {
	db := openMigrationTestDB(t)
	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	const targetMigration = "0044_retire_legacy_completion.sql"
	for _, migration := range migrations {
		if migration.filename == targetMigration {
			break
		}
		if _, err := db.Exec(migration.sql); err != nil {
			t.Fatalf("apply fixture migration %s: %v", migration.filename, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, migration.filename, "2026-09-19T00:00:00Z"); err != nil {
			t.Fatalf("record fixture migration %s: %v", migration.filename, err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 6`); err != nil {
		t.Fatalf("set fixture schema version: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO projects (id, name, root_path, created_at)
VALUES (1, 'legacy completion migration', '/legacy-completion-migration', '2026-09-19T00:00:00Z');
INSERT INTO goals (
    id, project_id, content, status, creator, result_summary,
    work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
    created_at, updated_at
)
VALUES (
    1, 1, 'legacy completion goal', 'done', 'human', 'legacy summary',
    'legacy work', 'legacy now', 'legacy verify', 'legacy surprises', 'legacy review', 'legacy next',
    '2026-09-19T00:00:00Z', '2026-09-19T00:00:00Z'
);
INSERT INTO decisions (
    id, goal_id, kind, question, options, status, answer_text,
    answered_at, applied_at, agent_session_id, created_at
)
VALUES
    (1, 1, 'completion', 'open completion', '[]', 'open', 'old open answer', NULL, NULL, 0, '2026-09-19T00:01:00Z'),
    (2, 1, 'completion', 'answered completion', '[]', 'answered', 'old answered answer', '2026-09-19T00:02:00Z', NULL, 0, '2026-09-19T00:01:00Z'),
    (3, 1, 'completion', 'applied completion', '[]', 'applied', 'old applied answer', '2026-09-19T00:02:00Z', '2026-09-19T00:03:00Z', 0, '2026-09-19T00:01:00Z'),
    (4, 1, 'completion', 'closed completion', '[]', 'closed', 'old closed answer', '2026-09-19T00:02:00Z', '2026-09-19T00:03:00Z', 0, '2026-09-19T00:01:00Z'),
    (5, 1, 'goal_review', 'open goal review', '[{"label":"approve"}]', 'open', '', NULL, NULL, 0, '2026-09-19T00:01:00Z');
`); err != nil {
		t.Fatalf("insert migration fixture rows: %v", err)
	}

	beforeGoal := readLegacyCompletionGoalSnapshot(t, db)
	beforeDecisions := readLegacyCompletionDecisionSnapshots(t, db)
	if err := applyEmbeddedMigrations(db); err != nil {
		t.Fatalf("apply legacy completion migration: %v", err)
	}

	afterGoal := readLegacyCompletionGoalSnapshot(t, db)
	if afterGoal != beforeGoal {
		t.Fatalf("goal changed during legacy completion migration: before=%+v after=%+v", beforeGoal, afterGoal)
	}
	afterDecisions := readLegacyCompletionDecisionSnapshots(t, db)
	open := afterDecisions[1]
	if open.status != "withdrawn" {
		t.Fatalf("open completion status = %q, want withdrawn", open.status)
	}
	if open.answerText != "legacy completion retired; request a named goal review" {
		t.Fatalf("open completion answer_text = %q, want retirement message", open.answerText)
	}
	if open.answeredAt == "" {
		t.Fatal("open completion answered_at is empty")
	}
	for _, id := range []int{2, 3, 4} {
		if afterDecisions[id] != beforeDecisions[id] {
			t.Fatalf("legacy completion decision %d changed: before=%+v after=%+v", id, beforeDecisions[id], afterDecisions[id])
		}
	}
	nonCompletionBefore, ok := beforeDecisions[5]
	if !ok {
		t.Fatal("non-completion sentinel is missing before migration")
	}
	nonCompletionAfter, ok := afterDecisions[5]
	if !ok {
		t.Fatal("non-completion sentinel is missing after migration")
	}
	if nonCompletionAfter != nonCompletionBefore {
		t.Fatalf("non-completion decision changed: before=%+v after=%+v", nonCompletionBefore, nonCompletionAfter)
	}
	assertMigrationRecorded(t, db, targetMigration)
}

type legacyCompletionGoalSnapshot struct {
	status        string
	resultSummary string
	workDone      string
	nowPossible   string
	howToVerify   string
	surprises     string
	needsReview   string
	nextSteps     string
}

func readLegacyCompletionGoalSnapshot(t *testing.T, db *sql.DB) legacyCompletionGoalSnapshot {
	t.Helper()
	var snapshot legacyCompletionGoalSnapshot
	if err := db.QueryRow(`
SELECT status, result_summary, work_done, now_possible, how_to_verify,
       surprises, needs_review, next_steps
FROM goals
WHERE id = 1
`).Scan(
		&snapshot.status, &snapshot.resultSummary, &snapshot.workDone,
		&snapshot.nowPossible, &snapshot.howToVerify, &snapshot.surprises,
		&snapshot.needsReview, &snapshot.nextSteps,
	); err != nil {
		t.Fatalf("read goal snapshot: %v", err)
	}
	return snapshot
}

type legacyCompletionDecisionSnapshot struct {
	goalID           int64
	taskID           int64
	kind             string
	question         string
	options          string
	status           string
	defaultOption    string
	defaultAfterMS   int64
	defaultAppliedAt string
	answerLabel      string
	answerText       string
	answeredAt       string
	appliedAt        string
	agentSessionID   int64
	createdAt        string
}

func readLegacyCompletionDecisionSnapshots(t *testing.T, db *sql.DB) map[int]legacyCompletionDecisionSnapshot {
	t.Helper()
	rows, err := db.Query(`
	SELECT id, goal_id, COALESCE(task_id, 0), kind, question, options, status,
	       default_option, COALESCE(default_after_ms, -1), COALESCE(default_applied_at, ''),
	       answer_label, answer_text, COALESCE(answered_at, ''), COALESCE(applied_at, ''),
	       agent_session_id, created_at
FROM decisions
WHERE goal_id = 1
ORDER BY id
`)
	if err != nil {
		t.Fatalf("read decision snapshots: %v", err)
	}
	defer rows.Close()

	snapshots := make(map[int]legacyCompletionDecisionSnapshot)
	for rows.Next() {
		var id int
		var snapshot legacyCompletionDecisionSnapshot
		if err := rows.Scan(
			&id, &snapshot.goalID, &snapshot.taskID, &snapshot.kind, &snapshot.question,
			&snapshot.options, &snapshot.status, &snapshot.defaultOption, &snapshot.defaultAfterMS,
			&snapshot.defaultAppliedAt, &snapshot.answerLabel, &snapshot.answerText,
			&snapshot.answeredAt, &snapshot.appliedAt, &snapshot.agentSessionID, &snapshot.createdAt,
		); err != nil {
			t.Fatalf("scan decision snapshot: %v", err)
		}
		snapshots[id] = snapshot
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read decision snapshots: %v", err)
	}
	return snapshots
}
