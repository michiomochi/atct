package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestPlanHandoffReviewRejectReceiveLifecycle(t *testing.T) {
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

	if _, err := s.RequestPlanHandoffReview(ctx, reviewRequested.ID, goalID, subcommanderID, "revised plan"); err == nil {
		t.Fatal("RequestPlanHandoffReview accepted a rejection that the original submitter has not received")
	}
	if _, err := s.ReceivePlanHandoffReviewRejection(ctx, reviewRequested.ID, goalID, wrongReviewerID); err == nil {
		t.Fatal("ReceivePlanHandoffReviewRejection accepted a foreign session")
	}
	rejectionReceived, err := s.ReceivePlanHandoffReviewRejection(ctx, reviewRequested.ID, goalID, subcommanderID)
	if err != nil {
		t.Fatalf("ReceivePlanHandoffReviewRejection: %v", err)
	}
	if rejectionReceived.ReviewRejectionReceivedBy != subcommanderID || rejectionReceived.ReviewRejectionReceivedAt == nil {
		t.Fatalf("unexpected plan rejection receipt: %+v", rejectionReceived)
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

func TestRecoverPlanHandoffClearsOnlyDefinitelyStaleReviewer(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	staleCommanderID := registerNamedTestAgentSession(t, s, "recover-plan-stale-commander", os.Getpid())
	freshCommanderID := registerNamedTestAgentSession(t, s, "recover-plan-fresh-commander", os.Getpid())
	subcommanderID := registerNamedTestAgentSession(t, s, "recover-plan-subcommander", os.Getpid())
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE projects SET claimed_by = ? WHERE id = ?`, staleCommanderID, goal.ProjectID); err != nil {
		t.Fatalf("claim project for stale commander: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET started_at = 'stale-process-start' WHERE id = ?`, staleCommanderID); err != nil {
		t.Fatalf("make commander definitely stale: %v", err)
	}
	// The stale commander was live when it received the review; only its
	// process identity is now stale. The current commander must be able to
	// reopen that receipt without changing the submitting subcommander.
	handoff, err := s.RequestGoalHandoff(ctx, "recover-plan-goal", goalID, staleCommanderID, "delegate")
	if err == nil {
		t.Fatal("RequestGoalHandoff accepted a definitely stale commander")
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET started_at = ? WHERE id = ?`, processStartedAtOrFail(t, os.Getpid()), staleCommanderID); err != nil {
		t.Fatalf("restore commander identity for setup: %v", err)
	}
	handoff, err = s.RequestGoalHandoff(ctx, "recover-plan-goal", goalID, staleCommanderID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, subcommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	plan, err := s.RequestPlanHandoffReview(ctx, "recover-plan-review", goalID, subcommanderID, "ready")
	if err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, plan.ID, goalID, staleCommanderID); err != nil {
		t.Fatalf("ReceivePlanHandoffReview: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE projects SET claimed_by = ? WHERE id = ?`, freshCommanderID, goal.ProjectID); err != nil {
		t.Fatalf("turn over commander: %v", err)
	}
	if _, err := s.RecoverPlanHandoff(ctx, plan.ID, goalID, freshCommanderID, "live reviewer"); !errors.Is(err, ErrSessionRecoveryNotProven) {
		t.Fatalf("RecoverPlanHandoff with live reviewer error = %v, want ErrSessionRecoveryNotProven", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET started_at = 'stale-process-start' WHERE id = ?`, staleCommanderID); err != nil {
		t.Fatalf("make recorded reviewer stale: %v", err)
	}

	recovered, err := s.RecoverPlanHandoff(ctx, plan.ID, goalID, freshCommanderID, "reviewer process identity changed")
	if err != nil {
		t.Fatalf("RecoverPlanHandoff: %v", err)
	}
	if recovered.ReviewReceivedAt != nil || recovered.ReviewReceivedBy != 0 || recovered.ReviewRequestedBy != subcommanderID || recovered.ReviewRequestReport != "ready" {
		t.Fatalf("recovered plan handoff = %+v, want only reviewer receipt cleared", recovered)
	}
	if _, err := s.RecoverPlanHandoff(ctx, plan.ID, goalID, freshCommanderID, "retry"); err != nil {
		t.Fatalf("RecoverPlanHandoff retry: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, plan.ID, goalID, freshCommanderID); err != nil {
		t.Fatalf("ReceivePlanHandoffReview after recovery: %v", err)
	}
	recoveries, err := s.ListHandoffRecoveriesForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("ListHandoffRecoveriesForGoal: %v", err)
	}
	if len(recoveries) != 1 || recoveries[0].HandoffKind != "plan" || recoveries[0].HandoffID != plan.ID || recoveries[0].StaleSessionID != staleCommanderID || recoveries[0].RecoveredBy != freshCommanderID {
		t.Fatalf("plan recovery audit = %+v", recoveries)
	}
}

func processStartedAtOrFail(t *testing.T, pid int) string {
	t.Helper()
	startedAt, err := processStartedAt(pid)
	if err != nil {
		t.Fatalf("processStartedAt(%d): %v", pid, err)
	}
	return startedAt
}
