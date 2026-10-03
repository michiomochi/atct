package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

// 0051 runs against a schema that already holds goals in every old status; the
// fixture is built on the current schema and the migration's SQL is applied to it.
func TestRemoveGoalApprovalMigrationActivatesRecentProposedGoalsAndDropsStaleOnes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	newGoal := func(projectID int64, status string, age time.Duration) int64 {
		t.Helper()
		g, err := s.CreateGoal(ctx, projectID, status+" goal", "human")
		if err != nil {
			t.Fatal(err)
		}
		updatedAt := time.Now().UTC().Add(-age).Format(time.RFC3339)
		if _, err := s.DB().ExecContext(ctx, `UPDATE goals SET status = ?, updated_at = ?, work_done = 'x', now_possible = 'x', how_to_verify = 'x', surprises = 'x', needs_review = 'x' WHERE id = ?`, status, updatedAt, g.ID); err != nil {
			t.Fatal(err)
		}
		return g.ID
	}
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

	const day = 24 * time.Hour
	type fixture struct{ goal, approval int64 }
	var recent, stale []fixture
	for _, name := range []string{"one", "two"} {
		project, err := s.CreateProject(ctx, "migration-"+name, "/repos/migration-"+name)
		if err != nil {
			t.Fatal(err)
		}
		for _, age := range []time.Duration{day, 6*day + 23*time.Hour} {
			g := newGoal(project.ID, "proposed", age)
			recent = append(recent, fixture{g, approval(g, "open")})
		}
		for _, age := range []time.Duration{8 * day, 7*day + time.Minute} {
			g := newGoal(project.ID, "proposed", age)
			stale = append(stale, fixture{g, approval(g, "open")})
		}
	}
	project, err := s.CreateProject(ctx, "migration-other", "/repos/migration-other")
	if err != nil {
		t.Fatal(err)
	}
	active := newGoal(project.ID, "active", 30*day)
	done := newGoal(project.ID, "done", 30*day)
	dropped := newGoal(project.ID, "dropped", 30*day)
	appliedApproval := approval(active, "applied")
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

	statusOf := func(table string, id int64) string {
		t.Helper()
		var status string
		if err := s.DB().QueryRowContext(ctx, `SELECT status FROM `+table+` WHERE id = ?`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	for _, f := range recent {
		goal, err := s.GetGoal(ctx, f.goal)
		if err != nil {
			t.Fatal(err)
		}
		if goal.Status != domain.GoalActive || goal.ResultSummary != "" {
			t.Fatalf("recent goal %d = %q / %q, want active without result_summary", f.goal, goal.Status, goal.ResultSummary)
		}
		var status, label string
		var answeredAt, appliedAt *string
		if err := s.DB().QueryRowContext(ctx, `SELECT status, answer_label, answered_at, applied_at FROM decisions WHERE id = ?`, f.approval).Scan(&status, &label, &answeredAt, &appliedAt); err != nil {
			t.Fatal(err)
		}
		if status != "applied" || label != "approve" || answeredAt == nil || appliedAt == nil {
			t.Fatalf("recent goal %d approval = %q/%q/%v/%v, want applied/approve with timestamps", f.goal, status, label, answeredAt, appliedAt)
		}
	}
	for _, f := range stale {
		goal, err := s.GetGoal(ctx, f.goal)
		if err != nil {
			t.Fatal(err)
		}
		if goal.Status != domain.GoalDropped || !strings.Contains(goal.ResultSummary, "Goal 321") {
			t.Fatalf("stale goal %d = %q / %q, want dropped with the Goal 321 reason", f.goal, goal.Status, goal.ResultSummary)
		}
		if got := statusOf("decisions", f.approval); got != string(domain.DecisionWithdrawn) {
			t.Fatalf("stale goal %d approval = %q, want withdrawn", f.goal, got)
		}
	}
	for id, want := range before {
		if got := statusOf("goals", id); got != want {
			t.Fatalf("goal %d status = %q, want %q unchanged", id, got, want)
		}
	}
	if got := statusOf("decisions", appliedApproval); got != "applied" {
		t.Fatalf("applied approval status = %q, want applied unchanged", got)
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
}
