package store

import (
	"context"
	"errors"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

// reviewableGoalForPostMergeTest returns a goal whose delegated handoff has
// been reviewed, so RequestGoalReview gets past its handoff check and reaches
// the open-decision check under test.
func reviewableGoalForPostMergeTest(t *testing.T, s *Store) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "post-merge-requester")
	addTestAgentSession(t, s, "post-merge-receiver")
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "post-merge-handoff", goalID, testSessionID("post-merge-requester"), testSessionID("post-merge-receiver"))
	return goalID, testSessionID("post-merge-requester")
}

func TestOpenPostMergeDecisionDoesNotBlockGoalReview(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID, commanderID := reviewableGoalForPostMergeTest(t, s)
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindPostMerge, Question: "deploy to production?", AgentSessionID: testSessionID("run-1"),
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	review, err := s.RequestGoalReview(ctx, goalID, commanderID, goalReviewRequestTestReport())
	if err != nil {
		t.Fatalf("RequestGoalReview with open post_merge decision: %v", err)
	}
	if _, err := s.ApproveGoalReview(ctx, review.ID); err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}
	if _, err := s.FinalizeGoalReview(ctx, goalID, commanderID); !errors.Is(err, ErrGoalHasOpenDecision) {
		t.Fatalf("FinalizeGoalReview with open post_merge decision: got %v, want ErrGoalHasOpenDecision", err)
	}
}

func TestOpenDecisionStillBlocksGoalReview(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID, commanderID := reviewableGoalForPostMergeTest(t, s)
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: "which option?", AgentSessionID: testSessionID("run-1"),
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	if _, err := s.RequestGoalReview(ctx, goalID, commanderID, goalReviewRequestTestReport()); !errors.Is(err, ErrGoalHasOpenDecision) {
		t.Fatalf("RequestGoalReview with open decision: got %v, want ErrGoalHasOpenDecision", err)
	}
}
