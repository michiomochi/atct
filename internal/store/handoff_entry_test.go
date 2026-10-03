package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenCreatesTaskAndGoalHandoffEntryTables(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, table := range []string{"task_handoff_entries", "goal_handoff_entries"} {
		var name string
		if err := s.DB().QueryRowContext(ctx, `
			SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?
		`, table).Scan(&name); err != nil {
			t.Fatalf("handoff entry table %q is missing: %v", table, err)
		}
	}
}

func TestTaskHandoffTransitionsAppendEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "entry-requester")
	addTestAgentSession(t, s, "entry-receiver")

	handoff, err := s.RequestTaskHandoff(ctx, "entry-task-handoff", taskID, testSessionID("entry-requester"), "please take this task")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("entry-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, handoff.ID, taskID, "task completed"); err != nil {
		t.Fatalf("CompleteTaskHandoff: %v", err)
	}

	rows, err := s.DB().QueryContext(ctx, `
		SELECT id, kind, body, author_session_id
		FROM task_handoff_entries
		WHERE handoff_id = ?
		ORDER BY id
	`, handoff.ID)
	if err != nil {
		t.Fatalf("query task handoff entries: %v", err)
	}
	defer rows.Close()

	type entry struct {
		id     int64
		kind   string
		body   string
		author sql.NullInt64
	}
	var got []entry
	for rows.Next() {
		var item entry
		if err := rows.Scan(&item.id, &item.kind, &item.body, &item.author); err != nil {
			t.Fatalf("scan task handoff entry: %v", err)
		}
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task handoff entries: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("task handoff entries = %+v, want request/received/completed", got)
	}
	wantKinds := []string{HandoffEntryKindRequest, HandoffEntryKindReceived, HandoffEntryKindCompleted}
	wantBodies := []string{"please take this task", "received", "task completed"}
	wantAuthors := []int64{testSessionID("entry-requester"), testSessionID("entry-receiver"), testSessionID("entry-receiver")}
	for i, item := range got {
		if item.id != int64(i+1) || item.kind != wantKinds[i] || item.body != wantBodies[i] {
			t.Fatalf("entry[%d] = %+v, want id=%d kind=%q body=%q", i, item, i+1, wantKinds[i], wantBodies[i])
		}
		if !item.author.Valid || item.author.Int64 != wantAuthors[i] {
			t.Fatalf("entry[%d] author = %+v, want %d", i, item.author, wantAuthors[i])
		}
	}
}

func TestGoalHandoffTransitionsAppendEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-entry-requester")
	addTestAgentSession(t, s, "goal-entry-receiver")

	handoff, err := s.RequestGoalHandoff(ctx, "entry-goal-handoff", goalID, testSessionID("goal-entry-requester"), "please take this goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	received, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("goal-entry-receiver"))
	if err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if len(received.Entries) != 2 || received.HasMore || received.NextCursor != 2 {
		t.Fatalf("received goal history = entries:%d has_more:%t next_cursor:%d, want 2/false/2", len(received.Entries), received.HasMore, received.NextCursor)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID("goal-entry-receiver"), "ready for review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID("goal-entry-requester")); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	finalizeGoalHandoffByReviewForTest(t, s, ctx, goalID, testSessionID("goal-entry-requester"))

	rows, err := s.DB().QueryContext(ctx, `
		SELECT id, kind, body
		FROM goal_handoff_entries
		WHERE handoff_id = ?
		ORDER BY id
	`, handoff.ID)
	if err != nil {
		t.Fatalf("query goal handoff entries: %v", err)
	}
	defer rows.Close()

	var got []struct {
		id   int64
		kind string
		body string
	}
	for rows.Next() {
		var item struct {
			id   int64
			kind string
			body string
		}
		if err := rows.Scan(&item.id, &item.kind, &item.body); err != nil {
			t.Fatalf("scan goal handoff entry: %v", err)
		}
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate goal handoff entries: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("goal handoff entries = %+v, want request/received/review_requested/review_received/completed", got)
	}
	wantKinds := []string{HandoffEntryKindRequest, HandoffEntryKindReceived, HandoffEntryKindReviewRequested, HandoffEntryKindReviewReceived, HandoffEntryKindCompleted}
	wantBodies := []string{"please take this goal", "received", "ready for review", "received", "ready for review"}
	for i, item := range got {
		if item.id != int64(i+1) || item.kind != wantKinds[i] || item.body != wantBodies[i] {
			t.Fatalf("entry[%d] = %+v, want id=%d kind=%q body=%q", i, item, i+1, wantKinds[i], wantBodies[i])
		}
	}
}

func TestAppendTaskHandoffEntryValidatesParticipantsAndRelations(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "append-requester")
	addTestAgentSession(t, s, "append-receiver")
	addTestAgentSession(t, s, "append-outsider")

	handoff, err := s.RequestTaskHandoff(ctx, "append-task-handoff", taskID, testSessionID("append-requester"), "take this task")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("append-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	first, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewRequested, "  first review  ", testSessionID("append-receiver"), "")
	if err != nil {
		t.Fatalf("AppendTaskHandoffEntry: %v", err)
	}
	if first.Body != "first review" {
		t.Fatalf("entry body = %q, want trimmed body", first.Body)
	}
	if first.ID <= 0 || first.EntryID != fmt.Sprint(first.ID) {
		t.Fatalf("entry id = %d (compatibility alias %q), want a positive integer id", first.ID, first.EntryID)
	}

	if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewReceived, "review", testSessionID("append-outsider"), ""); !errors.Is(err, ErrHandoffEntryParticipant) {
		t.Fatalf("outsider append error = %v, want ErrHandoffEntryParticipant", err)
	}
	if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindRequest, "reserved", testSessionID("append-receiver"), ""); !errors.Is(err, ErrHandoffEntryKindInvalid) {
		t.Fatalf("reserved append error = %v, want ErrHandoffEntryKindInvalid", err)
	}
	if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewReceived, "missing relation", testSessionID("append-receiver"), "missing-entry"); !errors.Is(err, ErrHandoffEntryRelatesToInvalid) {
		t.Fatalf("missing relation error = %v, want ErrHandoffEntryRelatesToInvalid", err)
	}
	if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewReceived, "reply", testSessionID("append-receiver"), first.EntryID); err != nil {
		t.Fatalf("valid relation append: %v", err)
	}

	if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewReceived, "\t \n", testSessionID("append-receiver"), ""); !errors.Is(err, ErrHandoffEntryBodyEmpty) {
		t.Fatalf("blank body error = %v, want ErrHandoffEntryBodyEmpty", err)
	}
	if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewReceived, strings.Repeat("x", HandoffEntryBodyMaxBytes+1), testSessionID("append-receiver"), ""); !errors.Is(err, ErrHandoffEntryBodyTooLarge) {
		t.Fatalf("oversized body error = %v, want ErrHandoffEntryBodyTooLarge", err)
	}
}

func TestReceiveTaskHandoffReturnsInitialHistoryPage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "receive-history-requester")
	addTestAgentSession(t, s, "receive-history-receiver")

	handoff, err := s.RequestTaskHandoff(ctx, "receive-history-task-handoff", taskID, testSessionID("receive-history-requester"), "request body")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	received, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("receive-history-receiver"))
	if err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	if len(received.Entries) != 2 {
		t.Fatalf("received history entries = %+v, want request and received", received.Entries)
	}
	if received.Entries[0].Kind != HandoffEntryKindRequest || received.Entries[1].Kind != HandoffEntryKindReceived {
		t.Fatalf("received history kinds = %+v, want request/received", received.Entries)
	}
	if received.HasMore || received.NextCursor != 2 {
		t.Fatalf("received history page = has_more:%t next_cursor:%d, want false/2", received.HasMore, received.NextCursor)
	}

	for i := 0; i < 3; i++ {
		if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewReceived, "review", testSessionID("receive-history-receiver"), ""); err != nil {
			t.Fatalf("AppendTaskHandoffEntry[%d]: %v", i, err)
		}
	}
	page, err := s.ListTaskHandoffEntries(ctx, handoff.ID, 2, 2)
	if err != nil {
		t.Fatalf("ListTaskHandoffEntries: %v", err)
	}
	if len(page.Entries) != 2 || !page.HasMore || page.NextCursor != 4 {
		t.Fatalf("history page = %+v, want two entries/more/4", page)
	}
	if page.Entries[0].ID != 3 || page.Entries[1].ID != 4 {
		t.Fatalf("history page ids = %d,%d, want 3,4", page.Entries[0].ID, page.Entries[1].ID)
	}
	last, err := s.ListTaskHandoffEntries(ctx, handoff.ID, page.NextCursor, 2)
	if err != nil {
		t.Fatalf("ListTaskHandoffEntries after cursor: %v", err)
	}
	if len(last.Entries) != 1 || last.HasMore || last.NextCursor != 5 {
		t.Fatalf("last history page = %+v, want one entry/no more/5", last)
	}
}

func TestListHandoffEntriesRejectsUnknownHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.ListTaskHandoffEntries(ctx, "missing-task-handoff", 0, 1); !errors.Is(err, ErrHandoffEntryNotFound) {
		t.Fatalf("unknown task handoff history error = %v, want ErrHandoffEntryNotFound", err)
	}
	if _, err := s.ListGoalHandoffEntries(ctx, "missing-goal-handoff", 0, 1); !errors.Is(err, ErrHandoffEntryNotFound) {
		t.Fatalf("unknown goal handoff history error = %v, want ErrHandoffEntryNotFound", err)
	}
}

func TestOpenBackfillsLegacyHandoffReportsIntoCanonicalEntries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-handoffs.db")
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
		if migration.filename == "0048_handoff_entries.sql" {
			break
		}
		if _, err := raw.Exec(migration.sql); err != nil {
			raw.Close()
			t.Fatalf("apply fixture migration %s: %v", migration.filename, err)
		}
		if migration.number > 0 {
			if _, err := raw.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, migration.filename, "2026-09-05T00:00:00Z"); err != nil {
				raw.Close()
				t.Fatalf("record fixture migration %s: %v", migration.filename, err)
			}
		}
	}
	if _, err := raw.Exec(`PRAGMA user_version = 7`); err != nil {
		raw.Close()
		t.Fatalf("set fixture schema version: %v", err)
	}
	oversized := strings.Repeat("legacy report ", HandoffEntryBodyMaxBytes)
	if _, err := raw.Exec(`
		INSERT INTO projects (id, name, root_path, created_at) VALUES (1, 'legacy', '/legacy', '2026-09-05T00:00:00Z');
		INSERT INTO goals (id, project_id, content, status, created_at, updated_at) VALUES (1, 1, 'legacy goal', 'active', '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z');
		INSERT INTO tasks (id, goal_id, title, status, declare_key, created_at, updated_at) VALUES (1, 1, 'legacy task', 'todo', 'legacy-task', '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z');
		INSERT INTO agent_sessions (id, project_id, registered_at) VALUES (1, 1, '2026-09-05T00:00:00Z'), (2, 1, '2026-09-05T00:00:01Z');
		INSERT INTO task_handoffs (id, task_id, requested_by, received_by, requested_at, received_at, completed_report_at, request_report, complete_report)
		VALUES ('legacy-task-handoff', 1, 1, 2, '2026-09-05T00:01:00Z', '2026-09-05T00:02:00Z', '2026-09-05T00:03:00Z', 'legacy request', 'legacy complete');
		INSERT INTO goal_handoffs (id, goal_id, requested_by, received_by, requested_at, received_at, completed_report_at, request_report, complete_report)
		VALUES ('legacy-goal-handoff', 1, 1, 2, '2026-09-05T00:04:00Z', '2026-09-05T00:05:00Z', '2026-09-05T00:06:00Z', '', 'legacy goal complete');
		INSERT INTO task_handoffs (id, task_id, requested_by, received_by, requested_at, complete_report, completed_report_at)
		VALUES ('legacy-oversized-handoff', 1, 1, 2, '2026-09-05T00:07:00Z', ?, '2026-09-05T00:08:00Z');
	`, oversized); err != nil {
		raw.Close()
		t.Fatalf("insert legacy handoff rows: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open legacy database: %v", err)
	}
	defer s.Close()

	var taskKinds []string
	rows, err := s.DB().QueryContext(context.Background(), `SELECT kind FROM task_handoff_entries WHERE handoff_id = ? ORDER BY id`, "legacy-task-handoff")
	if err != nil {
		t.Fatalf("query backfilled task entries: %v", err)
	}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			rows.Close()
			t.Fatalf("scan backfilled task kind: %v", err)
		}
		taskKinds = append(taskKinds, kind)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close backfilled task entries: %v", err)
	}
	if !reflect.DeepEqual(taskKinds, []string{HandoffEntryKindRequest, HandoffEntryKindCompleted}) {
		t.Fatalf("backfilled task kinds = %v, want request/completed", taskKinds)
	}

	var goalBody string
	if err := s.DB().QueryRowContext(context.Background(), `SELECT body FROM goal_handoff_entries WHERE handoff_id = ? AND kind = 'completed'`, "legacy-goal-handoff").Scan(&goalBody); err != nil {
		t.Fatalf("query backfilled goal completed entry: %v", err)
	}
	if goalBody != "legacy goal complete" {
		t.Fatalf("backfilled goal complete body = %q, want normal legacy body", goalBody)
	}

	var oversizedBody string
	if err := s.DB().QueryRowContext(context.Background(), `SELECT body FROM task_handoff_entries WHERE handoff_id = ? AND kind = 'completed'`, "legacy-oversized-handoff").Scan(&oversizedBody); err != nil {
		t.Fatalf("query oversized completed entry: %v", err)
	}
	if oversizedBody != oversized {
		t.Fatalf("oversized completed body length = %d, want %d", len(oversizedBody), len(oversized))
	}

	var entryID int64
	if err := s.DB().QueryRowContext(context.Background(), `SELECT id FROM task_handoff_entries WHERE handoff_id = ? AND kind = 'request'`, "legacy-task-handoff").Scan(&entryID); err != nil {
		t.Fatalf("query task entry id: %v", err)
	}
	firstID := entryID
	if err := s.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer s.Close()
	if err := s.DB().QueryRowContext(context.Background(), `SELECT id FROM task_handoff_entries WHERE handoff_id = ? AND kind = 'request'`, "legacy-task-handoff").Scan(&entryID); err != nil {
		t.Fatalf("query id after reopen: %v", err)
	}
	if entryID != firstID {
		t.Fatalf("backfilled entry id changed from %d to %d", firstID, entryID)
	}
}

