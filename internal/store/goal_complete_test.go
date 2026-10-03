package store

import (
	"context"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

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
	if before.Status != domain.GoalActive || before.WorkDone != report.WorkDone || before.NowPossible != report.NowPossible || before.HowToVerify != report.HowToVerify || before.Surprises != report.Surprises || before.NeedsReview != report.NeedsReview || before.ResultSummary != report.WorkDone {
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
		Surprises: "rejected surprise", NeedsReview: "rejected review",
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
	}

	review, err := s.RequestGoalReview(ctx, goalID, commanderID, report)
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	storedGoal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if storedGoal.Status != domain.GoalActive || storedGoal.WorkDone != report.WorkDone || storedGoal.NowPossible != report.NowPossible || storedGoal.HowToVerify != report.HowToVerify || storedGoal.Surprises != report.Surprises || storedGoal.NeedsReview != report.NeedsReview || storedGoal.ResultSummary != report.WorkDone {
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
	if done.Status != domain.GoalDone || done.WorkDone != "request-time work" || done.NowPossible != "request-time result" || done.HowToVerify != "run the request-time tests" || done.Surprises != "request-time surprise" || done.NeedsReview != "request-time review" || done.ResultSummary != "request-time work" {
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
		Surprises: "surprise", NeedsReview: "review",
	}); err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	if _, err := s.FinalizeGoalReview(ctx, goalID, commanderID); err == nil {
		t.Fatal("FinalizeGoalReview before approval succeeded")
	}
}
