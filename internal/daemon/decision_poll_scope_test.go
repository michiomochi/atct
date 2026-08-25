package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

type decisionPollRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (f unappliedDecisionScopeRPCTestFixture) callDecisionPoll(t *testing.T, agentSessionID, decisionID string) (json.RawMessage, json.RawMessage) {
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

func assertPollReturnedDecision(t *testing.T, result json.RawMessage, decisionID string) map[string]any {
	t.Helper()
	for _, decision := range pollResultDecisions(t, result) {
		if decision["id"] == decisionID {
			return decision
		}
	}
	t.Fatalf("decision.poll result = %s, want decision %q", result, decisionID)
	return nil
}

func assertPollSucceeded(t *testing.T, result json.RawMessage, rpcError json.RawMessage, decisionID string) map[string]any {
	t.Helper()
	if len(rpcError) > 0 && string(rpcError) != "null" {
		t.Fatalf("decision.poll returned error: %s", rpcError)
	}
	return assertPollReturnedDecision(t, result, decisionID)
}

func TestDecisionPollForSubcommanderRefusesOtherGoalDecision(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	_, rpcError := f.callDecisionPoll(t, f.subcommanderSessionID, f.decisionBID)
	if len(rpcError) == 0 || string(rpcError) == "null" {
		t.Fatal("decision.poll succeeded for a decision outside the held goal")
	}
	if !strings.Contains(string(rpcError), f.goalBID) {
		t.Fatalf("decision.poll error = %s, want goal ID %q", rpcError, f.goalBID)
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
		t.Fatalf("decision status = %q, want answered", decision.Status)
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

	result, rpcError := f.callDecisionPoll(t, "decision-scope-answer-b", "")
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
	result, rpcError := f.callDecisionPoll(t, f.subcommanderSessionID, f.decisionAID)
	assertPollSucceeded(t, result, rpcError, f.decisionAID)
}

func TestDecisionPollForCommanderAcceptsOtherGoalDecision(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	result, rpcError := f.callDecisionPoll(t, f.commanderSessionID, f.decisionBID)
	assertPollSucceeded(t, result, rpcError, f.decisionBID)
}

func TestDecisionPollWithoutSessionAcceptsOtherGoalDecision(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	result, rpcError := f.callDecisionPoll(t, "", f.decisionBID)
	assertPollSucceeded(t, result, rpcError, f.decisionBID)
}
