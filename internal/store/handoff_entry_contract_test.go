package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestHandoffEntrySchemaUsesCanonicalContract(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	wantColumns := []string{
		"id",
		"handoff_id",
		"kind",
		"body",
		"author_session_id",
		"in_reply_to_id",
		"created_at",
	}

	for _, table := range []string{"task_handoff_entries", "goal_handoff_entries"} {
		rows, err := s.DB().QueryContext(ctx, `
			SELECT name, type, pk
			FROM pragma_table_info(?)
			ORDER BY cid
		`, table)
		if err != nil {
			t.Fatalf("read %s columns: %v", table, err)
		}
		var gotColumns []string
		var idType string
		var idPrimaryKey int
		for rows.Next() {
			var name, columnType string
			var primaryKey int
			if err := rows.Scan(&name, &columnType, &primaryKey); err != nil {
				rows.Close()
				t.Fatalf("scan %s column: %v", table, err)
			}
			gotColumns = append(gotColumns, name)
			if name == "id" {
				idType = columnType
				idPrimaryKey = primaryKey
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close %s columns: %v", table, err)
		}
		if !reflect.DeepEqual(gotColumns, wantColumns) {
			t.Fatalf("%s columns = %v, want %v", table, gotColumns, wantColumns)
		}
		if !strings.EqualFold(idType, "INTEGER") || idPrimaryKey != 1 {
			t.Fatalf("%s id definition = type %q pk %d, want INTEGER primary key", table, idType, idPrimaryKey)
		}

		var triggerCount int
		if err := s.DB().QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'trigger' AND tbl_name = ?
		`, table).Scan(&triggerCount); err != nil {
			t.Fatalf("count %s triggers: %v", table, err)
		}
		if triggerCount != 0 {
			t.Fatalf("%s trigger count = %d, want 0", table, triggerCount)
		}
	}
}

func TestHandoffEntryUsesAllowedKindsAndRejectsRemovedKinds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "contract-kinds-requester")
	addTestAgentSession(t, s, "contract-kinds-receiver")
	handoff, err := s.RequestTaskHandoff(ctx, "contract-kinds-handoff", taskID, testSessionID("contract-kinds-requester"), "take this task")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("contract-kinds-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	entry, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, "review_requested", "please review", testSessionID("contract-kinds-receiver"), "")
	if err != nil {
		t.Fatalf("append canonical kind: %v", err)
	}
	if _, err := strconv.ParseInt(entry.EntryID, 10, 64); err != nil || entry.Kind != "review_requested" {
		t.Fatalf("canonical entry = %+v, want an integer id and review_requested kind", entry)
	}
	if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, "progress", "removed", testSessionID("contract-kinds-receiver"), ""); !errors.Is(err, ErrHandoffEntryKindInvalid) {
		t.Fatalf("removed kind error = %v, want ErrHandoffEntryKindInvalid", err)
	}
}

func TestHandoffEntryDatabaseAllowsExactlySixCanonicalKinds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "contract-six-kinds-requester")
	addTestAgentSession(t, s, "contract-six-kinds-receiver")
	handoff, err := s.RequestTaskHandoff(ctx, "contract-six-kinds-handoff", taskID, testSessionID("contract-six-kinds-requester"), "request")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("contract-six-kinds-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	canonicalKinds := []string{
		HandoffEntryKindRequest,
		HandoffEntryKindReceived,
		HandoffEntryKindReviewRequested,
		HandoffEntryKindReviewReceived,
		HandoffEntryKindReviewRejected,
		HandoffEntryKindCompleted,
	}
	for _, kind := range canonicalKinds {
		if _, err := s.DB().ExecContext(ctx, `
			INSERT INTO task_handoff_entries (handoff_id, kind, body, created_at)
			VALUES (?, ?, ?, ?)
		`, handoff.ID, kind, "canonical "+kind, "2026-09-06T00:00:00Z"); err != nil {
			t.Fatalf("insert canonical kind %q: %v", kind, err)
		}
	}
	for _, kind := range []string{"progress", "question", "answer", "amend", "system"} {
		if _, err := s.DB().ExecContext(ctx, `
			INSERT INTO task_handoff_entries (handoff_id, kind, body, created_at)
			VALUES (?, ?, ?, ?)
		`, handoff.ID, kind, "removed "+kind, "2026-09-06T00:00:00Z"); err == nil {
			t.Fatalf("removed kind %q unexpectedly inserted", kind)
		}
	}
}

func TestHandoffEntryReplyMustReferenceAnEntryInTheSameHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskIDs := addTestTasks(t, s, 2)
	addLiveParentGoalClaim(t, s, taskIDs[0], "contract-reply-requester")
	addTestAgentSession(t, s, "contract-reply-receiver")
	firstHandoff, err := s.RequestTaskHandoff(ctx, "contract-reply-first", taskIDs[0], testSessionID("contract-reply-requester"), "first")
	if err != nil {
		t.Fatalf("RequestTaskHandoff first: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, firstHandoff.ID, taskIDs[0], testSessionID("contract-reply-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff first: %v", err)
	}
	secondHandoff, err := s.RequestTaskHandoff(ctx, "contract-reply-second", taskIDs[1], testSessionID("contract-reply-requester"), "second")
	if err != nil {
		t.Fatalf("RequestTaskHandoff second: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, secondHandoff.ID, taskIDs[1], testSessionID("contract-reply-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff second: %v", err)
	}

	var firstID int64
	if err := s.DB().QueryRowContext(ctx, `
		INSERT INTO task_handoff_entries (handoff_id, kind, body, author_session_id, created_at)
		VALUES (?, 'review_requested', 'first review', ?, ?)
		RETURNING id
	`, firstHandoff.ID, testSessionID("contract-reply-receiver"), "2026-09-06T00:00:00Z").Scan(&firstID); err != nil {
		t.Fatalf("insert first entry: %v", err)
	}
	if firstID <= 0 {
		t.Fatalf("first entry id = %d, want positive auto-increment id", firstID)
	}
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_handoff_entries (handoff_id, kind, body, author_session_id, in_reply_to_id, created_at)
		VALUES (?, 'review_received', 'same handoff reply', ?, ?, ?)
	`, firstHandoff.ID, testSessionID("contract-reply-receiver"), firstID, "2026-09-06T00:00:01Z"); err != nil {
		t.Fatalf("same-handoff reply: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_handoff_entries (handoff_id, kind, body, author_session_id, in_reply_to_id, created_at)
		VALUES (?, 'review_received', 'cross handoff reply', ?, ?, ?)
	`, secondHandoff.ID, testSessionID("contract-reply-receiver"), firstID, "2026-09-06T00:00:02Z"); err == nil {
		t.Fatal("cross-handoff reply unexpectedly succeeded")
	}
}

func TestOpenMigratesLegacyHandoffEntriesByDroppingRemovedKinds(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy-entry-migration.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	raw.SetMaxOpenConns(1)

	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		raw.Close()
		t.Fatalf("load embedded migrations: %v", err)
	}
	for _, migration := range migrations {
		if _, err := raw.Exec(migration.sql); err != nil {
			raw.Close()
			t.Fatalf("apply fixture migration %s: %v", migration.filename, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, migration.filename, "2026-09-06T00:00:00Z"); err != nil {
			raw.Close()
			t.Fatalf("record fixture migration %s: %v", migration.filename, err)
		}
		if migration.filename == "0023_handoff_entries.sql" {
			break
		}
	}
	if _, err := raw.Exec(`PRAGMA user_version = 6`); err != nil {
		raw.Close()
		t.Fatalf("set fixture schema version: %v", err)
	}
	if _, err := raw.Exec(`
		INSERT INTO projects (id, name, root_path, created_at) VALUES (1, 'legacy-entry', '/legacy-entry', '2026-09-06T00:00:00Z');
		INSERT INTO goals (id, project_id, content, status, created_at, updated_at) VALUES (1, 1, 'legacy entry goal', 'active', '2026-09-06T00:00:00Z', '2026-09-06T00:00:00Z');
		INSERT INTO tasks (id, goal_id, title, status, declare_key, created_at, updated_at) VALUES (1, 1, 'legacy entry task', 'todo', 'legacy-entry-task', '2026-09-06T00:00:00Z', '2026-09-06T00:00:00Z');
		INSERT INTO agent_sessions (id, project_id, registered_at) VALUES (1, 1, '2026-09-06T00:00:00Z'), (2, 1, '2026-09-06T00:00:01Z');
		INSERT INTO task_handoffs (id, task_id, requested_by, received_by, requested_at, received_at)
		VALUES ('legacy-entry-migration-task', 1, 1, 2, '2026-09-06T00:01:00Z', '2026-09-06T00:02:00Z');
		INSERT INTO task_handoff_entries (entry_id, handoff_id, sequence, kind, body, author_session_id, created_at)
		VALUES
			('legacy-request', 'legacy-entry-migration-task', 1, 'request', 'request body', 1, '2026-09-06T00:01:00Z'),
			('legacy-received', 'legacy-entry-migration-task', 2, 'received', 'received body', 2, '2026-09-06T00:02:00Z'),
			('legacy-progress', 'legacy-entry-migration-task', 3, 'progress', 'progress body', 2, '2026-09-06T00:02:01Z'),
			('legacy-question', 'legacy-entry-migration-task', 4, 'question', 'question body', 2, '2026-09-06T00:02:02Z'),
			('legacy-answer', 'legacy-entry-migration-task', 5, 'answer', 'answer body', 1, '2026-09-06T00:02:03Z'),
			('legacy-review-request', 'legacy-entry-migration-task', 6, 'review_request', 'review request body', 2, '2026-09-06T00:02:04Z'),
			('legacy-review-response', 'legacy-entry-migration-task', 7, 'review_response', 'review response body', 1, '2026-09-06T00:02:05Z'),
			('legacy-amend', 'legacy-entry-migration-task', 8, 'amend', 'amend body', 2, '2026-09-06T00:02:06Z'),
			('legacy-system', 'legacy-entry-migration-task', 9, 'system', 'system body', NULL, '2026-09-06T00:02:07Z'),
			('legacy-complete', 'legacy-entry-migration-task', 10, 'complete', 'complete body', 2, '2026-09-06T00:03:00Z');
	`); err != nil {
		raw.Close()
		t.Fatalf("insert legacy entries: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open legacy database: %v", err)
	}
	defer s.Close()

	rows, err := s.DB().QueryContext(ctx, `
		SELECT kind, body
		FROM task_handoff_entries
		WHERE handoff_id = ?
		ORDER BY id
	`, "legacy-entry-migration-task")
	if err != nil {
		t.Fatalf("query migrated entries: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var kind, body string
		if err := rows.Scan(&kind, &body); err != nil {
			t.Fatalf("scan migrated entry: %v", err)
		}
		got = append(got, kind+":"+body)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated entries: %v", err)
	}
	want := []string{
		"request:request body",
		"received:received body",
		"review_requested:review request body",
		"review_received:review response body",
		"completed:complete body",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated entries = %v, want %v", got, want)
	}

	var oldColumnCount int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pragma_table_info('task_handoff_entries')
		WHERE name IN ('entry_id', 'sequence', 'relates_to', 'source')
	`).Scan(&oldColumnCount); err != nil {
		t.Fatalf("check removed columns: %v", err)
	}
	if oldColumnCount != 0 {
		t.Fatalf("legacy columns remaining after migration = %d, want 0", oldColumnCount)
	}
}

func TestOpenBackfillsLegacyReportsAsCanonicalEntries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-entry-contract.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	raw.SetMaxOpenConns(1)

	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		raw.Close()
		t.Fatalf("load embedded migrations: %v", err)
	}
	for _, migration := range migrations {
		if migration.filename == "0023_handoff_entries.sql" {
			break
		}
		if _, err := raw.Exec(migration.sql); err != nil {
			raw.Close()
			t.Fatalf("apply fixture migration %s: %v", migration.filename, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, migration.filename, "2026-09-06T00:00:00Z"); err != nil {
			raw.Close()
			t.Fatalf("record fixture migration %s: %v", migration.filename, err)
		}
	}
	if _, err := raw.Exec(`PRAGMA user_version = 6`); err != nil {
		raw.Close()
		t.Fatalf("set fixture schema version: %v", err)
	}
	if _, err := raw.Exec(`
		INSERT INTO projects (id, name, root_path, created_at) VALUES (1, 'legacy', '/legacy', '2026-09-06T00:00:00Z');
		INSERT INTO goals (id, project_id, content, status, created_at, updated_at) VALUES (1, 1, 'legacy goal', 'active', '2026-09-06T00:00:00Z', '2026-09-06T00:00:00Z');
		INSERT INTO tasks (id, goal_id, title, status, declare_key, created_at, updated_at) VALUES (1, 1, 'legacy task', 'todo', 'legacy-task', '2026-09-06T00:00:00Z', '2026-09-06T00:00:00Z');
		INSERT INTO agent_sessions (id, project_id, registered_at) VALUES (1, 1, '2026-09-06T00:00:00Z'), (2, 1, '2026-09-06T00:00:01Z');
		INSERT INTO task_handoffs (id, task_id, requested_by, received_by, requested_at, received_at, completed_report_at, request_report, complete_report)
		VALUES ('legacy-entry-task', 1, 1, 2, '2026-09-06T00:01:00Z', '2026-09-06T00:02:00Z', '2026-09-06T00:03:00Z', 'legacy request', 'legacy complete');
	`); err != nil {
		raw.Close()
		t.Fatalf("insert legacy rows: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open legacy database: %v", err)
	}
	defer s.Close()

	rows, err := s.DB().QueryContext(context.Background(), `
		SELECT id, kind, body
		FROM task_handoff_entries
		WHERE handoff_id = ?
		ORDER BY id
	`, "legacy-entry-task")
	if err != nil {
		t.Fatalf("query backfilled entries: %v", err)
	}
	defer rows.Close()
	var got []struct {
		id   int64
		kind string
		body string
	}
	for rows.Next() {
		var entry struct {
			id   int64
			kind string
			body string
		}
		if err := rows.Scan(&entry.id, &entry.kind, &entry.body); err != nil {
			t.Fatalf("scan backfilled entry: %v", err)
		}
		got = append(got, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate backfilled entries: %v", err)
	}
	if len(got) != 2 || got[0].kind != "request" || got[0].body != "legacy request" || got[1].kind != "completed" || got[1].body != "legacy complete" {
		t.Fatalf("backfilled entries = %+v, want request/completed reports", got)
	}
	if got[0].id <= 0 || got[1].id <= got[0].id {
		t.Fatalf("backfilled ids = %d,%d, want increasing positive auto-increment ids", got[0].id, got[1].id)
	}
}
