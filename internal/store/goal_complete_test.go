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

func TestGoalReviewLifecycleDefersFinalReportUntilCommanderCompletion(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("goal-review-commander")
	receiverID := testSessionID("goal-review-commander-receiver")
	addLiveProjectClaim(t, s, goalID, "goal-review-commander")
	addTestAgentSession(t, s, "goal-review-commander-receiver")
	handoff := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "goal-review-commander-handoff", goalID, commanderID, receiverID)
	if handoff.CompletedReportAt != nil {
		t.Fatalf("goal handoff = %+v, want open handoff before human review", handoff)
	}
	report := domain.CompletionReport{
		WorkDone:    "reviewed work",
		NowPossible: "reviewed result",
		HowToVerify: "run the focused tests",
		Surprises:   "none",
		NeedsReview: "none",
		NextSteps:   "merge",
	}

	review, err := s.RequestGoalReview(ctx, goalID, commanderID, report)
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	if review.Kind != domain.KindGoalReview || review.TaskID != 0 || review.Status != domain.DecisionOpen {
		t.Fatalf("goal review = %+v, want open taskless goal review", review)
	}

	before, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal before approval: %v", err)
	}
	if before.Status != domain.GoalActive || before.WorkDone != report.WorkDone || before.NowPossible != report.NowPossible || before.HowToVerify != report.HowToVerify || before.Surprises != report.Surprises || before.NeedsReview != report.NeedsReview || before.NextSteps != report.NextSteps || before.ResultSummary != report.WorkDone {
		t.Fatalf("goal before approval = %+v, want active with request-time report", before)
	}

	approvedGoal, err := s.ApproveGoalReview(ctx, review.ID)
	if err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}
	if approvedGoal.Status != domain.GoalActive {
		t.Fatalf("goal after human approval = %q, want active until commander completion", approvedGoal.Status)
	}
	approvedDecision, err := s.GetDecision(ctx, review.ID)
	if err != nil {
		t.Fatalf("GetDecision after approval: %v", err)
	}
	if approvedDecision.Status != domain.DecisionApplied || approvedDecision.AnswerLabel != "approve" {
		t.Fatalf("approved goal review = %+v, want applied approve", approvedDecision)
	}

	done, err := s.FinalizeGoalReview(ctx, goalID, commanderID)
	if err != nil {
		t.Fatalf("FinalizeGoalReview: %v", err)
	}
	if done.Status != domain.GoalDone || done.WorkDone != report.WorkDone || done.ResultSummary != report.WorkDone {
		t.Fatalf("completed goal = %+v, want done with final report", done)
	}
	completed, err := s.GetGoalHandoff(ctx, handoff.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff after finalization: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "goal work is ready for commander review" {
		t.Fatalf("completed handoff = %+v, want stored review report", completed)
	}
}

func TestRejectedGoalReviewLeavesGoalActiveWithoutReopeningHandoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("goal-review-reject-commander")
	receiverID := testSessionID("goal-review-reject-receiver")
	addLiveProjectClaim(t, s, goalID, "goal-review-reject-commander")
	addTestAgentSession(t, s, "goal-review-reject-receiver")
	if handoff := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "goal-review-reject-handoff", goalID, commanderID, receiverID); handoff.CompletedReportAt != nil {
		t.Fatalf("goal handoff = %+v, want open handoff before goal review", handoff)
	}
	review, err := s.RequestGoalReview(ctx, goalID, commanderID, domain.CompletionReport{
		WorkDone: "rejected work", NowPossible: "rejected result", HowToVerify: "rejected verify",
		Surprises: "rejected surprise", NeedsReview: "rejected review", NextSteps: "rejected next",
	})
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	if err := s.RejectGoalReview(ctx, review.ID, "needs another review"); err != nil {
		t.Fatalf("RejectGoalReview: %v", err)
	}

	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal after rejection: %v", err)
	}
	if goal.Status != domain.GoalActive || goal.WorkDone != "rejected work" || goal.ResultSummary != "rejected work" {
		t.Fatalf("goal after rejection = %+v, want active with request-time report", goal)
	}
	got, err := s.GetDecision(ctx, review.ID)
	if err != nil {
		t.Fatalf("GetDecision after rejection: %v", err)
	}
	if got.Status != domain.DecisionAnswered || got.AnswerLabel != "reject" || got.AnswerText != "needs another review" {
		t.Fatalf("rejected goal review = %+v, want answered reject with reason", got)
	}
}

