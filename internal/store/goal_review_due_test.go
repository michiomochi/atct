package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func newProposal(t *testing.T, s *Store, creator string) domain.Goal {
	t.Helper()
	project, err := s.CreateProject(context.Background(), fmt.Sprintf("review-due-%s-%s", creator, t.Name()), t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(context.Background(), project.ID, "proposed goal", creator)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if creator == "human" {
		if _, err := s.DB().ExecContext(context.Background(), `UPDATE goals SET status = 'proposed' WHERE id = ?`, goal.ID); err != nil {
			t.Fatalf("make proposed: %v", err)
		}
	}
	return goal
}

func ageGoal(t *testing.T, s *Store, goalID int64, updatedAt time.Time) {
	t.Helper()
	if _, err := s.DB().ExecContext(context.Background(), `UPDATE goals SET updated_at = ? WHERE id = ?`, updatedAt.UTC().Format(time.RFC3339), goalID); err != nil {
		t.Fatalf("age goal: %v", err)
	}
}

func reviewDueIDs(t *testing.T, s *Store, projectID int64) []int64 {
	t.Helper()
	state, err := s.EvaluateWakeup(context.Background(), projectID)
	if err != nil {
		t.Fatalf("EvaluateWakeup: %v", err)
	}
	ids := []int64{}
	for _, g := range state.ReviewDueGoals {
		ids = append(ids, g.ID)
		if _, ok := state.ReviewDueAt[g.ID]; !ok {
			t.Fatalf("ReviewDueAt missing goal %d", g.ID)
		}
	}
	return ids
}

func TestReviewDueGoalsByAge(t *testing.T) {
	for _, creator := range []string{"agent", "human"} {
		t.Run(creator, func(t *testing.T) {
			s := newTestStore(t)
			goal := newProposal(t, s, creator)
			if got := reviewDueIDs(t, s, goal.ProjectID); len(got) != 0 {
				t.Fatalf("fresh proposal due: %v", got)
			}
			ageGoal(t, s, goal.ID, time.Now().Add(-6*24*time.Hour))
			if got := reviewDueIDs(t, s, goal.ProjectID); len(got) != 0 {
				t.Fatalf("6-day proposal due: %v", got)
			}
			updated := time.Now().Add(-8 * 24 * time.Hour)
			ageGoal(t, s, goal.ID, updated)
			if got := reviewDueIDs(t, s, goal.ProjectID); len(got) != 1 || got[0] != goal.ID {
				t.Fatalf("8-day proposal due = %v, want [%d]", got, goal.ID)
			}
			state, _ := s.EvaluateWakeup(context.Background(), goal.ProjectID)
			if want := updated.UTC().Truncate(time.Second).Add(goalReviewDueAfter); !state.ReviewDueAt[goal.ID].Equal(want) {
				t.Fatalf("due at = %v, want %v", state.ReviewDueAt[goal.ID], want)
			}
		})
	}
}

func TestReviewDueGoalsIgnoresNonProposed(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s) // active
	ageGoal(t, s, goalID, time.Now().Add(-30*24*time.Hour))
	goal, err := s.GetGoal(context.Background(), goalID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reviewDueIDs(t, s, goal.ProjectID); len(got) != 0 {
		t.Fatalf("active goal due: %v", got)
	}
}

func TestConfirmProposedGoalExtendsDueTime(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goal := newProposal(t, s, "agent")
	ageGoal(t, s, goal.ID, time.Now().Add(-8*24*time.Hour))
	if err := s.ConfirmProposedGoal(ctx, goal.ID, "still valid against main"); err != nil {
		t.Fatalf("ConfirmProposedGoal: %v", err)
	}
	if got := reviewDueIDs(t, s, goal.ProjectID); len(got) != 0 {
		t.Fatalf("confirmed goal due: %v", got)
	}
	after, _ := s.GetGoal(ctx, goal.ID)
	if after.Status != domain.GoalProposed {
		t.Fatalf("status = %q, want proposed", after.Status)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE goal_confirmations SET confirmed_at = ? WHERE goal_id = ?`, formatTimestamp(time.Now().Add(-8*24*time.Hour)), goal.ID); err != nil {
		t.Fatal(err)
	}
	if got := reviewDueIDs(t, s, goal.ProjectID); len(got) != 1 {
		t.Fatalf("backdated confirmation due = %v, want 1", got)
	}
}

func TestConfirmProposedGoalRejects(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goal := newProposal(t, s, "agent")
	for _, note := range []string{"", " \t\n"} {
		if err := s.ConfirmProposedGoal(ctx, goal.ID, note); err == nil {
			t.Fatalf("blank note %q accepted", note)
		}
	}
	if err := s.ConfirmProposedGoal(ctx, 999999, "note"); err == nil {
		t.Fatal("unknown goal accepted")
	}
	activeID := newTestGoal(t, s)
	if err := s.ConfirmProposedGoal(ctx, activeID, "note"); err == nil {
		t.Fatal("active goal accepted")
	}
	var n int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal_confirmations`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("confirmations = %d, %v; want 0", n, err)
	}
}

func TestWithdrawProposedGoalWithWorkAndHumanCreator(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goal := newProposal(t, s, "agent")
	if _, err := s.DB().ExecContext(ctx, `UPDATE goals SET creator = 'human' WHERE id = ?`, goal.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO tasks (goal_id, title, description, status, agent, sort_order, declare_key, created_at, updated_at)
		VALUES (?, 'open task', 'd', 'todo', 'agent', 0, 'k', ?, ?)`, goal.ID, now, now); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := s.AskDecision(ctx, AskInput{GoalID: goal.ID, Kind: domain.KindDecision, Question: "extra?"}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	const reason = "superseded by a fix on main"
	if err := s.WithdrawActiveGoal(ctx, goal.ID, reason); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}
	got, _ := s.GetGoal(ctx, goal.ID)
	if got.Status != domain.GoalDropped || got.ResultSummary != reason {
		t.Fatalf("goal = %+v, want dropped with reason", got)
	}
	tasks, _ := s.ListTasks(ctx, goal.ID)
	if len(tasks) != 1 || tasks[0].Status != domain.TaskDropped {
		t.Fatalf("tasks = %+v, want one dropped", tasks)
	}
	decisions, _ := s.ListDecisionsForGoal(ctx, goal.ID)
	for _, d := range decisions {
		if d.Status != domain.DecisionWithdrawn || d.AnswerText != reason {
			t.Fatalf("decision = %+v, want withdrawn with reason", d)
		}
	}
	if len(decisions) != 2 {
		t.Fatalf("decisions = %d, want 2", len(decisions))
	}
}
