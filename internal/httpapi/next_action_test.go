package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

// nextActionCode reads the goal's next_action.code from both the inbox and the
// goal detail. The dashboard shows one of them and links to the other, so they
// must derive the same step at every point in the goal's life.
func nextActionCode(t *testing.T, base string, client *http.Client, goalID int64) (inbox, detail string) {
	t.Helper()

	response, err := client.Get(base + "/api/inbox")
	if err != nil {
		t.Fatalf("GET /api/inbox: %v", err)
	}
	defer response.Body.Close()
	var inboxBody struct {
		ActiveGoals []struct {
			ID         int64 `json:"id"`
			NextAction struct {
				Code string `json:"code"`
			} `json:"next_action"`
		} `json:"active_goals"`
	}
	if err := json.NewDecoder(response.Body).Decode(&inboxBody); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	found := false
	for _, goal := range inboxBody.ActiveGoals {
		if goal.ID == goalID {
			inbox, found = goal.NextAction.Code, true
		}
	}
	if !found {
		t.Fatalf("goal %d is not in active_goals", goalID)
	}

	status, _, body := doRequest(t, client, http.MethodGet, base+"/api/goals/"+strconv.FormatInt(goalID, 10), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/goals/%d status = %d; body=%s", goalID, status, body)
	}
	var detailBody struct {
		Goal struct {
			NextAction struct {
				Code string `json:"code"`
			} `json:"next_action"`
		} `json:"goal"`
	}
	if err := json.Unmarshal(body, &detailBody); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	return inbox, detailBody.Goal.NextAction.Code
}

func TestNextActionInInboxAndGoalDetailAgree(t *testing.T) {
	f := newBareFixture(t)
	ctx := f.ctx
	commander, err := f.store.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(commander): %v", err)
	}
	subcommander, err := f.store.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(subcommander): %v", err)
	}
	if _, err := f.store.ClaimProject(ctx, f.project.ID, commander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	server := httptest.NewServer(httpapi.New(f.store).Handler())
	t.Cleanup(server.Close)
	client := server.Client()

	check := func(state, want string) {
		t.Helper()
		inbox, detail := nextActionCode(t, server.URL, client, f.goal.ID)
		if inbox != want {
			t.Errorf("%s: inbox next_action = %q, want %q", state, inbox, want)
		}
		if detail != want {
			t.Errorf("%s: goal detail next_action = %q, want %q", state, detail, want)
		}
		if inbox != detail {
			t.Errorf("%s: inbox %q and goal detail %q disagree", state, inbox, detail)
		}
	}

	check("just created", "commander_request_handoff")

	const handoffID = "gh-next-action"
	if _, err := f.store.RequestGoalHandoff(ctx, handoffID, f.goal.ID, commander, "delegate"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	check("after goal handoff request", "subcommander_receive_handoff")

	if _, err := f.store.ReceiveGoalHandoff(ctx, handoffID, f.goal.ID, subcommander); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	check("after receive, before plan request", "subcommander_plan")

	if _, err := f.store.UpdateGoalRequestReport(ctx, f.goal.ID, "# Spec", "# Plan"); err != nil {
		t.Fatalf("UpdateGoalRequestReport: %v", err)
	}
	if _, err := f.store.RequestPlanHandoffReview(ctx, handoffID, f.goal.ID, subcommander, "plan for review"); err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	check("after plan review request", "commander_receive_plan_review")
}

func TestNextActionWithdrawnGoalAgreesAcrossEndpoints(t *testing.T) {
	f := newBareFixture(t)
	if err := f.store.WithdrawActiveGoal(f.ctx, f.goal.ID, "no longer needed"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	server := httptest.NewServer(httpapi.New(f.store).Handler())
	t.Cleanup(server.Close)

	status, _, body := doRequest(t, server.Client(), http.MethodGet, server.URL+"/api/goals/"+strconv.FormatInt(f.goal.ID, 10), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/goals/%d status = %d; body=%s", f.goal.ID, status, body)
	}
	var detail struct {
		Goal struct {
			NextAction store.NextAction `json:"next_action"`
		} `json:"goal"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	if got := detail.Goal.NextAction.Code; got != "withdrawn" {
		t.Fatalf("goal detail next_action = %q, want withdrawn", got)
	}
}