func TestRequestGoalReviewPersistsReportInGoalAndFinalizesWithoutInput(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("goal-review-request-time-commander")
	receiverID := testSessionID("goal-review-request-time-receiver")
	addLiveProjectClaim(t, s, goalID, "goal-review-request-time-commander")
	addTestAgentSession(t, s, "goal-review-request-time-receiver")
	if handoff := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "goal-review-request-time-handoff", goalID, commanderID, receiverID); handoff.CompletedReportAt != nil {
		t.Fatalf("goal handoff = %+v, want open handoff before goal review", handoff)
	}
	report := domain.CompletionReport{
		WorkDone:    "request-time work",
		NowPossible: "request-time result",
		HowToVerify: "run the request-time tests",
		Surprises:   "request-time surprise",
		NeedsReview: "request-time review",
		NextSteps:   "request-time next step",
	}

	review, err := s.RequestGoalReview(ctx, goalID, commanderID, report)
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	storedGoal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if storedGoal.Status != domain.GoalActive || storedGoal.WorkDone != report.WorkDone || storedGoal.NowPossible != report.NowPossible || storedGoal.HowToVerify != report.HowToVerify || storedGoal.Surprises != report.Surprises || storedGoal.NeedsReview != report.NeedsReview || storedGoal.NextSteps != report.NextSteps || storedGoal.ResultSummary != report.WorkDone {
		t.Fatalf("stored goal = %+v, want request-time report %+v", storedGoal, report)
	}
	stored, err := s.GetDecision(ctx, review.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if stored.Kind != domain.KindGoalReview || stored.Status != domain.DecisionOpen {
		t.Fatalf("stored goal review = %+v, want ordinary open goal-review decision", stored)
	}

	if _, err := s.FinalizeGoalReview(ctx, goalID, commanderID); err == nil {
		t.Fatal("FinalizeGoalReview before approval succeeded")
	}
	if _, err := s.ApproveGoalReview(ctx, review.ID); err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}

	report.WorkDone = "caller mutation must not win"
	done, err := s.FinalizeGoalReview(ctx, goalID, commanderID)
	if err != nil {
		t.Fatalf("FinalizeGoalReview: %v", err)
	}
	if done.Status != domain.GoalDone || done.WorkDone != "request-time work" || done.NowPossible != "request-time result" || done.HowToVerify != "run the request-time tests" || done.Surprises != "request-time surprise" || done.NeedsReview != "request-time review" || done.NextSteps != "request-time next step" || done.ResultSummary != "request-time work" {
		t.Fatalf("finalized goal = %+v, want stored request-time report", done)
	}
}

func TestRequestGoalReviewRejectsIncompleteReport(t *testing.T) {
	fields := []struct {
		name string
		edit func(*domain.CompletionReport)
	}{
		{name: "work_done", edit: func(report *domain.CompletionReport) { report.WorkDone = "" }},
		{name: "now_possible", edit: func(report *domain.CompletionReport) { report.NowPossible = "" }},
		{name: "how_to_verify", edit: func(report *domain.CompletionReport) { report.HowToVerify = "" }},
		{name: "surprises", edit: func(report *domain.CompletionReport) { report.Surprises = "" }},
		{name: "needs_review", edit: func(report *domain.CompletionReport) { report.NeedsReview = "" }},
		{name: "next_steps", edit: func(report *domain.CompletionReport) { report.NextSteps = "" }},
	}

	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			goalID := newTestGoal(t, s)
			commanderID := testSessionID("goal-review-incomplete-" + field.name)
			receiverID := testSessionID("goal-review-incomplete-receiver-" + field.name)
			addLiveProjectClaim(t, s, goalID, "goal-review-incomplete-"+field.name)
			addTestAgentSession(t, s, "goal-review-incomplete-receiver-"+field.name)
			receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "goal-review-incomplete-handoff-"+field.name, goalID, commanderID, receiverID)
			report := domain.CompletionReport{
				WorkDone:    "work",
				NowPossible: "result",
				HowToVerify: "verify",
				Surprises:   "surprise",
				NeedsReview: "review",
				NextSteps:   "next",
			}
			field.edit(&report)

			if _, err := s.RequestGoalReview(ctx, goalID, commanderID, report); err == nil {
				t.Fatalf("RequestGoalReview with empty %s succeeded", field.name)
			}
			var count int
			if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM decisions WHERE goal_id = ? AND kind = 'goal_review'", goalID).Scan(&count); err != nil {
				t.Fatalf("count goal reviews: %v", err)
			}
			if count != 0 {
				t.Fatalf("goal review count = %d, want 0 after validation failure", count)
			}
		})
	}
}

func TestFinalizeGoalReviewRequiresApprovedReview(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("goal-review-unapproved")
	receiverID := testSessionID("goal-review-unapproved-receiver")
	addLiveProjectClaim(t, s, goalID, "goal-review-unapproved")
	addTestAgentSession(t, s, "goal-review-unapproved-receiver")
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "goal-review-unapproved-handoff", goalID, commanderID, receiverID)
	if _, err := s.RequestGoalReview(ctx, goalID, commanderID, domain.CompletionReport{
		WorkDone: "work", NowPossible: "result", HowToVerify: "verify",
		Surprises: "surprise", NeedsReview: "review", NextSteps: "next",
	}); err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	if _, err := s.FinalizeGoalReview(ctx, goalID, commanderID); err == nil {
		t.Fatal("FinalizeGoalReview before approval succeeded")
	}
}