func TestHandoffEntryAllowsMutationAndRejectsSelfReference(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "immutable-entry-requester")
	addTestAgentSession(t, s, "immutable-entry-receiver")
	handoff, err := s.RequestTaskHandoff(ctx, "immutable-entry-handoff", taskID, testSessionID("immutable-entry-requester"), "request")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("immutable-entry-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	entry, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewRequested, "review requested", testSessionID("immutable-entry-receiver"), "")
	if err != nil {
		t.Fatalf("AppendTaskHandoffEntry: %v", err)
	}

	if _, err := s.DB().ExecContext(ctx, `UPDATE task_handoff_entries SET body = 'changed' WHERE id = ?`, entry.ID); err != nil {
		t.Fatalf("entry update failed: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM task_handoff_entries WHERE id = ?`, entry.ID); err != nil {
		t.Fatalf("entry delete failed: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM task_handoffs WHERE id = ?`, handoff.ID); err == nil {
		t.Fatal("parent handoff delete unexpectedly succeeded while entries exist")
	}

	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_handoff_entries (
			id, handoff_id, kind, body, author_session_id, in_reply_to_id, created_at
		) VALUES (999999, ?, 'review_received', 'self', ?, 999999, ?)
	`, handoff.ID, testSessionID("immutable-entry-receiver"), time.Now().UTC().Format(time.RFC3339Nano)); err == nil {
		t.Fatal("self-referencing entry unexpectedly succeeded")
	}
}

func TestAmendTaskHandoffUpdatesReportAndTerminalThreadRejectsGenericAppend(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "amend-entry-requester")
	addTestAgentSession(t, s, "amend-entry-receiver")
	handoff, err := s.RequestTaskHandoff(ctx, "amend-entry-handoff", taskID, testSessionID("amend-entry-requester"), "request")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("amend-entry-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, handoff.ID, taskID, "complete"); err != nil {
		t.Fatalf("CompleteTaskHandoff: %v", err)
	}
	amended, err := s.AmendTaskHandoffReport(ctx, handoff.ID, taskID, "amended")
	if err != nil {
		t.Fatalf("AmendTaskHandoffReport: %v", err)
	}
	if amended.CompleteReport != "amended" {
		t.Fatalf("amended complete report = %q, want amended", amended.CompleteReport)
	}
	if _, err := s.AppendTaskHandoffEntry(ctx, handoff.ID, HandoffEntryKindReviewRequested, "too late", testSessionID("amend-entry-receiver"), ""); !errors.Is(err, ErrHandoffEntryTerminal) {
		t.Fatalf("terminal generic append error = %v, want ErrHandoffEntryTerminal", err)
	}

	var count int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM task_handoff_entries
		WHERE handoff_id = ? AND kind = 'amend'
	`, handoff.ID).Scan(&count); err != nil {
		t.Fatalf("count amend entries: %v", err)
	}
	if count != 0 {
		t.Fatalf("amend entry count = %d, want 0", count)
	}
}

func TestAmendGoalHandoffUpdatesReportWithoutRemovedEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "amend-goal-requester")
	addTestAgentSession(t, s, "amend-goal-receiver")
	handoff, err := s.RequestGoalHandoff(ctx, "amend-goal-handoff", goalID, testSessionID("amend-goal-requester"), "request")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("amend-goal-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID("amend-goal-receiver"), "ready for review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID("amend-goal-requester")); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	finalizeGoalHandoffByReviewForTest(t, s, ctx, goalID, testSessionID("amend-goal-requester"))
	if _, err := s.AmendGoalHandoffReport(ctx, handoff.ID, goalID, "amended"); err != nil {
		t.Fatalf("AmendGoalHandoffReport: %v", err)
	}

	var count int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM goal_handoff_entries
		WHERE handoff_id = ? AND kind = 'amend' AND body = 'amended'
	`, handoff.ID).Scan(&count); err != nil {
		t.Fatalf("count goal amend entries: %v", err)
	}
	if count != 0 {
		t.Fatalf("goal amend entry count = %d, want 0", count)
	}
}

func TestRequestTaskHandoffRetryDoesNotDuplicateRequestEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "retry-entry-requester")
	if _, err := s.RequestTaskHandoff(ctx, "retry-entry-handoff", taskID, testSessionID("retry-entry-requester"), "request body"); err != nil {
		t.Fatalf("first RequestTaskHandoff: %v", err)
	}
	if _, err := s.RequestTaskHandoff(ctx, "retry-entry-handoff", taskID, testSessionID("retry-entry-requester"), "request body"); err != nil {
		t.Fatalf("retry RequestTaskHandoff: %v", err)
	}
	var count int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM task_handoff_entries
		WHERE handoff_id = ? AND kind = 'request'
	`, "retry-entry-handoff").Scan(&count); err != nil {
		t.Fatalf("count request entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("request entry count after retry = %d, want 1", count)
	}
}

