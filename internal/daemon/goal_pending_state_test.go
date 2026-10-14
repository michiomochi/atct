package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func goalListPendingState(t *testing.T, fixture stopCheckFixture) string {
	t.Helper()
	var root string
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT root_path FROM projects WHERE id = ?`, fixture.projectID).Scan(&root); err != nil {
		t.Fatalf("read project root: %v", err)
	}
	params, _ := json.Marshal(map[string]any{"cwd": root})
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var response struct {
		Goals []struct {
			ID           int64  `json:"id"`
			PendingState string `json:"pending_state"`
		} `json:"goals"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode goal.list: %v", err)
	}
	for _, goal := range response.Goals {
		if goal.ID == fixture.goalID {
			return goal.PendingState
		}
	}
	t.Fatalf("goal.list did not return goal %d", fixture.goalID)
	return ""
}

func TestGoalListMarksApprovedAndRejectedReviewsPending(t *testing.T) {
	ctx := context.Background()

	t.Run("approved", func(t *testing.T) {
		fixture := newStopCheckFixture(t)
		decision := stopCheckGoalReview(t, fixture)
		if _, err := fixture.store.ApproveGoalReview(ctx, decision.ID); err != nil {
			t.Fatalf("ApproveGoalReview: %v", err)
		}
		if got := goalListPendingState(t, fixture); got != store.GoalPendingApprovedComplete {
			t.Fatalf("pending_state = %q, want %q", got, store.GoalPendingApprovedComplete)
		}
	})

	t.Run("rejected until the handoff is rejected", func(t *testing.T) {
		fixture := newStopCheckFixture(t)
		decision := stopCheckGoalReview(t, fixture)
		if err := fixture.store.RejectGoalReview(ctx, decision.ID, "revise"); err != nil {
			t.Fatalf("RejectGoalReview: %v", err)
		}
		if got := goalListPendingState(t, fixture); got != store.GoalPendingRejectedHandoffReject {
			t.Fatalf("pending_state = %q, want %q", got, store.GoalPendingRejectedHandoffReject)
		}
		if _, err := fixture.store.RejectGoalHandoffReview(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.commanderID, "revise"); err != nil {
			t.Fatalf("RejectGoalHandoffReview: %v", err)
		}
		if got := goalListPendingState(t, fixture); got != "" {
			t.Fatalf("pending_state after atct_goal_handoff_review_reject = %q, want none", got)
		}
	})
	t.Run("not after the rejected handoff is resubmitted and received", func(t *testing.T) {
		fixture := newStopCheckFixture(t)
		decision := stopCheckGoalReview(t, fixture)
		if err := fixture.store.RejectGoalReview(ctx, decision.ID, "revise"); err != nil {
			t.Fatalf("RejectGoalReview: %v", err)
		}
		if _, err := fixture.store.RejectGoalHandoffReview(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.commanderID, "revise"); err != nil {
			t.Fatalf("RejectGoalHandoffReview: %v", err)
		}
		if _, err := fixture.store.ReceiveGoalHandoffReviewRejection(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.subcommanderID); err != nil {
			t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
		}
		if _, err := fixture.store.RequestGoalHandoffReview(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.subcommanderID, "fixed"); err != nil {
			t.Fatalf("RequestGoalHandoffReview (resubmit): %v", err)
		}
		if _, err := fixture.store.ReceiveGoalHandoffReview(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.commanderID); err != nil {
			t.Fatalf("ReceiveGoalHandoffReview (resubmit): %v", err)
		}
		if got := goalListPendingState(t, fixture); got != "" {
			t.Fatalf("pending_state after a resubmission was received = %q, want none", got)
		}
	})
	t.Run("a later goal_review supersedes the rejection", func(t *testing.T) {
		fixture := newStopCheckFixture(t)
		first := stopCheckGoalReview(t, fixture)
		if err := fixture.store.RejectGoalReview(ctx, first.ID, "revise"); err != nil {
			t.Fatalf("RejectGoalReview: %v", err)
		}
		if _, err := fixture.store.RejectGoalHandoffReview(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.commanderID, "revise"); err != nil {
			t.Fatalf("RejectGoalHandoffReview: %v", err)
		}
		if _, err := fixture.store.ReceiveGoalHandoffReviewRejection(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.subcommanderID); err != nil {
			t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
		}
		if _, err := fixture.store.RequestGoalHandoffReview(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.subcommanderID, "fixed"); err != nil {
			t.Fatalf("RequestGoalHandoffReview (resubmit): %v", err)
		}
		if _, err := fixture.store.ReceiveGoalHandoffReview(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.commanderID); err != nil {
			t.Fatalf("ReceiveGoalHandoffReview (resubmit): %v", err)
		}
		second, err := fixture.store.RequestGoalReview(ctx, fixture.goalID, fixture.commanderID, domain.CompletionReport{
			WorkDone: "done", NowPossible: "now", HowToVerify: "verify", Surprises: "none", NeedsReview: "none", NextGoalIDs: []int64{},
		})
		if err != nil {
			t.Fatalf("RequestGoalReview (second): %v", err)
		}
		if got, err := fixture.store.GoalPendingState(ctx, fixture.goalID); err != nil || got != "" {
			t.Fatalf("GoalPendingState with a newer open goal_review = %q, %v; want none", got, err)
		}
		// The newer review is rejected in turn: the rejection is owed again.
		if err := fixture.store.RejectGoalReview(ctx, second.ID, "again"); err != nil {
			t.Fatalf("RejectGoalReview (second): %v", err)
		}
		if got := goalListPendingState(t, fixture); got != store.GoalPendingRejectedHandoffReject {
			t.Fatalf("pending_state after the second rejection = %q, want %q", got, store.GoalPendingRejectedHandoffReject)
		}
	})
}
