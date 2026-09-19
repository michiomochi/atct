package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestMigration0045PreservesLegacyNextStepsAndCreatesNextGoals(t *testing.T) {
	db := openMigrationTestDB(t)
	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	found := false
	for _, migration := range migrations {
		if migration.filename == "0045_next_goals.sql" {
			found = true
			break
		}
		if _, err := db.Exec(migration.sql); err != nil {
			t.Fatalf("apply fixture migration %s: %v", migration.filename, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`, migration.filename, "2026-09-19T00:00:00Z"); err != nil {
			t.Fatalf("record fixture migration %s: %v", migration.filename, err)
		}
	}
	if !found {
		t.Fatal("embedded migrations do not contain 0045_next_goals.sql")
	}
	if _, err := db.Exec(`PRAGMA user_version = 6`); err != nil {
		t.Fatalf("set v6 schema version: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO projects (id, name, root_path, created_at)
VALUES (1, 'next goals project', '/next-goals', '2026-09-19T00:00:00Z');
INSERT INTO goals (
  id, project_id, content, status, result_summary, work_done, now_possible,
  how_to_verify, surprises, needs_review, next_steps, created_at, updated_at
)
VALUES
  (1, 1, 'done goal', 'done', 'done work', 'done work', 'done result', 'done verify', 'done surprise', 'done review', 'legacy done prose', '2026-09-19T00:00:00Z', '2026-09-19T00:00:00Z'),
  (2, 1, 'active goal', 'active', '', '', '', '', '', '', 'legacy active prose', '2026-09-19T00:00:00Z', '2026-09-19T00:00:00Z');
CREATE INDEX idx_goals_content_for_next_goals_test ON goals(content);
`); err != nil {
		t.Fatalf("insert v6 fixture rows: %v", err)
	}

	if err := applyEmbeddedMigrations(db); err != nil {
		t.Fatalf("apply 0045 migration: %v", err)
	}
	assertUserVersion(t, db, schemaVersion)
	assertMigrationRecorded(t, db, "0045_next_goals.sql")
	assertTableExists(t, db, "next_goals")
	assertTableExists(t, db, "goal_review_state_snapshots")

	columns := migrationTableColumns(t, db, "goals")
	if _, ok := columns["next_steps"]; ok {
		t.Fatal("goals still exposes next_steps after migration")
	}
	if _, ok := columns["legacy_next_steps"]; !ok {
		t.Fatal("goals is missing legacy_next_steps after migration")
	}

	rows, err := db.Query(`SELECT id, legacy_next_steps FROM goals ORDER BY id`)
	if err != nil {
		t.Fatalf("read migrated legacy next steps: %v", err)
	}
	defer rows.Close()
	want := []struct {
		id   int64
		text string
	}{{1, "legacy done prose"}, {2, "legacy active prose"}}
	for _, expected := range want {
		if !rows.Next() {
			t.Fatalf("missing migrated legacy row %+v", expected)
		}
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			t.Fatalf("scan migrated legacy row: %v", err)
		}
		if id != expected.id || text != expected.text {
			t.Fatalf("migrated legacy row = (%d, %q), want (%d, %q)", id, text, expected.id, expected.text)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read migrated legacy rows: %v", err)
	}
	if rows.Next() {
		t.Fatal("migrated legacy rows contain an unexpected extra row")
	}

	var foreignKeyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list('next_goals')`).Scan(&foreignKeyCount); err != nil {
		t.Fatalf("inspect next_goals foreign keys: %v", err)
	}
	if foreignKeyCount != 2 {
		t.Fatalf("next_goals foreign key count = %d, want 2", foreignKeyCount)
	}
	var indexName string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_goals_content_for_next_goals_test'`).Scan(&indexName); err != nil {
		t.Fatalf("preserved goals index: %v", err)
	}
}

func nextGoalsFixture(t *testing.T) (*Store, context.Context, domain.Goal, domain.Goal, domain.Goal) {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "next-goals", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	source, err := s.CreateGoal(ctx, project.ID, "source goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal source: %v", err)
	}
	first, err := s.CreateGoal(ctx, project.ID, "first target\nfirst body", "human")
	if err != nil {
		t.Fatalf("CreateGoal first: %v", err)
	}
	second, err := s.CreateGoal(ctx, project.ID, "second target", "human")
	if err != nil {
		t.Fatalf("CreateGoal second: %v", err)
	}
	return s, ctx, source, first, second
}

