package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
)

func TestDecisionAskAfterReviewStoresPostMergeKind(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()
	ctx := context.Background()

	sessionID := "decision-after-review-session"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	goalID := fixture.active[0].ID
	params, err := json.Marshal(map[string]any{
		"goal_id":          goalID,
		"question":         "deploy to production?",
		"wait_ms":          0,
		"after_review":     true,
		"agent_session_id": daemonTestSessionID(t, fixture.store, sessionID),
	})
	if err != nil {
		t.Fatalf("marshal decision.ask params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "decision.ask", Params: params}); err != nil {
		t.Fatalf("decision.ask with after_review: %v", err)
	}

	decisions, err := fixture.store.ListDecisionsForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("ListDecisionsForGoal: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Kind != domain.KindPostMerge {
		t.Fatalf("stored decisions = %+v, want one post_merge decision", decisions)
	}
}
