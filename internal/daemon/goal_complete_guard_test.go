package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestGoalCompleteDispatchRejectsNonActiveStatuses(t *testing.T) {
	cases := []struct {
		name      string
		status    domain.GoalStatus
		wantError string
		setupGoal func(*testing.T, context.Context, *store.Store, *Daemon, domain.Project, int64, domain.CompletionReport) domain.Goal
	}{
		{
			name:      "proposed",
			status:    domain.GoalProposed,
			wantError: "goal %d is proposed, not active; approve it before reporting completion (承認前のゴールには完了報告を出せません)",
			setupGoal: func(t *testing.T, ctx context.Context, s *store.Store, _ *Daemon, project domain.Project, _ int64, report domain.CompletionReport) domain.Goal {
				goal, err := s.CreateGoal(ctx, project.ID, "proposed goal\n\ndescription", "agent")
				if err != nil {
					t.Fatalf("CreateGoal: %v", err)
				}
				seedGoalCompletionReport(t, ctx, s, goal.ID, report)
				return goal
			},
		},
		{
			name:      "done",
			status:    domain.GoalDone,
			wantError: "goal %d is done, not active; the approved completion report was left unchanged (完了済みのゴールには完了報告を出せません。承認済みの文章はそのままです)",
			setupGoal: setupDoneGoal,
		},
		{
			name:      "dropped",
			status:    domain.GoalDropped,
			wantError: "goal %d is dropped, not active; the completion report was left unchanged (取り下げ済みのゴールには完了報告を出せません。完了報告はそのままです)",
			setupGoal: setupDroppedGoal,
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx, s, daemon, project, sessionID := newGoalCompleteGuardFixture(t, tt.name)
			report := approvedGoalReport(tt.name)
			goal := tt.setupGoal(t, ctx, s, daemon, project, sessionID, report)

			before, err := s.GetGoal(ctx, goal.ID)
			if err != nil {
				t.Fatalf("GetGoal before dispatch: %v", err)
			}
			completionCountBefore := completionDecisionCount(t, ctx, s, goal.ID)

			raw, err := daemon.dispatch(ctx, rpc.Request{
				Method: "goal.complete",
				Params: goalCompleteParams(t, goal.ID, sessionID, lateGoalReport(tt.name)),
			})
			if err == nil {
				t.Fatalf("goal.complete returned success with response %s", raw)
			}
			if !errors.Is(err, store.ErrGoalNotActive) {
				t.Fatalf("goal.complete error = %v, want ErrGoalNotActive", err)
			}
			wantError := fmt.Sprintf("%s: "+tt.wantError, store.ErrGoalNotActive, goal.ID)
			if err.Error() != wantError {
				t.Fatalf("goal.complete error = %q, want %q", err, wantError)
			}

			after, err := s.GetGoal(ctx, goal.ID)
			if err != nil {
				t.Fatalf("GetGoal after dispatch: %v", err)
			}
			if after.Status != before.Status || after.Status != tt.status {
				t.Fatalf("goal status = %q, want unchanged %q", after.Status, before.Status)
			}
			assertGoalCompletionReportUnchanged(t, before, after)
			completionCountAfter := completionDecisionCount(t, ctx, s, goal.ID)
			if completionCountAfter != completionCountBefore {
				t.Fatalf("completion decision count = %d, want unchanged %d", completionCountAfter, completionCountBefore)
			}
		})
	}
}

