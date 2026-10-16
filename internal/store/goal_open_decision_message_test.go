package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestGoalReviewOpenDecisionErrorNamesBlockingDecision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID, commanderID := reviewableGoalForPostMergeTest(t, s)

	blocking, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: "which option?", AgentSessionID: testSessionID("run-1"),
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	_, err = s.RequestGoalReview(ctx, goalID, commanderID, goalReviewRequestTestReport())
	if !errors.Is(err, ErrGoalHasOpenDecision) {
		t.Fatalf("RequestGoalReview: got %v, want ErrGoalHasOpenDecision", err)
	}
	if want := fmt.Sprintf("decision %d kind=%s status=open", blocking.ID, domain.KindDecision); !strings.Contains(err.Error(), want) {
		t.Fatalf("RequestGoalReview error %q does not contain %q", err, want)
	}

	if err := s.WithdrawDecision(ctx, blocking.ID, "resolved independently"); err != nil {
		t.Fatalf("WithdrawDecision: %v", err)
	}
	// post_merge does not block RequestGoalReview but does block FinalizeGoalReview,
	// so the finalize rejection has to name it.
	postMerge, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindPostMerge, Question: "deploy to production?", AgentSessionID: testSessionID("run-1"),
	})
	if err != nil {
		t.Fatalf("AskDecision post_merge: %v", err)
	}
	review, err := s.RequestGoalReview(ctx, goalID, commanderID, goalReviewRequestTestReport())
	if err != nil {
		t.Fatalf("RequestGoalReview with open post_merge decision: %v", err)
	}
	if _, err := s.ApproveGoalReview(ctx, review.ID); err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}
	_, err = s.FinalizeGoalReview(ctx, goalID, commanderID)
	if !errors.Is(err, ErrGoalHasOpenDecision) {
		t.Fatalf("FinalizeGoalReview: got %v, want ErrGoalHasOpenDecision", err)
	}
	if want := fmt.Sprintf("decision %d kind=%s status=open", postMerge.ID, domain.KindPostMerge); !strings.Contains(err.Error(), want) {
		t.Fatalf("FinalizeGoalReview error %q does not contain %q", err, want)
	}
}
