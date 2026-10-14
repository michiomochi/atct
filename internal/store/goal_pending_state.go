package store

import (
	"context"
	"fmt"

	"github.com/michiomochi/atct/internal/domain"
)

const (
	GoalPendingApprovedComplete      = "approved_pending_complete"
	GoalPendingRejectedHandoffReject = "rejected_pending_handoff_reject"
)

// GoalPendingState marks a goal whose human review has been answered but whose
// next commander step is not done yet. It only reads: the approve/complete gate
// (FinalizeGoalReview) does not consult it. Empty means neither state applies.
func (s *Store) GoalPendingState(ctx context.Context, goalID int64) (string, error) {
	decisions, err := s.ListDecisionsForGoal(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list decisions for goal %d: %w", goalID, err)
	}
	var latest domain.Decision
	hasReview := false
	for _, decision := range decisions {
		if decision.Kind != domain.KindGoalReview {
			continue
		}
		if decision.Status == domain.DecisionApplied && decision.AnswerLabel == "approve" {
			return GoalPendingApprovedComplete, nil
		}
		latest, hasReview = decision, true
	}
	if !hasReview || latest.Status != domain.DecisionAnswered || latest.AnswerLabel != "reject" {
		return "", nil
	}
	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list goal handoffs for goal %d: %w", goalID, err)
	}
	for _, handoff := range handoffs {
		// A resubmission clears review_rejected_at on the same handoff while the
		// latest goal_review stays rejected, so a review requested after the
		// rejection was answered is the resubmission, not work still owed.
		resubmitted := latest.AnsweredAt != nil && handoff.ReviewRequestedAt != nil && handoff.ReviewRequestedAt.After(*latest.AnsweredAt)
		if handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil && !resubmitted {
			return GoalPendingRejectedHandoffReject, nil
		}
	}
	return "", nil
}