func TestGoalCompleteDispatchAcceptsActiveAndRejectsAfterApproval(t *testing.T) {
	cases := []struct {
		name string
	}{
		{name: "active"},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx, s, daemon, project, sessionID := newGoalCompleteGuardFixture(t, tt.name)
			goal, err := s.CreateGoal(ctx, project.ID, "active goal\n\ndescription", "human")
			if err != nil {
				t.Fatalf("CreateGoal: %v", err)
			}
			approved := approvedGoalReport(tt.name)
			completionCountBefore := completionDecisionCount(t, ctx, s, goal.ID)

			completion := dispatchGoalComplete(t, ctx, daemon, goal.ID, sessionID, approved)
			if completion.Kind != domain.KindCompletion {
				t.Fatalf("goal.complete kind = %q, want %q", completion.Kind, domain.KindCompletion)
			}

			pending, err := s.GetGoal(ctx, goal.ID)
			if err != nil {
				t.Fatalf("GetGoal after active dispatch: %v", err)
			}
			if pending.Status != domain.GoalActive {
				t.Fatalf("goal status after dispatch = %q, want %q", pending.Status, domain.GoalActive)
			}
			assertGoalCompletionReport(t, pending, approved)
			if got := completionDecisionCount(t, ctx, s, goal.ID); got != completionCountBefore+1 {
				t.Fatalf("completion decision count after dispatch = %d, want %d", got, completionCountBefore+1)
			}

			done, err := s.ApproveCompletion(ctx, completion.ID)
			if err != nil {
				t.Fatalf("ApproveCompletion: %v", err)
			}
			if done.Status != domain.GoalDone {
				t.Fatalf("goal status after approval = %q, want %q", done.Status, domain.GoalDone)
			}
			assertGoalCompletionReport(t, done, approved)

			_, err = daemon.dispatch(ctx, rpc.Request{
				Method: "goal.complete",
				Params: goalCompleteParams(t, goal.ID, sessionID, lateGoalReport(tt.name)),
			})
			if err == nil {
				t.Fatal("goal.complete returned success for an approved done goal")
			}
			if !errors.Is(err, store.ErrGoalNotActive) {
				t.Fatalf("late goal.complete error = %v, want ErrGoalNotActive", err)
			}
			wantError := fmt.Sprintf("%s: goal %d is done, not active; the approved completion report was left unchanged (完了済みのゴールには完了報告を出せません。承認済みの文章はそのままです)", store.ErrGoalNotActive, goal.ID)
			if err.Error() != wantError {
				t.Fatalf("late goal.complete error = %q, want %q", err, wantError)
			}

			afterLate, err := s.GetGoal(ctx, goal.ID)
			if err != nil {
				t.Fatalf("GetGoal after late dispatch: %v", err)
			}
			if afterLate.Status != domain.GoalDone {
				t.Fatalf("goal status after late dispatch = %q, want %q", afterLate.Status, domain.GoalDone)
			}
			assertGoalCompletionReport(t, afterLate, approved)
			if got := completionDecisionCount(t, ctx, s, goal.ID); got != completionCountBefore+1 {
				t.Fatalf("completion decision count after late dispatch = %d, want %d", got, completionCountBefore+1)
			}
		})
	}
}

func newGoalCompleteGuardFixture(t *testing.T, label string) (context.Context, *store.Store, *Daemon, domain.Project, int64) {
	t.Helper()
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project, err := s.CreateProject(ctx, "goal-complete-guard-"+label, t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	sessionID := daemonTestSessionID(t, s, "goal-complete-guard-"+label)
	if err := s.AssociateAgentSessionWithProject(ctx, sessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	// goal.complete authorization requires the dispatching session to hold the project's claim.
	if _, err := s.ClaimProject(ctx, project.ID, sessionID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	return ctx, s, New(s), project, sessionID
}

func setupDoneGoal(t *testing.T, ctx context.Context, s *store.Store, daemon *Daemon, project domain.Project, sessionID int64, report domain.CompletionReport) domain.Goal {
	t.Helper()
	goal, err := s.CreateGoal(ctx, project.ID, "done goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	completion := dispatchGoalComplete(t, ctx, daemon, goal.ID, sessionID, report)
	if _, err := s.ApproveCompletion(ctx, completion.ID); err != nil {
		t.Fatalf("ApproveCompletion: %v", err)
	}
	goal, err = s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal after approval: %v", err)
	}
	return goal
}

func setupDroppedGoal(t *testing.T, ctx context.Context, s *store.Store, daemon *Daemon, project domain.Project, sessionID int64, report domain.CompletionReport) domain.Goal {
	t.Helper()
	goal, err := s.CreateGoal(ctx, project.ID, "dropped goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if completion := dispatchGoalComplete(t, ctx, daemon, goal.ID, sessionID, report); completion.Kind != domain.KindCompletion {
		t.Fatalf("goal.complete kind = %q, want %q", completion.Kind, domain.KindCompletion)
	}
	if err := s.WithdrawActiveGoal(ctx, goal.ID, report.WorkDone); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}
	goal, err = s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal after withdrawal: %v", err)
	}
	return goal
}

func dispatchGoalComplete(t *testing.T, ctx context.Context, daemon *Daemon, goalID, sessionID int64, report domain.CompletionReport) domain.Decision {
	t.Helper()
	raw, err := daemon.dispatch(ctx, rpc.Request{
		Method: "goal.complete",
		Params: goalCompleteParams(t, goalID, sessionID, report),
	})
	if err != nil {
		t.Fatalf("goal.complete: %v", err)
	}
	var completion domain.Decision
	if err := json.Unmarshal(raw, &completion); err != nil {
		t.Fatalf("unmarshal goal.complete response %s: %v", raw, err)
	}
	return completion
}

func goalCompleteParams(t *testing.T, goalID, sessionID int64, report domain.CompletionReport) json.RawMessage {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"goal_id":          goalID,
		"work_done":        report.WorkDone,
		"now_possible":     report.NowPossible,
		"how_to_verify":    report.HowToVerify,
		"surprises":        report.Surprises,
		"needs_review":     report.NeedsReview,
		"next_steps":       report.NextSteps,
		"agent_session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("Marshal goal.complete params: %v", err)
	}
	return params
}

