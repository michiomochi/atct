package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestHTTPWithdrawActiveGoalRejectsDroppedGoal(t *testing.T) {
	f := newBareFixture(t)
	if err := f.store.WithdrawActiveGoal(f.ctx, f.goal.ID, "first withdrawal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodPost, fmt.Sprintf("%s/api/goals/%d/withdraw", srv.URL, f.goal.ID), mustJSON(t, map[string]string{
		"reason": "second withdrawal",
	}))
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusConflict, body)
	}
	var response map[string]string
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, body)
	}
	want := fmt.Sprintf("goal %d is dropped, not active", f.goal.ID)
	if response["error"] != want {
		t.Fatalf("error = %q, want %q", response["error"], want)
	}
}

// A proposed goal appears in its own dashboard section and is approved from the
// goal page. Listing its approval decision in the answers table as well would put
// one act in two places, which is how the two drift apart.
func TestHTTPInboxOmitsGoalApprovalFromOpenDecisions(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)

	proposed, err := f.store.CreateGoal(f.ctx, f.project.ID, "Proposed by an agent", "agent")
	if err != nil {
		t.Fatal(err)
	}

	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}

	var response struct {
		OpenDecisions []struct {
			Kind   string `json:"kind"`
			GoalID int64  `json:"goal_id"`
		} `json:"open_decisions"`
		ProposedGoals []struct {
			ID int64 `json:"id"`
		} `json:"proposed_goals"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}

	for _, decision := range response.OpenDecisions {
		if decision.Kind == string(domain.KindGoalApproval) {
			t.Fatalf("open_decisions contains a goal_approval for %d; it belongs to the proposed section only", decision.GoalID)
		}
	}

	// The goal itself must still be visible, or this would be hiding rather than
	// de-duplicating.
	found := false
	for _, goal := range response.ProposedGoals {
		if goal.ID == proposed.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("proposed_goals does not contain %d", proposed.ID)
	}
}
