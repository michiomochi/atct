package store

import (
	"context"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

// 0051 runs against a schema that already holds goals in every old status; the
// fixture is built on the current schema and the migration's SQL is applied to it.
func TestRemoveGoalApprovalMigrationDropsProposedGoalsAndWithdrawsOpenApprovals(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "migration", "/repos/migration")
	if err != nil {
		t.Fatal(err)
	}
	newGoal := func(status string) int64 {
		t.Helper()
		g, err := s.CreateGoal(ctx, project.ID, status+" goal", "human")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().ExecContext(ctx, `UPDATE goals SET status = ?, work_done = 'x', now_possible = 'x', how_to_verify = 'x', surprises = 'x', needs_review = 'x' WHERE id = ?`, status, g.ID); err != nil {
			t.Fatal(err)
		}
		return g.ID
	}
	proposed := newGoal("proposed")
	active := newGoal("active")
	done := newGoal("done")
	dropped := newGoal("dropped")
	approval := func(goalID int64, status string) int64 {
		t.Helper()
		res, err := s.DB().ExecContext(ctx, `
			INSERT INTO decisions (goal_id, kind, question, options, status, created_at)
			VALUES (?, 'goal_approval', 'Approve this goal?', '[]', ?, '2026-10-01T00:00:00Z')`, goalID, status)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	openApproval := approval(proposed, "open")
	appliedApproval := approval(active, "applied")

	statusOf := func(table string, id int64) string {
		t.Helper()
		var status string
		if err := s.DB().QueryRowContext(ctx, `SELECT status FROM `+table+` WHERE id = ?`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	before := map[int64]string{active: "active", done: "done", dropped: "dropped"}

	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range migrations {
		if strings.HasPrefix(m.filename, "0051_") {
			found = true
			if _, err := s.DB().ExecContext(ctx, m.sql); err != nil {
				t.Fatalf("execute %s: %v", m.filename, err)
			}
		}
	}
	if !found {
		t.Fatal("migration 0051 is not embedded")
	}

	if got := statusOf("goals", proposed); got != string(domain.GoalDropped) {
		t.Fatalf("proposed goal status = %q, want dropped", got)
	}
	goal, err := s.GetGoal(ctx, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goal.ResultSummary, "Goal 321") {
		t.Fatalf("result_summary = %q, want the Goal 321 reason", goal.ResultSummary)
	}
	for id, want := range before {
		if got := statusOf("goals", id); got != want {
			t.Fatalf("goal %d status = %q, want %q unchanged", id, got, want)
		}
	}
	var proposedLeft, openApprovals int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goals WHERE status = 'proposed'`).Scan(&proposedLeft); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM decisions WHERE kind = 'goal_approval' AND status = 'open'`).Scan(&openApprovals); err != nil {
		t.Fatal(err)
	}
	if proposedLeft != 0 || openApprovals != 0 {
		t.Fatalf("proposed goals = %d, open goal_approval = %d, want 0/0", proposedLeft, openApprovals)
	}
	if got := statusOf("decisions", openApproval); got != string(domain.DecisionWithdrawn) {
		t.Fatalf("open approval status = %q, want withdrawn", got)
	}
	if got := statusOf("decisions", appliedApproval); got != "applied" {
		t.Fatalf("applied approval status = %q, want applied unchanged", got)
	}
}
