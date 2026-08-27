package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestCompleteGoalWithReportRejectsNonActiveGoalsWithoutChangingReport(t *testing.T) {
	cases := []struct {
		name   string
		status domain.GoalStatus
		want   string
	}{
		{
			name:   "proposed",
			status: domain.GoalProposed,
			want:   "goal %d is proposed, not active; approve it before reporting completion (承認前のゴールには完了報告を出せません)",
		},
		{
			name:   "done",
			status: domain.GoalDone,
			want:   "goal %d is done, not active; the approved completion report was left unchanged (完了済みのゴールには完了報告を出せません。承認済みの文章はそのままです)",
		},
		{
			name:   "dropped",
			status: domain.GoalDropped,
			want:   "goal %d is dropped, not active; the completion report was left unchanged (取り下げ済みのゴールには完了報告を出せません。完了報告はそのままです)",
		},
		{
			name:   "paused",
			status: domain.GoalStatus("paused"),
			want:   `goal %d has status "paused", not active; the completion report was left unchanged (ゴールの状態 "paused" はアクティブではないため、完了報告を出せません。完了報告はそのままです)`,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			goalID := newTestGoal(t, s)
			const (
				resultSummary = "approved result"
				workDone      = "approved work"
				nowPossible   = "approved possibility"
				howToVerify   = "approved verification"
				surprises     = "approved surprises"
				needsReview   = "approved review"
				nextSteps     = "approved next steps"
			)
			if _, err := s.db.ExecContext(ctx, `
				UPDATE goals SET status = ?, result_summary = ?, work_done = ?, now_possible = ?,
				  how_to_verify = ?, surprises = ?, needs_review = ?, next_steps = ?
				WHERE id = ?`,
				string(tt.status), resultSummary, workDone, nowPossible, howToVerify, surprises,
				needsReview, nextSteps, goalID); err != nil {
				t.Fatalf("set goal state: %v", err)
			}

			before, err := s.GetGoal(ctx, goalID)
			if err != nil {
				t.Fatalf("GetGoal before completion: %v", err)
			}
			var completionCountBefore int
			if err := s.db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM decisions WHERE goal_id = ? AND kind = 'completion'", goalID,
			).Scan(&completionCountBefore); err != nil {
				t.Fatalf("count completion decisions before completion: %v", err)
			}

			_, err = s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
				WorkDone:    "new work",
				NowPossible: "new possibility",
				HowToVerify: "new verification",
				Surprises:   "new surprises",
				NeedsReview: "new review",
				NextSteps:   "new next steps",
			}, testSessionID("non-active-completion"))
			if !errors.Is(err, ErrGoalNotActive) {
				t.Fatalf("CompleteGoalWithReport error = %v, want ErrGoalNotActive", err)
			}
			if want := fmt.Sprintf("%s: "+tt.want, ErrGoalNotActive, goalID); err.Error() != want {
				t.Fatalf("CompleteGoalWithReport error = %q, want %q", err, want)
			}

			after, err := s.GetGoal(ctx, goalID)
			if err != nil {
				t.Fatalf("GetGoal after completion: %v", err)
			}
			beforeReport := []string{before.WorkDone, before.NowPossible, before.HowToVerify, before.Surprises, before.NeedsReview, before.NextSteps, before.ResultSummary}
			afterReport := []string{after.WorkDone, after.NowPossible, after.HowToVerify, after.Surprises, after.NeedsReview, after.NextSteps, after.ResultSummary}
			for i := range beforeReport {
				if afterReport[i] != beforeReport[i] {
					t.Fatalf("completion report field %d = %q, want unchanged %q", i, afterReport[i], beforeReport[i])
				}
			}
			var completionCountAfter int
			if err := s.db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM decisions WHERE goal_id = ? AND kind = 'completion'", goalID,
			).Scan(&completionCountAfter); err != nil {
				t.Fatalf("count completion decisions after completion: %v", err)
			}
			if completionCountAfter != completionCountBefore {
				t.Fatalf("completion decision count = %d, want unchanged %d", completionCountAfter, completionCountBefore)
			}
		})
	}
}

func TestCompleteGoalWithReportUpdatesActiveGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	report := domain.CompletionReport{
		WorkDone:    "all work",
		NowPossible: "the feature is usable",
		HowToVerify: "run the store tests",
		Surprises:   "none",
		NeedsReview: "the SQL guard",
		NextSteps:   "merge the change",
	}

	d, err := s.CompleteGoalWithReport(ctx, goalID, report, testSessionID("active-completion"))
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	if d.Kind != domain.KindCompletion {
		t.Fatalf("kind = %q, want %q", d.Kind, domain.KindCompletion)
	}

	g, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if g.Status != domain.GoalActive {
		t.Fatalf("goal status = %q, want %q", g.Status, domain.GoalActive)
	}
	gotReport := []string{g.WorkDone, g.NowPossible, g.HowToVerify, g.Surprises, g.NeedsReview, g.NextSteps, g.ResultSummary}
	wantReport := []string{report.WorkDone, report.NowPossible, report.HowToVerify, report.Surprises, report.NeedsReview, report.NextSteps, report.WorkDone}
	for i := range wantReport {
		if gotReport[i] != wantReport[i] {
			t.Fatalf("completion report field %d = %q, want %q", i, gotReport[i], wantReport[i])
		}
	}
}

func TestCompleteGoalRejectedWhenOpenDecisionExists(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "complete-open")

	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "Unresolved", AgentSessionID: testSessionID("run-1"),
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	_, err := s.CompleteGoal(ctx, goalID, "Done", testSessionID("run-1"))
	if !errors.Is(err, ErrGoalHasOpenDecision) {
		t.Fatalf("err = %v, want ErrGoalHasOpenDecision", err)
	}
}

func TestApproveCompletionClosesGoalImmediately(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d, err := s.CompleteGoal(ctx, goalID, "All tasks complete", testSessionID("run-1"))
	if err != nil {
		t.Fatalf("CompleteGoal: %v", err)
	}
	if d.Kind != domain.KindCompletion {
		t.Fatalf("kind = %q, want %q", d.Kind, domain.KindCompletion)
	}

	g, err := s.ApproveCompletion(ctx, d.ID)
	if err != nil {
		t.Fatalf("ApproveCompletion: %v", err)
	}
	if g.Status != domain.GoalDone {
		t.Fatalf("goal status = %q, want %q", g.Status, domain.GoalDone)
	}
	if g.ResultSummary != "All tasks complete" {
		t.Fatalf("result_summary = %q, want %q", g.ResultSummary, "All tasks complete")
	}

	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionApplied {
		t.Fatalf("decision status = %q, want %q (approval becomes applied immediately because no follow-up work exists)",
			got.Status, domain.DecisionApplied)
	}
}

func TestRejectCompletionKeepsGoalActiveAndAwaitsAgent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d, err := s.CompleteGoal(ctx, goalID, "Thought it was done", testSessionID("run-1"))
	if err != nil {
		t.Fatalf("CompleteGoal: %v", err)
	}
	if err := s.RejectCompletion(ctx, d.ID, "Insufficient tests"); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}

	g, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if g.Status != domain.GoalActive {
		t.Fatalf("goal status = %q, want %q", g.Status, domain.GoalActive)
	}

	got, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered {
		t.Fatalf("decision status = %q, want %q (rejection remains answered until the agent receives it)",
			got.Status, domain.DecisionAnswered)
	}
}