func nextGoalsReport(ids ...int64) domain.CompletionReport {
	return domain.CompletionReport{
		WorkDone:    "work done",
		NowPossible: "now possible",
		HowToVerify: "run store tests",
		Surprises:   "none",
		NeedsReview: "none",
		NextGoalIDs: ids,
	}
}

func requireNextGoalIDs(t *testing.T, s *Store, ctx context.Context, goalID int64, want []int64) {
	t.Helper()
	got, err := s.ListNextGoalIDs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListNextGoalIDs: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("next goal ids = %v, want %v", got, want)
	}
}

func TestNextGoalsPreserveOrderReplaceLinksAndAllowEmptyInput(t *testing.T) {
	s, ctx, source, first, second := nextGoalsFixture(t)

	decision, err := s.CompleteGoalWithReport(ctx, source.ID, nextGoalsReport(second.ID, first.ID), testSessionID("next-goals-replace"))
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{second.ID, first.ID})
	goal, err := s.GetGoal(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got := []int64{goal.NextGoals[0].ID, goal.NextGoals[1].ID}; !reflect.DeepEqual(got, []int64{second.ID, first.ID}) {
		t.Fatalf("goal next-goal summaries = %v, want %v", got, []int64{second.ID, first.ID})
	}

	if err := s.RejectCompletion(ctx, decision.ID, "revise"); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}
	decision, err = s.CompleteGoalWithReport(ctx, source.ID, nextGoalsReport(first.ID), testSessionID("next-goals-replace"))
	if err != nil {
		t.Fatalf("CompleteGoalWithReport replacement: %v", err)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{first.ID})
	if err := s.RejectCompletion(ctx, decision.ID, "clear links"); err != nil {
		t.Fatalf("RejectCompletion replacement: %v", err)
	}
	if _, err := s.CompleteGoalWithReport(ctx, source.ID, nextGoalsReport(), testSessionID("next-goals-replace")); err != nil {
		t.Fatalf("CompleteGoalWithReport empty replacement: %v", err)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, nil)
}

func TestNextGoalsRejectInvalidTargetIDs(t *testing.T) {
	crossProjectStore, ctx, source, first, _ := nextGoalsFixture(t)
	otherProject, err := crossProjectStore.CreateProject(ctx, "other-next-goals", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	other, err := crossProjectStore.CreateGoal(ctx, otherProject.ID, "other target", "human")
	if err != nil {
		t.Fatalf("CreateGoal other: %v", err)
	}
	cases := []struct {
		name string
		ids  []int64
		want error
	}{
		{name: "self", ids: []int64{source.ID}, want: ErrNextGoalSelfReference},
		{name: "duplicate", ids: []int64{first.ID, first.ID}, want: ErrNextGoalDuplicate},
		{name: "unknown", ids: []int64{999999}, want: ErrNextGoalNotFound},
		{name: "cross project", ids: []int64{other.ID}, want: ErrNextGoalProjectMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := crossProjectStore.CompleteGoalWithReport(ctx, source.ID, nextGoalsReport(tc.ids...), testSessionID("next-goals-invalid-"+tc.name))
			if !errors.Is(err, tc.want) {
				t.Fatalf("CompleteGoalWithReport error = %v, want %v", err, tc.want)
			}
			requireNextGoalIDs(t, crossProjectStore, ctx, source.ID, nil)
		})
	}
}

func TestNextGoalsSurviveReviewRejectionAndWithdrawal(t *testing.T) {
	s, ctx, source, first, second := nextGoalsFixture(t)
	requester := "next-goals-review-requester"
	receiver := "next-goals-review-receiver"
	requesterID := testSessionID(requester)
	receiverID := testSessionID(receiver)
	addLiveProjectClaim(t, s, source.ID, requester)
	addTestAgentSession(t, s, receiver)
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "next-goals-review-handoff", source.ID, requesterID, receiverID)

	review, err := s.RequestGoalReview(ctx, source.ID, requesterID, nextGoalsReport(first.ID, second.ID))
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{first.ID, second.ID})
	if err := s.RejectGoalReview(ctx, review.ID, "revise"); err != nil {
		t.Fatalf("RejectGoalReview: %v", err)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{first.ID, second.ID})

	if err := s.WithdrawActiveGoal(ctx, source.ID, "withdraw source"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{first.ID, second.ID})
}

