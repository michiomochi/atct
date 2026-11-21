package store

import (
	"context"
	"os"
	"testing"
)

// A rejected plan review could only be received by the session that submitted
// it. When that session is gone the rejection is stranded: the goal's current
// subcommander cannot pick it up and no other role can either. Goals 260 and
// 287 both stopped there, and their subcommanders were right to report that no
// state change was available.
func TestPlanRejectionIsReceivableByTheCurrentGoalHolder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}

	commander := registerNamedTestAgentSession(t, s, "plan-orphan-commander", os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, commander, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimProject(ctx, goal.ProjectID, commander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	// The subcommander that submits the plan and then disappears.
	gone := registerNamedTestAgentSession(t, s, "plan-orphan-gone", os.Getpid())
	if _, err := s.RequestGoalHandoff(ctx, "gh-plan-orphan", goalID, commander, "delegate"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, "gh-plan-orphan", goalID, gone); err != nil {
		t.Fatalf("ReceiveGoalHandoff(gone): %v", err)
	}
	if _, err := s.UpdateGoalRequestReport(ctx, goalID, "# Spec", "# Plan"); err != nil {
		t.Fatalf("UpdateGoalRequestReport: %v", err)
	}
	if _, err := s.RequestPlanHandoffReview(ctx, "plan-orphan", goalID, gone, "review the plan"); err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, "plan-orphan", goalID, commander); err != nil {
		t.Fatalf("ReceivePlanHandoffReview: %v", err)
	}
	if _, err := s.RejectPlanHandoffReview(ctx, "plan-orphan", goalID, commander, "needs more detail"); err != nil {
		t.Fatalf("RejectPlanHandoffReview: %v", err)
	}

	// Its lease lapses and a successor takes the goal over.
	expireTestAgentSessionLease(t, s, gone)
	successor := registerNamedTestAgentSession(t, s, "plan-orphan-successor", os.Getpid())
	if _, err := s.ReceiveGoalHandoff(ctx, "gh-plan-orphan", goalID, successor); err != nil {
		t.Fatalf("ReceiveGoalHandoff(successor): %v", err)
	}

	if _, err := s.ReceivePlanHandoffReviewRejection(ctx, "plan-orphan", goalID, successor); err != nil {
		t.Fatalf("the goal's current holder cannot receive the rejection left behind: %v", err)
	}
}

// Someone with no claim on the goal still cannot take its rejection.
func TestPlanRejectionRefusesASessionOutsideTheGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	commander := registerNamedTestAgentSession(t, s, "plan-outsider-commander", os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, commander, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimProject(ctx, goal.ProjectID, commander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	holder := registerNamedTestAgentSession(t, s, "plan-outsider-holder", os.Getpid())
	if _, err := s.RequestGoalHandoff(ctx, "gh-plan-outsider", goalID, commander, "delegate"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, "gh-plan-outsider", goalID, holder); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.UpdateGoalRequestReport(ctx, goalID, "# Spec", "# Plan"); err != nil {
		t.Fatalf("UpdateGoalRequestReport: %v", err)
	}
	if _, err := s.RequestPlanHandoffReview(ctx, "plan-outsider", goalID, holder, "review the plan"); err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, "plan-outsider", goalID, commander); err != nil {
		t.Fatalf("ReceivePlanHandoffReview: %v", err)
	}
	if _, err := s.RejectPlanHandoffReview(ctx, "plan-outsider", goalID, commander, "needs more detail"); err != nil {
		t.Fatalf("RejectPlanHandoffReview: %v", err)
	}

	outsider := registerNamedTestAgentSession(t, s, "plan-outsider-stranger", os.Getpid())
	if _, err := s.ReceivePlanHandoffReviewRejection(ctx, "plan-outsider", goalID, outsider); err == nil {
		t.Fatal("a session with no hold on the goal received its plan rejection")
	}
}
