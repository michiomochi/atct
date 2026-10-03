package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func reviewTS(sec int) string {
	return time.Date(2026, 10, 1, 0, 0, sec, 0, time.UTC).Format(timestampLayout)
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// entry inserts a review entry row straight into the entry table.
func reviewEntry(t *testing.T, db *sql.DB, table, handoff, kind, body string, author int64, sec int) {
	t.Helper()
	mustExec(t, db, fmt.Sprintf(`INSERT INTO %s (handoff_id, kind, body, author_session_id, created_at) VALUES (?, ?, ?, ?, ?)`, table),
		handoff, kind, body, author, reviewTS(sec))
}

func TestListReviewExchanges(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	db := s.DB()
	goalID := newTestGoal(t, s)
	otherGoal, err := s.CreateGoal(ctx, 1, "other", "human")
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.CreateTasks(ctx, goalID, "a", "k1", []string{"t1"}, []string{"d"})
	if err != nil {
		t.Fatal(err)
	}
	otherTasks, err := s.CreateTasks(ctx, otherGoal.ID, "a", "k2", []string{"t2"}, []string{"d"})
	if err != nil {
		t.Fatal(err)
	}
	taskID, otherTaskID := tasks[0].ID, otherTasks[0].ID
	addTestAgentSession(t, s, "rx-a")
	a := testSessionID("rx-a")

	// goal stream: an old (recovered) handoff rejected, then a new handoff submits.
	mustExec(t, db, `INSERT INTO goal_handoffs (id, goal_id, completed_report_at) VALUES ('g-old', ?, ?), ('g-new', ?, NULL)`, goalID, reviewTS(3), goalID)
	reviewEntry(t, db, "goal_handoff_entries", "g-old", "review_requested", "g1", a, 1)
	reviewEntry(t, db, "goal_handoff_entries", "g-old", "review_rejected", "fix g", a, 2)
	reviewEntry(t, db, "goal_handoff_entries", "g-new", "review_requested", "g2", a, 4)
	// plan stream: rejection with no later submission.
	mustExec(t, db, `INSERT INTO plan_handoffs (id, goal_id) VALUES ('p1', ?)`, goalID)
	reviewEntry(t, db, "plan_handoff_entries", "p1", "review_requested", "p", a, 1)
	reviewEntry(t, db, "plan_handoff_entries", "p1", "review_rejected", "fix p", a, 3)
	// task stream, plus a foreign task that must never show up.
	mustExec(t, db, `INSERT INTO task_handoffs (id, task_id) VALUES ('t-1', ?), ('t-x', ?)`, taskID, otherTaskID)
	reviewEntry(t, db, "task_handoff_entries", "t-1", "review_rejected", "fix t", a, 5)
	reviewEntry(t, db, "task_handoff_entries", "t-1", "review_requested", "t", a, 6)
	reviewEntry(t, db, "task_handoff_entries", "t-x", "review_rejected", "foreign", a, 5)
	// human rejection (answered_at) and a withdrawn one with no answered_at.
	mustExec(t, db, `INSERT INTO decisions (goal_id, kind, question, status, answer_label, answer_text, answered_at, agent_session_id, created_at)
VALUES (?, 'goal_review', 'q', 'applied', 'reject', 'human no', ?, 0, ?),
       (?, 'goal_review', 'q', 'withdrawn', '', 'gone', NULL, 0, ?)`,
		goalID, reviewTS(3), reviewTS(0), goalID, reviewTS(7))
	// legacy history: a handoff predating exchange recording.
	mustExec(t, db, `INSERT INTO handoff_history_gaps (handoff_id, scope) VALUES ('g-old', 'goal'), ('t-1', 'task'), ('p1', 'plan')`)

	type row struct {
		scope, source, reason, response string
		task                            int64
	}
	got := func(h ReviewExchangeHistory) []row {
		var out []row
		for _, e := range h.Exchanges {
			r := row{scope: e.Scope, source: e.Rejection.Source, reason: e.Rejection.Reason, task: e.TaskID}
			if e.Response != nil {
				r.response = e.Response.Report
			}
			out = append(out, r)
		}
		return out
	}

	all, err := s.ListReviewExchanges(ctx, goalID, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []row{
		{"goal", "handoff", "fix g", "g2", 0},  // t=2, old handoff rejection answered by the new handoff
		{"goal", "human", "human no", "g2", 0}, // t=3
		{"plan", "handoff", "fix p", "", 0},    // t=3, never answered
		{"task", "handoff", "fix t", "t", taskID},
		{"goal", "withdrawn", "gone", "", 0}, // t=7 from created_at
	}
	if g := got(all); fmt.Sprint(g) != fmt.Sprint(want) {
		t.Fatalf("exchanges = %+v\nwant %+v", g, want)
	}
	if all.Exchanges[0].Response.HandoffID != "g-new" || all.Exchanges[1].Rejection.DecisionID == 0 {
		t.Fatalf("ids not carried: %+v", all.Exchanges[:2])
	}
	if len(all.Gaps) != 3 || all.Gaps[0].HandoffID != "g-old" || !all.Gaps[0].BeforeAt.Equal(time.Date(2026, 10, 1, 0, 0, 1, 0, time.UTC)) {
		t.Fatalf("gaps = %+v", all.Gaps)
	}

	only, err := s.ListReviewExchanges(ctx, goalID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if g := got(only); fmt.Sprint(g) != fmt.Sprint([]row{{"task", "handoff", "fix t", "t", taskID}}) {
		t.Fatalf("task-only exchanges = %+v", g)
	}
	if len(only.Gaps) != 1 || only.Gaps[0].Scope != "task" || only.Gaps[0].TaskID != taskID {
		t.Fatalf("task-only gaps = %+v", only.Gaps)
	}

	if _, err := s.ListReviewExchanges(ctx, goalID, otherTaskID); !errors.Is(err, ErrTaskHandoffTaskMismatch) {
		t.Fatalf("foreign task error = %v", err)
	}
	if _, err := s.ListReviewExchanges(ctx, 9999, 0); !errors.Is(err, ErrGoalNotFound) {
		t.Fatalf("missing goal error = %v", err)
	}
	blank, err := s.CreateGoal(ctx, 1, "blank", "human")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.ListReviewExchanges(ctx, blank.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Exchanges == nil || empty.Gaps == nil || len(empty.Exchanges)+len(empty.Gaps) != 0 {
		t.Fatalf("empty history must be [] not nil: %+v", empty)
	}
}

func TestPlanHandoffReviewAppendsEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("rx-plan-commander")
	subID := testSessionID("rx-plan-sub")
	addLiveProjectClaim(t, s, goalID, "rx-plan-commander")
	addTestAgentSession(t, s, "rx-plan-sub")
	gh, err := s.RequestGoalHandoff(ctx, "rx-plan-goal", goalID, commanderID, "delegate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, gh.ID, goalID, subID); err != nil {
		t.Fatal(err)
	}
	setPlanReviewGoalArtifacts(t, s, goalID)
	if _, err := s.RequestPlanHandoffReview(ctx, "rx-plan", goalID, subID, "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, "rx-plan", goalID, commanderID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RejectPlanHandoffReview(ctx, "rx-plan", goalID, commanderID, "revise"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB().Query(`SELECT kind, body, author_session_id FROM plan_handoff_entries WHERE handoff_id = 'rx-plan' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var kind, body string
		var author int64
		if err := rows.Scan(&kind, &body, &author); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s/%s/%d", kind, body, author))
	}
	want := []string{fmt.Sprintf("review_requested/ready/%d", subID), fmt.Sprintf("review_rejected/revise/%d", commanderID)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("plan entries = %v, want %v", got, want)
	}
	h, err := s.ListReviewExchanges(ctx, goalID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Exchanges) != 1 || h.Exchanges[0].Scope != "plan" || h.Exchanges[0].Response != nil {
		t.Fatalf("plan exchange = %+v", h.Exchanges)
	}
}

// TestReviewExchangeMigration builds a database just before 0050 and checks the
// backfill: rows become entries, existing entries are not doubled, blank bodies
// are skipped, every handoff that had been submitted is recorded as a gap, and
// running the backfill again changes nothing.
func TestReviewExchangeMigration(t *testing.T) {
	db := openMigrationTestDB(t)
	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	const target = "0050_review_exchange.sql"
	var targetSQL string
	for _, m := range migrations {
		if m.filename == target {
			targetSQL = m.sql
			break
		}
		mustExec(t, db, m.sql)
		mustExec(t, db, `INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, m.filename, "2026-10-01T00:00:00Z")
	}
	if targetSQL == "" {
		t.Fatalf("%s not embedded", target)
	}
	mustExec(t, db, `PRAGMA user_version = 6`)
	mustExec(t, db, `
INSERT INTO projects (id, name, root_path, created_at) VALUES (1, 'p', '/p', '2026-10-01T00:00:00Z');
INSERT INTO goals (id, project_id, content, status, creator, created_at, updated_at) VALUES (1, 1, 'g', 'active', 'human', '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z');
INSERT INTO tasks (id, goal_id, title, status, declare_key, created_at, updated_at) VALUES (1, 1, 't', 'todo', 'k', '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z');
INSERT INTO agent_sessions (id, registered_at, session_key) VALUES (5, '2026-10-01T00:00:00Z', 'a'), (6, '2026-10-01T00:00:00Z', 'b');
INSERT INTO goal_handoffs (id, goal_id, completed_report_at, review_requested_by, review_requested_at, review_request_report, review_received_by, review_rejected_at, review_reject_report)
VALUES ('g1', 1, '2026-10-01T00:00:09Z', 5, '2026-10-01T00:00:01Z', 'req', 6, '2026-10-01T00:00:02Z', 'rej'),
       ('g2', 1, '2026-10-01T00:00:09Z', 5, '2026-10-01T00:00:03Z', '  ', NULL, NULL, NULL),
       ('g3', 1, NULL, NULL, NULL, NULL, NULL, NULL, NULL);
INSERT INTO goal_handoff_entries (handoff_id, kind, body, author_session_id, created_at) VALUES ('g1', 'review_requested', 'already', 5, '2026-10-01T00:00:01Z');
INSERT INTO task_handoffs (id, task_id, review_requested_by, review_requested_at, review_request_report)
VALUES ('t1', 1, 5, '2026-10-01T00:00:04Z', 'treq');
INSERT INTO plan_handoffs (id, goal_id, review_requested_by, review_requested_at, review_request_report, review_received_by, review_rejected_at, review_reject_report)
VALUES ('p1', 1, 5, '2026-10-01T00:00:05Z', 'preq', 6, '2026-10-01T00:00:06Z', 'prej');
`)

	count := func(q string) int {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	snapshot := func() []int {
		return []int{
			count(`SELECT COUNT(*) FROM goal_handoff_entries`),
			count(`SELECT COUNT(*) FROM task_handoff_entries`),
			count(`SELECT COUNT(*) FROM plan_handoff_entries`),
			count(`SELECT COUNT(*) FROM handoff_history_gaps`),
		}
	}
	mustExec(t, db, targetSQL)
	// goal: g1 keeps its existing request entry and gains the rejection; g2's blank body is skipped.
	// task: t1 request. plan: p1 request + rejection. gaps: g1, g2, t1, p1.
	if got, want := snapshot(), []int{2, 1, 2, 4}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("counts (goal, task, plan, gaps) = %v, want %v", got, want)
	}
	if n := count(`SELECT COUNT(*) FROM goal_handoff_entries WHERE handoff_id='g1' AND kind='review_rejected' AND body='rej' AND author_session_id=6 AND created_at='2026-10-01T00:00:02Z'`); n != 1 {
		t.Fatalf("goal rejection entry not backfilled correctly")
	}
	if n := count(`SELECT COUNT(*) FROM handoff_history_gaps WHERE handoff_id='g3'`); n != 0 {
		t.Fatalf("never-submitted handoff must not be a gap")
	}
	// Idempotent: running the whole migration again adds nothing.
	before := snapshot()
	mustExec(t, db, targetSQL)
	if after := snapshot(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("rerun changed counts: before=%v after=%v", before, after)
	}
}