func TestNextGoalsSurviveReviewFinalization(t *testing.T) {
	s, ctx, source, first, second := nextGoalsFixture(t)
	requester := "next-goals-finalize-requester"
	receiver := "next-goals-finalize-receiver"
	requesterID := testSessionID(requester)
	receiverID := testSessionID(receiver)
	addLiveProjectClaim(t, s, source.ID, requester)
	addTestAgentSession(t, s, receiver)
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "next-goals-finalize-handoff", source.ID, requesterID, receiverID)

	review, err := s.RequestGoalReview(ctx, source.ID, requesterID, nextGoalsReport(second.ID, first.ID))
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	if _, err := s.ApproveGoalReview(ctx, review.ID); err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}
	done, err := s.FinalizeGoalReview(ctx, source.ID, requesterID)
	if err != nil {
		t.Fatalf("FinalizeGoalReview: %v", err)
	}
	if done.Status != domain.GoalDone {
		t.Fatalf("finalized goal status = %q, want %q", done.Status, domain.GoalDone)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{second.ID, first.ID})
}

func TestNextGoalsReviewRejectionRestoresPreviousReportAndLinks(t *testing.T) {
	s, ctx, source, first, second := nextGoalsFixture(t)
	requester := "next-goals-review-restore-requester"
	receiver := "next-goals-review-restore-receiver"
	requesterID := testSessionID(requester)
	receiverID := testSessionID(receiver)
	addLiveProjectClaim(t, s, source.ID, requester)
	addTestAgentSession(t, s, receiver)
	handoffID := "next-goals-review-restore-handoff"
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, handoffID, source.ID, requesterID, receiverID)

	previous := nextGoalsReport(first.ID)
	previous.WorkDone = "previous work"
	previous.NowPossible = "previous result"
	previous.HowToVerify = "previous verify"
	previous.Surprises = "previous surprise"
	previous.NeedsReview = "previous review"
	firstReview, err := s.RequestGoalReview(ctx, source.ID, requesterID, previous)
	if err != nil {
		t.Fatalf("RequestGoalReview previous: %v", err)
	}
	if err := s.RejectGoalReview(ctx, firstReview.ID, "revise once"); err != nil {
		t.Fatalf("RejectGoalReview previous: %v", err)
	}

	if _, err := s.RejectGoalHandoffReview(ctx, handoffID, source.ID, requesterID, "revise once"); err != nil {
		t.Fatalf("RejectGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReviewRejection(ctx, handoffID, source.ID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoffID, source.ID, receiverID, "revised goal ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoffID, source.ID, requesterID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}

	replacement := nextGoalsReport(second.ID)
	replacement.WorkDone = "replacement work"
	replacement.NowPossible = "replacement result"
	replacement.HowToVerify = "replacement verify"
	replacement.Surprises = "replacement surprise"
	replacement.NeedsReview = "replacement review"
	secondReview, err := s.RequestGoalReview(ctx, source.ID, requesterID, replacement)
	if err != nil {
		t.Fatalf("RequestGoalReview replacement: %v", err)
	}
	if err := s.RejectGoalReview(ctx, secondReview.ID, "restore previous review"); err != nil {
		t.Fatalf("RejectGoalReview replacement: %v", err)
	}

	goal, err := s.GetGoal(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetGoal after replacement rejection: %v", err)
	}
	if goal.WorkDone != previous.WorkDone || goal.NowPossible != previous.NowPossible || goal.HowToVerify != previous.HowToVerify || goal.Surprises != previous.Surprises || goal.NeedsReview != previous.NeedsReview || goal.ResultSummary != previous.WorkDone {
		t.Fatalf("goal after replacement rejection = %+v, want previous report %+v", goal, previous)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{first.ID})
}
