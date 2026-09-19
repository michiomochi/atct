package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/michiomochi/atct/internal/httpapi"
)

// A goal whose handoff review is sitting with the commander has no open
// decision, so it rendered exactly like a goal nobody had picked up. The
// waiting is real; it is just not a decision.
func TestInboxMarksAGoalWaitingOnItsHandoffReview(t *testing.T) {
	f := newBareFixture(t)
	ctx := f.ctx
	commander, err := f.store.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := f.store.ClaimProject(ctx, f.project.ID, commander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	if _, err := f.store.RequestGoalHandoff(ctx, "gh-await-review", f.goal.ID, commander, "delegate"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := f.store.ReceiveGoalHandoff(ctx, "gh-await-review", f.goal.ID, commander); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := f.store.RequestGoalHandoffReview(ctx, "gh-await-review", f.goal.ID, commander, "please review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}

	server := httptest.NewServer(httpapi.New(f.store).Handler())
	t.Cleanup(server.Close)

	response, getErr := server.Client().Get(server.URL + "/api/inbox")
	if getErr != nil {
		t.Fatalf("GET /api/inbox: %v", getErr)
	}
	defer response.Body.Close()

	var inbox struct {
		ActiveGoals []struct {
			ID               int64 `json:"id"`
			AwaitingDecision bool  `json:"awaiting_decision"`
			AwaitingReview   bool  `json:"awaiting_review"`
		} `json:"active_goals"`
	}
	if err := json.NewDecoder(response.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	for _, goal := range inbox.ActiveGoals {
		if goal.ID != f.goal.ID {
			continue
		}
		if goal.AwaitingDecision {
			t.Error("a pending review was reported as an open decision; there is none to answer")
		}
		if !goal.AwaitingReview {
			t.Fatal("the goal shows nothing while its handoff review waits to be received")
		}
		return
	}
	t.Fatalf("goal %d is not in active_goals", f.goal.ID)
}