func TestRequestGoalHandoffRetryDoesNotDuplicateRequestEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "retry-goal-entry-requester")
	if _, err := s.RequestGoalHandoff(ctx, "retry-goal-entry-handoff", goalID, testSessionID("retry-goal-entry-requester"), "request body"); err != nil {
		t.Fatalf("first RequestGoalHandoff: %v", err)
	}
	if _, err := s.RequestGoalHandoff(ctx, "retry-goal-entry-handoff", goalID, testSessionID("retry-goal-entry-requester"), "request body"); err != nil {
		t.Fatalf("retry RequestGoalHandoff: %v", err)
	}
	var count int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM goal_handoff_entries
		WHERE handoff_id = ? AND kind = 'request'
	`, "retry-goal-entry-handoff").Scan(&count); err != nil {
		t.Fatalf("count goal request entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("goal request entry count after retry = %d, want 1", count)
	}
}

func TestTerminalHandoffRejectsRequestRetry(t *testing.T) {
	t.Run("task", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		taskID := addTestTasks(t, s, 1)[0]
		addLiveParentGoalClaim(t, s, taskID, "terminal-request-task-requester")
		addTestAgentSession(t, s, "terminal-request-task-receiver")

		const handoffID = "terminal-request-task-handoff"
		if _, err := s.RequestTaskHandoff(ctx, handoffID, taskID, testSessionID("terminal-request-task-requester"), "original request"); err != nil {
			t.Fatalf("first RequestTaskHandoff: %v", err)
		}
		if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, testSessionID("terminal-request-task-receiver")); err != nil {
			t.Fatalf("ReceiveTaskHandoff: %v", err)
		}
		if _, err := s.CompleteTaskHandoff(ctx, handoffID, taskID, "complete"); err != nil {
			t.Fatalf("CompleteTaskHandoff: %v", err)
		}

		if _, err := s.RequestTaskHandoff(ctx, handoffID, taskID, testSessionID("terminal-request-task-requester"), "replacement request"); !errors.Is(err, ErrHandoffEntryTerminal) {
			t.Fatalf("terminal RequestTaskHandoff error = %v, want ErrHandoffEntryTerminal", err)
		}
		persisted, err := s.GetTaskHandoff(ctx, handoffID)
		if err != nil {
			t.Fatalf("GetTaskHandoff: %v", err)
		}
		if persisted.RequestReport != "original request" {
			t.Fatalf("terminal request report = %q, want original request", persisted.RequestReport)
		}
		var requestEntries int
		if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM task_handoff_entries WHERE handoff_id = ? AND kind = 'request'`, handoffID).Scan(&requestEntries); err != nil {
			t.Fatalf("count terminal task request entries: %v", err)
		}
		if requestEntries != 1 {
			t.Fatalf("terminal task request entry count = %d, want 1", requestEntries)
		}
	})

	t.Run("goal", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		goalID := newTestGoal(t, s)
		addLiveProjectClaim(t, s, goalID, "terminal-request-goal-requester")
		addTestAgentSession(t, s, "terminal-request-goal-receiver")

		const handoffID = "terminal-request-goal-handoff"
		if _, err := s.RequestGoalHandoff(ctx, handoffID, goalID, testSessionID("terminal-request-goal-requester"), "original request"); err != nil {
			t.Fatalf("first RequestGoalHandoff: %v", err)
		}
		if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, testSessionID("terminal-request-goal-receiver")); err != nil {
			t.Fatalf("ReceiveGoalHandoff: %v", err)
		}
		if _, err := s.RequestGoalHandoffReview(ctx, handoffID, goalID, testSessionID("terminal-request-goal-receiver"), "ready for review"); err != nil {
			t.Fatalf("RequestGoalHandoffReview: %v", err)
		}
		if _, err := s.ReceiveGoalHandoffReview(ctx, handoffID, goalID, testSessionID("terminal-request-goal-requester")); err != nil {
			t.Fatalf("ReceiveGoalHandoffReview: %v", err)
		}
		finalizeGoalHandoffByReviewForTest(t, s, ctx, goalID, testSessionID("terminal-request-goal-requester"))

		if _, err := s.RequestGoalHandoff(ctx, handoffID, goalID, testSessionID("terminal-request-goal-requester"), "replacement request"); !errors.Is(err, ErrHandoffEntryTerminal) {
			t.Fatalf("terminal RequestGoalHandoff error = %v, want ErrHandoffEntryTerminal", err)
		}
		persisted, err := s.GetGoalHandoff(ctx, handoffID)
		if err != nil {
			t.Fatalf("GetGoalHandoff: %v", err)
		}
		if persisted.RequestReport != "original request" {
			t.Fatalf("terminal goal request report = %q, want original request", persisted.RequestReport)
		}
		var requestEntries int
		if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal_handoff_entries WHERE handoff_id = ? AND kind = 'request'`, handoffID).Scan(&requestEntries); err != nil {
			t.Fatalf("count terminal goal request entries: %v", err)
		}
		if requestEntries != 1 {
			t.Fatalf("terminal goal request entry count = %d, want 1", requestEntries)
		}
	})
}

func TestReceiveTaskHandoffRetryDoesNotDuplicateReceivedEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "retry-receive-requester")
	addTestAgentSession(t, s, "retry-receive-receiver")
	handoff, err := s.RequestTaskHandoff(ctx, "retry-receive-handoff", taskID, testSessionID("retry-receive-requester"), "request")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("retry-receive-receiver")); err != nil {
		t.Fatalf("first ReceiveTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, testSessionID("retry-receive-receiver")); err != nil {
		t.Fatalf("retry ReceiveTaskHandoff: %v", err)
	}
	var count int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM task_handoff_entries
		WHERE handoff_id = ? AND kind = 'received'
	`, handoff.ID).Scan(&count); err != nil {
		t.Fatalf("count received entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("received entry count after retry = %d, want 1", count)
	}
}