func approvedGoalReport(label string) domain.CompletionReport {
	return domain.CompletionReport{
		WorkDone:    "AAA-" + label + "-approved-work-done",
		NowPossible: "AAA-" + label + "-approved-now-possible",
		HowToVerify: "AAA-" + label + "-approved-how-to-verify",
		Surprises:   "AAA-" + label + "-approved-surprises",
		NeedsReview: "AAA-" + label + "-approved-needs-review",
		NextSteps:   "AAA-" + label + "-approved-next-steps",
	}
}

func lateGoalReport(label string) domain.CompletionReport {
	return domain.CompletionReport{
		WorkDone:    "BBB-" + label + "-late-draft-work-done",
		NowPossible: "BBB-" + label + "-late-draft-now-possible",
		HowToVerify: "BBB-" + label + "-late-draft-how-to-verify",
		Surprises:   "BBB-" + label + "-late-draft-surprises",
		NeedsReview: "BBB-" + label + "-late-draft-needs-review",
		NextSteps:   "BBB-" + label + "-late-draft-next-steps",
	}
}

func seedGoalCompletionReport(t *testing.T, ctx context.Context, s *store.Store, goalID int64, report domain.CompletionReport) {
	t.Helper()
	_, err := s.DB().ExecContext(ctx, `
		UPDATE goals SET result_summary = ?, work_done = ?, now_possible = ?,
		  how_to_verify = ?, surprises = ?, needs_review = ?, next_steps = ?
		WHERE id = ?`,
		report.WorkDone, report.WorkDone, report.NowPossible, report.HowToVerify,
		report.Surprises, report.NeedsReview, report.NextSteps, goalID)
	if err != nil {
		t.Fatalf("seed goal completion report: %v", err)
	}
}

func completionDecisionCount(t *testing.T, ctx context.Context, s *store.Store, goalID int64) int {
	t.Helper()
	var count int
	if err := s.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM decisions WHERE goal_id = ? AND kind = 'completion'", goalID,
	).Scan(&count); err != nil {
		t.Fatalf("count completion decisions: %v", err)
	}
	return count
}

func assertGoalCompletionReport(t *testing.T, goal domain.Goal, want domain.CompletionReport) {
	t.Helper()
	wantValues := []struct {
		name  string
		value string
	}{
		{name: "work_done", value: want.WorkDone},
		{name: "now_possible", value: want.NowPossible},
		{name: "how_to_verify", value: want.HowToVerify},
		{name: "surprises", value: want.Surprises},
		{name: "needs_review", value: want.NeedsReview},
		{name: "next_steps", value: want.NextSteps},
		{name: "result_summary", value: want.WorkDone},
	}
	gotValues := goalCompletionReportValues(goal)
	for i := range wantValues {
		if gotValues[i].value != wantValues[i].value {
			t.Errorf("%s = %q, want %q", gotValues[i].name, gotValues[i].value, wantValues[i].value)
		}
	}
}

func assertGoalCompletionReportUnchanged(t *testing.T, before, after domain.Goal) {
	t.Helper()
	beforeValues := goalCompletionReportValues(before)
	afterValues := goalCompletionReportValues(after)
	for i := range beforeValues {
		if afterValues[i].value != beforeValues[i].value {
			t.Errorf("%s = %q, want unchanged %q", afterValues[i].name, afterValues[i].value, beforeValues[i].value)
		}
		if strings.Contains(afterValues[i].value, "BBB-") {
			t.Errorf("%s contains late draft value %q", afterValues[i].name, afterValues[i].value)
		}
	}
}

func goalCompletionReportValues(goal domain.Goal) []struct {
	name  string
	value string
} {
	return []struct {
		name  string
		value string
	}{
		{name: "work_done", value: goal.WorkDone},
		{name: "now_possible", value: goal.NowPossible},
		{name: "how_to_verify", value: goal.HowToVerify},
		{name: "surprises", value: goal.Surprises},
		{name: "needs_review", value: goal.NeedsReview},
		{name: "next_steps", value: goal.NextSteps},
		{name: "result_summary", value: goal.ResultSummary},
	}
}
