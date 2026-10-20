package daemon

import (
	"encoding/json"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

// A subcommander cannot file a decision to the human. It asks through its goal
// handoff, the commander files it, the human answers in the commander's chat,
// and the commander returns the answer on the same handoff.
func TestSubcommanderDecisionGoesThroughCommanderOverGoalHandoff(t *testing.T) {
	f := newChatAnswerFixture(t)
	const handoffID = "decision-scope-goal-handoff"
	rpc := func(method string, params map[string]any) json.RawMessage {
		t.Helper()
		result, rpcError := chatAnswerRPC(t, f.socketPath, method, params)
		if refused(rpcError) {
			t.Fatalf("%s refused: %s", method, rpcError)
		}
		return result
	}
	var entry struct {
		ID int64 `json:"id"`
	}

	// 1. The subcommander writes its question as a review_requested entry.
	json.Unmarshal(rpc("goal.handoff.entry.append", map[string]any{
		"handoff_id": handoffID, "goal_id": f.goalAID, "kind": "review_requested",
		"body": "which option? A or B. recommend A", "agent_session_id": f.subcommanderSessionID,
	}), &entry)
	requestID := entry.ID
	if requestID == 0 {
		t.Fatal("review_requested entry has no id")
	}

	// 2. The subcommander cannot file the decision itself.
	_, rpcError := chatAnswerRPC(t, f.socketPath, "decision.ask", map[string]any{
		"goal_id": f.goalAID, "question": "which option?", "wait_ms": 0, "agent_session_id": f.subcommanderSessionID,
	})
	if !refused(rpcError) {
		t.Fatal("subcommander decision.ask was accepted")
	}

	// 3. The commander files it and owns it.
	var asked struct {
		DecisionID int64 `json:"decision_id"`
	}
	json.Unmarshal(rpc("decision.ask", map[string]any{
		"goal_id": f.goalAID, "question": "which option?", "wait_ms": 0, "agent_session_id": f.commanderSessionID,
		"options": []domain.Option{{Label: "A", Description: "a"}, {Label: "B", Description: "b"}},
	}), &asked)
	decision, err := f.store.GetDecision(t.Context(), asked.DecisionID)
	if err != nil || asked.DecisionID == 0 {
		t.Fatalf("GetDecision(%d) = (%+v, %v)", asked.DecisionID, decision, err)
	}
	if decision.AgentSessionID != f.commanderSessionID {
		t.Fatalf("decision asked by session %d, want the commander %d", decision.AgentSessionID, f.commanderSessionID)
	}

	// 4. The human answers in the commander's chat; the commander records it and applies it.
	f.writeHumanMessage(t, f.cmdKey, "pid-flow", "Aにしてください", answerTime(decision))
	rpc("decision.answer", map[string]any{
		"decision_id": decision.ID, "answer": "A", "human_quote": "Aにしてください", "agent_session_id": f.commanderSessionID,
	})
	rpc("decision.poll", map[string]any{"decision_id": decision.ID, "agent_session_id": f.commanderSessionID})
	applied, err := f.store.GetDecision(t.Context(), decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if applied.Status != domain.DecisionApplied || applied.AnswerLabel != "A" || applied.AnsweredVia != "chat" {
		t.Fatalf("decision = %+v, want applied with label A via chat", applied)
	}

	// 5. The commander replies on the handoff; the subcommander reads it.
	json.Unmarshal(rpc("goal.handoff.entry.append", map[string]any{
		"handoff_id": handoffID, "goal_id": f.goalAID, "kind": "review_received",
		"body": "the human chose A", "in_reply_to_id": requestID, "agent_session_id": f.commanderSessionID,
	}), &entry)
	var history struct {
		Entries []struct {
			ID              int64  `json:"id"`
			Kind            string `json:"kind"`
			Body            string `json:"body"`
			InReplyToID     *int64 `json:"in_reply_to_id"`
			AuthorSessionID int64  `json:"author_session_id"`
		} `json:"entries"`
	}
	json.Unmarshal(rpc("goal.handoff.entry.history", map[string]any{
		"handoff_id": handoffID, "goal_id": f.goalAID, "agent_session_id": f.subcommanderSessionID,
	}), &history)
	for _, e := range history.Entries {
		if e.ID == entry.ID {
			if e.Kind != "review_received" || e.Body != "the human chose A" || e.AuthorSessionID != f.commanderSessionID ||
				e.InReplyToID == nil || *e.InReplyToID != requestID {
				t.Fatalf("reply entry = %+v, want review_received by the commander replying to %d", e, requestID)
			}
			return
		}
	}
	t.Fatalf("subcommander's history %+v does not hold the commander's reply %d", history.Entries, entry.ID)
}
