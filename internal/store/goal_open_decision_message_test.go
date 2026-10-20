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
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "open-decision-message-requester")
	addTestAgentSession(t, s, "open-decision-message-receiver")
	commanderID := testSessionID("open-decision-message-requester")
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "open-decision-message-handoff", goalID, commanderID, testSessionID("open-decision-message-receiver"))

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
	// Once the review is approved, a decision asked afterwards is what blocks
	// FinalizeGoalReview, so that rejection has to name it too.
	review, err := s.RequestGoalReview(ctx, goalID, commanderID, goalReviewRequestTestReport())
	if err != nil {
		t.Fatalf("RequestGoalReview with no open decision: %v", err)
	}
	if _, err := s.ApproveGoalReview(ctx, review.ID); err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}
	postMerge, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindPostMerge, Question: "deploy to production?", AgentSessionID: testSessionID("run-1"),
	})
	if err != nil {
		t.Fatalf("AskDecision post_merge: %v", err)
	}
	_, err = s.FinalizeGoalReview(ctx, goalID, commanderID)
	if !errors.Is(err, ErrGoalHasOpenDecision) {
		t.Fatalf("FinalizeGoalReview: got %v, want ErrGoalHasOpenDecision", err)
	}
	if want := fmt.Sprintf("decision %d kind=%s status=open", postMerge.ID, domain.KindPostMerge); !strings.Contains(err.Error(), want) {
		t.Fatalf("FinalizeGoalReview error %q does not contain %q", err, want)
	}
}
