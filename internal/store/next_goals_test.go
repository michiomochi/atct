package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestMigration0046DiscardsLegacyNextStepsAndCreatesNextGoals(t *testing.T) {
	db := openMigrationTestDB(t)
	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	found := false
	for _, migration := range migrations {
		if migration.filename == "0046_next_goals.sql" {
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
		t.Fatal("embedded migrations do not contain 0046_next_goals.sql")
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
		t.Fatalf("apply 0046 migration: %v", err)
	}
	assertUserVersion(t, db, schemaVersion)
	assertMigrationRecorded(t, db, "0046_next_goals.sql")
	assertTableExists(t, db, "next_goals")

	columns := migrationTableColumns(t, db, "goals")
	for _, name := range []string{"next_steps", "legacy_next_steps"} {
		if _, ok := columns[name]; ok {
			t.Fatalf("goals still exposes %s after migration", name)
		}
	}
	nextGoalColumns := migrationTableColumns(t, db, "next_goals")
	if len(nextGoalColumns) != 3 {
		t.Fatalf("next_goals columns = %v, want goal_id, next_goal_id, created_at", nextGoalColumns)
	}
	for _, name := range []string{"goal_id", "next_goal_id", "created_at"} {
		if _, ok := nextGoalColumns[name]; !ok {
			t.Fatalf("next_goals is missing %s", name)
		}
	}
	var workDone string
	if err := db.QueryRow(`SELECT work_done FROM goals WHERE id = 1`).Scan(&workDone); err != nil || workDone != "done work" {
		t.Fatalf("migrated done goal work_done = %q, %v; want done work", workDone, err)
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

// resubmitNextGoalsHandoff walks a rejected goal review back through the
// handoff so that the next RequestGoalReview is allowed.
func resubmitNextGoalsHandoff(t *testing.T, s *Store, ctx context.Context, handoffID string, goalID, requesterID, receiverID int64) {
	t.Helper()
	if _, err := s.RejectGoalHandoffReview(ctx, handoffID, goalID, requesterID, "revise"); err != nil {
		t.Fatalf("RejectGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReviewRejection(ctx, handoffID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoffID, goalID, receiverID, "revised goal ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoffID, goalID, requesterID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
}

func TestNextGoalsReplaceLinksAndAllowEmptyInput(t *testing.T) {
	s, ctx, source, first, second := nextGoalsFixture(t)
	requester := "next-goals-replace-requester"
	receiver := "next-goals-replace-receiver"
	requesterID := testSessionID(requester)
	receiverID := testSessionID(receiver)
	addLiveProjectClaim(t, s, source.ID, requester)
	addTestAgentSession(t, s, receiver)
	const handoffID = "next-goals-replace-handoff"
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, handoffID, source.ID, requesterID, receiverID)

	review, err := s.RequestGoalReview(ctx, source.ID, requesterID, nextGoalsReport(second.ID, first.ID))
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{first.ID, second.ID})
	goal, err := s.GetGoal(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got := []int64{goal.NextGoals[0].ID, goal.NextGoals[1].ID}; !reflect.DeepEqual(got, []int64{first.ID, second.ID}) {
		t.Fatalf("goal next-goal summaries = %v, want %v", got, []int64{first.ID, second.ID})
	}

	if err := s.RejectGoalReview(ctx, review.ID, "revise"); err != nil {
		t.Fatalf("RejectGoalReview: %v", err)
	}
	resubmitNextGoalsHandoff(t, s, ctx, handoffID, source.ID, requesterID, receiverID)
	review, err = s.RequestGoalReview(ctx, source.ID, requesterID, nextGoalsReport(first.ID))
	if err != nil {
		t.Fatalf("RequestGoalReview replacement: %v", err)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{first.ID})
	if err := s.RejectGoalReview(ctx, review.ID, "clear links"); err != nil {
		t.Fatalf("RejectGoalReview replacement: %v", err)
	}
	resubmitNextGoalsHandoff(t, s, ctx, handoffID, source.ID, requesterID, receiverID)
	if _, err := s.RequestGoalReview(ctx, source.ID, requesterID, nextGoalsReport()); err != nil {
		t.Fatalf("RequestGoalReview empty replacement: %v", err)
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
			_, err := crossProjectStore.RequestGoalReview(ctx, source.ID, testSessionID("next-goals-invalid-"+tc.name), nextGoalsReport(tc.ids...))
			if !errors.Is(err, tc.want) {
				t.Fatalf("RequestGoalReview error = %v, want %v", err, tc.want)
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
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{first.ID, second.ID})
}

func TestNextGoalsReviewRejectionKeepsRejectedRequestReportAndLinks(t *testing.T) {
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
	if err := s.RejectGoalReview(ctx, secondReview.ID, "keep the rejected request"); err != nil {
		t.Fatalf("RejectGoalReview replacement: %v", err)
	}

	goal, err := s.GetGoal(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetGoal after replacement rejection: %v", err)
	}
	if goal.WorkDone != replacement.WorkDone || goal.NowPossible != replacement.NowPossible || goal.HowToVerify != replacement.HowToVerify || goal.Surprises != replacement.Surprises || goal.NeedsReview != replacement.NeedsReview || goal.ResultSummary != replacement.WorkDone {
		t.Fatalf("goal after replacement rejection = %+v, want rejected request report %+v", goal, replacement)
	}
	requireNextGoalIDs(t, s, ctx, source.ID, []int64{second.ID})
}
