package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

// A goal with an unanswered decision, whatever its kind, gets no human goal
// review request, and the refusal names the decision. Withdrawing it lets the
// request through. Goal 438.
func TestOpenDecisionBlocksGoalReviewRequest(t *testing.T) {
	for _, kind := range []domain.DecisionKind{domain.KindDecision, domain.KindPostMerge} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			goalID := newTestGoal(t, s)
			addLiveProjectClaim(t, s, goalID, "open-decision-requester")
			addTestAgentSession(t, s, "open-decision-receiver")
			commanderID := testSessionID("open-decision-requester")
			receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "open-decision-handoff", goalID, commanderID, testSessionID("open-decision-receiver"))
			decision, err := s.AskDecision(ctx, AskInput{GoalID: goalID, Kind: kind, Question: "deploy?", AgentSessionID: testSessionID("run-1")})
			if err != nil {
				t.Fatalf("AskDecision: %v", err)
			}

			_, err = s.RequestGoalReview(ctx, goalID, commanderID, goalReviewRequestTestReport())
			if !errors.Is(err, ErrGoalHasOpenDecision) || !strings.Contains(err.Error(), "decision "+strconv.FormatInt(decision.ID, 10)+" kind="+string(kind)) {
				t.Fatalf("RequestGoalReview with open %s decision: got %v, want ErrGoalHasOpenDecision naming it", kind, err)
			}
			if err := s.WithdrawDecision(ctx, decision.ID, "resolved"); err != nil {
				t.Fatalf("WithdrawDecision: %v", err)
			}
			if _, err := s.RequestGoalReview(ctx, goalID, commanderID, goalReviewRequestTestReport()); err != nil {
				t.Fatalf("RequestGoalReview after withdrawing: %v", err)
			}
		})
	}
}

// The subcommander's own review request is refused too, so the decision's
// owner hears about it instead of the commander who cannot answer it.
func TestOpenDecisionBlocksGoalHandoffReviewRequest(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "handoff-requester")
	addTestAgentSession(t, s, "handoff-receiver")
	receiverID := testSessionID("handoff-receiver")
	handoff, err := s.RequestGoalHandoff(ctx, "handoff-open-decision", goalID, testSessionID("handoff-requester"), "delegate goal work")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	decision, err := s.AskDecision(ctx, AskInput{GoalID: goalID, Kind: domain.KindPostMerge, Question: "deploy?", AgentSessionID: receiverID})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	_, err = s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, receiverID, "ready")
	if !errors.Is(err, ErrGoalHasOpenDecision) || !strings.Contains(err.Error(), "decision "+strconv.FormatInt(decision.ID, 10)) {
		t.Fatalf("RequestGoalHandoffReview with open decision: got %v, want ErrGoalHasOpenDecision naming it", err)
	}
	if err := s.WithdrawDecision(ctx, decision.ID, "resolved"); err != nil {
		t.Fatalf("WithdrawDecision: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, receiverID, "ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview after withdrawing: %v", err)
	}
}
