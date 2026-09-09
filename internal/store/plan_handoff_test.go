package store

import (
	"context"
	"testing"
)

func TestPlanHandoffReviewLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("plan-review-commander")
	subcommanderID := testSessionID("plan-review-subcommander")
	wrongReviewerID := testSessionID("plan-review-wrong-reviewer")
	addLiveProjectClaim(t, s, goalID, "plan-review-commander")
	addTestAgentSession(t, s, "plan-review-subcommander")
	addTestAgentSession(t, s, "plan-review-wrong-reviewer")

	goalHandoff, err := s.RequestGoalHandoff(ctx, "plan-review-goal-handoff", goalID, commanderID, "delegate the goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, goalHandoff.ID, goalID, subcommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	reviewRequested, err := s.RequestPlanHandoffReview(ctx, "plan-review-lifecycle", goalID, subcommanderID, "plan is ready")
	if err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	if reviewRequested.GoalID != goalID || reviewRequested.ReviewRequestedBy != subcommanderID || reviewRequested.ReviewRequestedAt == nil || reviewRequested.ReviewRequestReport != "plan is ready" {
		t.Fatalf("unexpected plan review request: %+v", reviewRequested)
	}

	if _, err := s.ReceivePlanHandoffReview(ctx, reviewRequested.ID, goalID, wrongReviewerID); err == nil {
		t.Fatal("ReceivePlanHandoffReview accepted the wrong reviewer")
	}
	reviewReceived, err := s.ReceivePlanHandoffReview(ctx, reviewRequested.ID, goalID, commanderID)
	if err != nil {
		t.Fatalf("ReceivePlanHandoffReview: %v", err)
	}
	if reviewReceived.ReviewReceivedBy != commanderID || reviewReceived.ReviewReceivedAt == nil {
		t.Fatalf("unexpected plan review receipt: %+v", reviewReceived)
	}

	if _, err := s.RejectPlanHandoffReview(ctx, reviewRequested.ID, goalID, wrongReviewerID, "wrong reviewer"); err == nil {
		t.Fatal("RejectPlanHandoffReview accepted the wrong reviewer")
	}
	rejected, err := s.RejectPlanHandoffReview(ctx, reviewRequested.ID, goalID, commanderID, "revise the plan")
	if err != nil {
		t.Fatalf("RejectPlanHandoffReview: %v", err)
	}
	if rejected.ReviewReceivedBy != 0 || rejected.ReviewReceivedAt != nil || rejected.ReviewRejectReport != "revise the plan" || rejected.ReviewRejectedAt == nil {
		t.Fatalf("plan review rejection did not clear reviewer state: %+v", rejected)
	}

	if _, err := s.RequestPlanHandoffReview(ctx, reviewRequested.ID, goalID, subcommanderID, "revised plan"); err != nil {
		t.Fatalf("second RequestPlanHandoffReview: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, reviewRequested.ID, goalID, commanderID); err != nil {
		t.Fatalf("second ReceivePlanHandoffReview: %v", err)
	}
	completed, err := s.CompletePlanHandoff(ctx, reviewRequested.ID, goalID, commanderID, "plan accepted")
	if err != nil {
		t.Fatalf("CompletePlanHandoff: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "plan accepted" {
		t.Fatalf("unexpected completed plan handoff: %+v", completed)
	}
}
