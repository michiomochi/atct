package daemon

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

const subAskedQuote = "Aにしてください"

// The subcommander of goal A asked the decision before decision.ask was closed
// to subcommanders; the commander of the project records the human's answer.
func TestChatAnswerCommanderAnswersSubcommanderDecision(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.writeHumanMessage(t, f.cmdKey, "pid-cmd-sub", subAskedQuote, answerTime(d))

	if _, rpcError := f.answerRPC(t, f.commanderSessionID, d.ID, "A", "", subAskedQuote); refused(rpcError) {
		t.Fatalf("commander refused to answer a subcommander's decision: %s", rpcError)
	}
	got, err := f.store.GetDecision(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.AnsweredVia != "chat" || got.AnswerLabel != "A" || got.AgentSessionID != f.subcommanderSessionID {
		t.Fatalf("decision = %+v, want answered via chat with label A, still asked by the subcommander", got)
	}
}

func TestChatAnswerRefusesCommanderOfAnotherProject(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	other, err := f.store.CreateProject(t.Context(), "other-project", filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	otherCommander := daemonTestSessionID(t, f.store, "chat-answer-other-commander")
	if err := f.store.AssociateAgentSessionWithProject(t.Context(), otherCommander, other.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := f.store.ClaimProject(t.Context(), other.ID, otherCommander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	otherKey := "chat-answer-other-cmd"
	if _, _, err := f.store.IdentifyAgentSession(t.Context(), otherCommander, otherKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	f.writeHumanMessage(t, otherKey, "pid-other-cmd", subAskedQuote, answerTime(d))

	_, rpcError := f.answerRPC(t, otherCommander, d.ID, "A", "", subAskedQuote)
	if !refused(rpcError) || !strings.Contains(string(rpcError), "owned by another agent session") {
		t.Fatalf("commander of another project: error = %s, want owned by another agent session", rpcError)
	}
}

// A session that never received a goal handoff of the goal is an executor; its
// decision is not the commander's to answer.
func TestChatAnswerRefusesCommanderForDecisionOfSessionWithoutGoalHandoff(t *testing.T) {
	f := newChatAnswerFixture(t)
	d, err := f.store.AskDecision(t.Context(), store.AskInput{
		GoalID: f.goalAID, Kind: domain.KindDecision, Question: "which option?",
		Options:        []domain.Option{{Label: "A", Description: "a"}},
		AgentSessionID: f.swappedSessionID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	f.writeHumanMessage(t, f.cmdKey, "pid-cmd-exec", subAskedQuote, answerTime(d))

	_, rpcError := f.answerRPC(t, f.commanderSessionID, d.ID, "A", "", subAskedQuote)
	if !refused(rpcError) || !strings.Contains(string(rpcError), "owned by another agent session") {
		t.Fatalf("executor's decision: error = %s, want owned by another agent session", rpcError)
	}
}

// A goal review the subcommander asked gets no shortcut for the asker.
func TestChatAnswerGoalReviewAskedBySubcommanderStaysCommanderOnly(t *testing.T) {
	f := newChatAnswerFixture(t)
	review := goalReviewAsk(t, f, f.subcommanderSessionID)
	line := "approve #" + fmt.Sprint(review.ID)
	f.writeHumanMessage(t, f.subKey, "pid-sub-review", line, answerTime(review))

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, review.ID, "approve", "", line); !refused(rpcError) {
		t.Fatalf("subcommander answered a goal review it asked")
	}
}

func TestChatAnswerCommanderStillNeedsHumanQuote(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.writeTranscriptLine(t, f.cmdKey, "pid-cmd-agent", subAskedQuote, false, answerTime(d))

	if _, rpcError := f.answerRPC(t, f.commanderSessionID, d.ID, "A", "", subAskedQuote); !refused(rpcError) {
		t.Fatalf("commander recorded an answer whose quote is not a human message")
	}
	got, err := f.store.GetDecision(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionOpen {
		t.Fatalf("decision status = %s after refused answer, want open", got.Status)
	}
}
