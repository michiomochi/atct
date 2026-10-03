package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

type decisionPollRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (f unappliedDecisionScopeRPCTestFixture) callDecisionPoll(t *testing.T, agentSessionID int64, decisionID int64) (json.RawMessage, json.RawMessage) {
	t.Helper()
	conn, err := net.Dial("unix", f.socketPath)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	defer conn.Close()
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "decision.poll",
		"params": map[string]any{
			"agent_session_id":          agentSessionID,
			"decision_id":               decisionID,
			"include_unapplied_answers": true,
		},
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatalf("encode decision.poll: %v", err)
	}
	var response decisionPollRPCResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("decode decision.poll: %v", err)
	}
	return response.Result, response.Error
}

func pollResultDecisions(t *testing.T, result json.RawMessage) []map[string]any {
	t.Helper()
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		t.Fatalf("decode decision.poll result: %v", err)
	}
	return envelope.Data
}

func assertPollReturnedDecision(t *testing.T, result json.RawMessage, decisionID int64) map[string]any {
	t.Helper()
	for _, decision := range pollResultDecisions(t, result) {
		if got, ok := decision["id"].(float64); ok && int64(got) == decisionID {
			return decision
		}
	}
	t.Fatalf("decision.poll result = %v, want decision %v", result, decisionID)
	return nil
}

func assertPollSucceeded(t *testing.T, result json.RawMessage, rpcError json.RawMessage, decisionID int64) map[string]any {
	t.Helper()
	if len(rpcError) > 0 && string(rpcError) != "null" {
		t.Fatalf("decision.poll returned error: %v", rpcError)
	}
	return assertPollReturnedDecision(t, result, decisionID)
}

func TestDecisionPollForSubcommanderRefusesOtherGoalDecision(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	_, rpcError := f.callDecisionPoll(t, f.subcommanderSessionID, f.decisionBID)
	if len(rpcError) == 0 || string(rpcError) == "null" {
		t.Fatal("decision.poll succeeded for a decision outside the held goal")
	}
	if !strings.Contains(string(rpcError), fmt.Sprint(f.goalBID)) {
		t.Fatalf("decision.poll error = %v, want goal ID %v", rpcError, f.goalBID)
	}
}

func TestDecisionPollRefusalLeavesOtherGoalDecisionAnswered(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	_, rpcError := f.callDecisionPoll(t, f.subcommanderSessionID, f.decisionBID)
	if len(rpcError) == 0 || string(rpcError) == "null" {
		t.Fatal("decision.poll succeeded for a decision outside the held goal")
	}

	decision, err := f.store.GetDecision(t.Context(), f.decisionBID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if decision.Status != domain.DecisionAnswered {
		t.Fatalf("decision status = %v, want answered", decision.Status)
	}
	if decision.AppliedAt != nil {
		t.Fatalf("decision applied_at = %v, want nil", decision.AppliedAt)
	}
}

func TestDecisionPollByOwnerSucceedsAfterRefusal(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	_, rpcError := f.callDecisionPoll(t, f.subcommanderSessionID, f.decisionBID)
	if len(rpcError) == 0 || string(rpcError) == "null" {
		t.Fatal("decision.poll succeeded for a decision outside the held goal")
	}

	result, rpcError := f.callDecisionPoll(t, daemonTestSessionID(t, f.store, "decision-scope-answer-b"), 0)
	decision := assertPollSucceeded(t, result, rpcError, f.decisionBID)
	if decision["status"] != string(domain.DecisionApplied) {
		t.Fatalf("returned decision status = %#v, want applied", decision["status"])
	}
	stored, err := f.store.GetDecision(t.Context(), f.decisionBID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if stored.Status != domain.DecisionApplied || stored.AppliedAt == nil {
		t.Fatalf("stored decision = %#v, want applied with applied_at", stored)
	}
}

func TestDecisionPollForSubcommanderAcceptsOwnGoalDecision(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	decision, err := f.store.GetDecision(t.Context(), f.decisionAID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	result, rpcError := f.callDecisionPoll(t, decision.AgentSessionID, f.decisionAID)
	assertPollSucceeded(t, result, rpcError, f.decisionAID)
}

func TestDecisionPollForCommanderRefusesForeignDecision(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	_, rpcError := f.callDecisionPoll(t, f.commanderSessionID, f.decisionBID)
	if len(rpcError) == 0 || string(rpcError) == "null" {
		t.Fatal("decision.poll succeeded for a foreign-owned decision")
	}
	stored, err := f.store.GetDecision(t.Context(), f.decisionBID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if stored.Status != domain.DecisionAnswered || stored.AppliedAt != nil {
		t.Fatalf("stored decision = %#v, want answered and unapplied", stored)
	}
}

func TestDecisionPollWithoutSessionRefusesForeignDecision(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	_, rpcError := f.callDecisionPoll(t, 0, f.decisionBID)
	if len(rpcError) == 0 || string(rpcError) == "null" {
		t.Fatal("decision.poll succeeded without the owner session")
	}
}

func (f unappliedDecisionScopeRPCTestFixture) answeredDecision(t *testing.T, goalID int64, kind domain.DecisionKind, session int64) int64 {
	t.Helper()
	d, err := f.store.AskDecision(t.Context(), store.AskInput{GoalID: goalID, Kind: kind, Question: "q", AgentSessionID: session})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := f.store.AnswerDecision(t.Context(), store.AnswerInput{DecisionID: d.ID, AnswerText: "a"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	return d.ID
}

func TestDecisionPollForCommanderAcceptsSessionlessGoalReview(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	id := f.answeredDecision(t, f.goalBID, domain.KindGoalReview, 0)
	result, rpcError := f.callDecisionPoll(t, f.commanderSessionID, id)
	decision := assertPollSucceeded(t, result, rpcError, id)
	if decision["status"] != string(domain.DecisionApplied) {
		t.Fatalf("status = %#v, want applied", decision["status"])
	}
	stored, err := f.store.GetDecision(t.Context(), id)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if stored.Status != domain.DecisionApplied {
		t.Fatalf("stored status = %v, want applied", stored.Status)
	}
}

func TestDecisionPollForCommanderWithoutIDAppliesSessionlessGoalReview(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	id := f.answeredDecision(t, f.goalBID, domain.KindGoalReview, 0)
	result, rpcError := f.callDecisionPoll(t, f.commanderSessionID, 0)
	assertPollSucceeded(t, result, rpcError, id)
	stored, err := f.store.GetDecision(t.Context(), id)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if stored.Status != domain.DecisionApplied {
		t.Fatalf("stored status = %v, want applied", stored.Status)
	}
	// a foreign session-owned kind=decision must stay unapplied
	other, err := f.store.GetDecision(t.Context(), f.decisionBID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if other.Status != domain.DecisionAnswered {
		t.Fatalf("foreign decision status = %v, want answered", other.Status)
	}
}

func TestDecisionPollForCommanderRefusesOtherProjectSessionlessDecision(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	project, err := f.store.CreateProject(t.Context(), "other-project", f.projectRoot+"-other")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := f.store.CreateGoal(t.Context(), project.ID, "other goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	id := f.answeredDecision(t, goal.ID, domain.KindGoalReview, 0)
	_, rpcError := f.callDecisionPoll(t, f.commanderSessionID, id)
	if len(rpcError) == 0 || string(rpcError) == "null" {
		t.Fatal("decision.poll succeeded for another project's decision")
	}
	stored, err := f.store.GetDecision(t.Context(), id)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if stored.Status != domain.DecisionAnswered {
		t.Fatalf("stored status = %v, want answered", stored.Status)
	}
}