func TestReceiveGoalHandoffRetryDoesNotDuplicateReceivedEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "retry-goal-receive-requester")
	addTestAgentSession(t, s, "retry-goal-receive-receiver")
	handoff, err := s.RequestGoalHandoff(ctx, "retry-goal-receive-handoff", goalID, testSessionID("retry-goal-receive-requester"), "request")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("retry-goal-receive-receiver")); err != nil {
		t.Fatalf("first ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("retry-goal-receive-receiver")); err != nil {
		t.Fatalf("retry ReceiveGoalHandoff: %v", err)
	}
	var count int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM goal_handoff_entries
		WHERE handoff_id = ? AND kind = 'received'
	`, handoff.ID).Scan(&count); err != nil {
		t.Fatalf("count goal received entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("goal received entry count after retry = %d, want 1", count)
	}
}

func TestTaskHandoffReclaimAndRequestAreAtomicWhenEntryValidationFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "atomic-reclaim-requester")
	addTestAgentSession(t, s, "atomic-reclaim-receiver")
	if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?`, 999999, "dead", testSessionID("atomic-reclaim-receiver")); err != nil {
		t.Fatalf("make receiver stale: %v", err)
	}
	first, err := s.RequestTaskHandoff(ctx, "atomic-reclaim-first", taskID, testSessionID("atomic-reclaim-requester"), "")
	if err != nil {
		t.Fatalf("first RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, first.ID, taskID, testSessionID("atomic-reclaim-receiver")); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	_, err = s.RequestTaskHandoff(ctx, "atomic-reclaim-second", taskID, testSessionID("atomic-reclaim-requester"), strings.Repeat("x", HandoffEntryBodyMaxBytes+1))
	if !errors.Is(err, ErrHandoffEntryBodyTooLarge) {
		t.Fatalf("oversized request error = %v, want ErrHandoffEntryBodyTooLarge", err)
	}
	persisted, err := s.GetTaskHandoff(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetTaskHandoff after failed reclaim: %v", err)
	}
	if persisted.CompletedReportAt != nil {
		t.Fatalf("stale handoff was reclaimed despite failed request: %+v", persisted)
	}
	var secondCount int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM task_handoffs WHERE id = ?`, "atomic-reclaim-second").Scan(&secondCount); err != nil {
		t.Fatalf("count second handoff: %v", err)
	}
	if secondCount != 0 {
		t.Fatalf("failed second handoff count = %d, want 0", secondCount)
	}
}

func TestGoalHandoffReclaimAndRequestAreAtomicWhenEntryValidationFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "atomic-goal-reclaim-requester")
	addTestAgentSession(t, s, "atomic-goal-reclaim-receiver")
	if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?`, 999999, "dead", testSessionID("atomic-goal-reclaim-receiver")); err != nil {
		t.Fatalf("make goal receiver stale: %v", err)
	}
	first, err := s.RequestGoalHandoff(ctx, "atomic-goal-reclaim-first", goalID, testSessionID("atomic-goal-reclaim-requester"), "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, testSessionID("atomic-goal-reclaim-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	_, err = s.RequestGoalHandoff(ctx, "atomic-goal-reclaim-second", goalID, testSessionID("atomic-goal-reclaim-requester"), strings.Repeat("x", HandoffEntryBodyMaxBytes+1))
	if !errors.Is(err, ErrHandoffEntryBodyTooLarge) {
		t.Fatalf("oversized goal request error = %v, want ErrHandoffEntryBodyTooLarge", err)
	}
	persisted, err := s.GetGoalHandoff(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff after failed reclaim: %v", err)
	}
	if persisted.CompletedReportAt != nil {
		t.Fatalf("stale goal handoff was reclaimed despite failed request: %+v", persisted)
	}
	var secondCount int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal_handoffs WHERE id = ?`, "atomic-goal-reclaim-second").Scan(&secondCount); err != nil {
		t.Fatalf("count second goal handoff: %v", err)
	}
	if secondCount != 0 {
		t.Fatalf("failed second goal handoff count = %d, want 0", secondCount)
	}
}

func TestTaskReleaseAppendsCompleteEntryWithTaskStateTransition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "release-entry-parent")
	addTestAgentSession(t, s, "release-entry-agent")
	ownerID := testSessionID("release-entry-agent")
	if _, err := s.UpdateTask(ctx, taskID, "doing", 0); err != nil {
		t.Fatalf("set task doing: %v", err)
	}
	if _, err := s.ClaimTask(ctx, taskID, ownerID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil || len(handoffs) != 1 {
		t.Fatalf("ListTaskHandoffs after claim = %+v, err=%v", handoffs, err)
	}
	handoffID := handoffs[0].ID
	if _, err := s.DB().ExecContext(ctx, `
		CREATE TRIGGER test_fail_task_handoff_entry
		BEFORE INSERT ON task_handoff_entries
		BEGIN SELECT RAISE(ABORT, 'entry write rejected'); END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if _, err := s.UpdateTask(ctx, taskID, "todo", ownerID); err == nil {
		t.Fatal("UpdateTask release unexpectedly succeeded when entry append failed")
	}
	task, err := s.loadTask(ctx, taskID)
	if err != nil {
		t.Fatalf("load task after failed release: %v", err)
	}
	if task.Status != "doing" {
		t.Fatalf("task status after failed release = %q, want doing", task.Status)
	}
	persisted, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil || persisted.CompletedReportAt != nil {
		t.Fatalf("handoff changed after failed release: %+v, err=%v", persisted, err)
	}
	if _, err := s.DB().ExecContext(ctx, `DROP TRIGGER test_fail_task_handoff_entry`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if _, err := s.UpdateTask(ctx, taskID, "todo", ownerID); err != nil {
		t.Fatalf("UpdateTask release after trigger removal: %v", err)
	}
	var kind, body string
	if err := s.DB().QueryRowContext(ctx, `
		SELECT kind, body FROM task_handoff_entries
		WHERE handoff_id = (SELECT id FROM task_handoffs WHERE task_id = ? ORDER BY id DESC LIMIT 1)
		  AND kind = 'completed'
	`, taskID).Scan(&kind, &body); err != nil {
		t.Fatalf("query released task handoff entry: %v", err)
	}
	if kind != HandoffEntryKindComplete || body != taskHandoffReleasedReport {
		t.Fatalf("released task handoff entry = kind:%q body:%q, want complete/%q", kind, body, taskHandoffReleasedReport)
	}
}

func TestWithdrawActiveGoalAppendsCompleteEntriesForOpenTaskHandoffs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "withdraw-entry", "withdraw-entry", []string{"open"}, []string{"open task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	addTestAgentSession(t, s, "withdraw-entry-requester")
	addTestAgentSession(t, s, "withdraw-entry-receiver")
	const handoffID = "withdraw-entry-handoff"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_handoffs (id, task_id, requested_by, received_by, requested_at, received_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, handoffID, tasks[0].ID, testSessionID("withdraw-entry-requester"), testSessionID("withdraw-entry-receiver"), now, now); err != nil {
		t.Fatalf("insert open task handoff: %v", err)
	}

	const reason = "withdraw with entry"
	if err := s.WithdrawActiveGoal(ctx, goalID, reason); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}
	var kind, body string
	if err := s.DB().QueryRowContext(ctx, `
		SELECT kind, body FROM task_handoff_entries WHERE handoff_id = ?
	`, handoffID).Scan(&kind, &body); err != nil {
		t.Fatalf("query withdrawn task handoff entry: %v", err)
	}
	if kind != HandoffEntryKindComplete || body != reason {
		t.Fatalf("withdrawn task handoff entry = kind:%q body:%q, want complete/%q", kind, body, reason)
	}
}

func TestCompleteHandoffRejectsUnreceivedStateBeforeAppendingEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "complete-unreceived-entry-requester")
	const handoffID = "complete-unreceived-entry-handoff"
	addRequestOnlyTaskHandoff(t, s, handoffID, taskID, "complete-unreceived-entry-requester")

	if _, err := s.CompleteTaskHandoff(ctx, handoffID, taskID, "must not complete"); err == nil {
		t.Fatal("CompleteTaskHandoff unexpectedly completed an unreceived handoff")
	}
	persisted, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetTaskHandoff after rejected completion: %v", err)
	}
	if persisted.CompletedReportAt != nil {
		t.Fatalf("unreceived handoff completed after rejected completion: %+v", persisted)
	}
	var entryCount int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM task_handoff_entries WHERE handoff_id = ?`, handoffID).Scan(&entryCount); err != nil {
		t.Fatalf("count rejected completion entries: %v", err)
	}
	if entryCount != 0 {
		t.Fatalf("rejected completion entry count = %d, want 0", entryCount)
	}
}

func TestTaskHandoffRejectAndAmendAppendEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "entry2-requester")
	addTestAgentSession(t, s, "entry2-receiver")
	reqID, recvID := testSessionID("entry2-requester"), testSessionID("entry2-receiver")

	h, err := s.RequestTaskHandoff(ctx, "entry2-handoff", taskID, reqID, "go")
	if err != nil {
		t.Fatal(err)
	}
	steps := []func() error{
		func() error { _, err := s.ReceiveTaskHandoff(ctx, h.ID, taskID, recvID); return err },
		func() error { _, err := s.RequestTaskHandoffReview(ctx, h.ID, taskID, recvID, "r1"); return err },
		func() error { _, err := s.ReceiveTaskHandoffReview(ctx, h.ID, taskID, reqID); return err },
		func() error { _, err := s.RejectTaskHandoffReview(ctx, h.ID, taskID, reqID, "no"); return err },
		func() error { _, err := s.ReceiveTaskHandoffReviewRejection(ctx, h.ID, taskID, recvID); return err },
		func() error { _, err := s.RequestTaskHandoffReview(ctx, h.ID, taskID, recvID, "r2"); return err },
		func() error { _, err := s.ReceiveTaskHandoffReview(ctx, h.ID, taskID, reqID); return err },
		func() error { _, err := s.CompleteTaskHandoffByReviewer(ctx, h.ID, taskID, reqID, "done"); return err },
		func() error { _, err := s.AmendTaskHandoffReport(ctx, h.ID, taskID, "fixed"); return err },
	}
	wantKinds := []string{"received", "review_requested", "review_received", "review_rejected", "received", "review_requested", "review_received", "completed", "completed"}
	wantBodies := []string{"received", "r1", "received", "no", "review rejection received", "r2", "received", "done", "fixed"}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		page, err := s.ListTaskHandoffEntries(ctx, h.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		last := page.Entries[len(page.Entries)-1]
		if len(page.Entries) != i+2 || last.Kind != wantKinds[i] || last.Body != wantBodies[i] {
			t.Fatalf("step %d: entries=%d last=%s/%q, want %d %s/%q", i, len(page.Entries), last.Kind, last.Body, i+2, wantKinds[i], wantBodies[i])
		}
	}
}
