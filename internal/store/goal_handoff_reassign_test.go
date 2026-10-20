package store

import (
	"context"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestReceiveGoalHandoffMovesUnappliedDecisionsToNewReceiver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "reassign-commander")
	// The old receiver never heartbeats, so the handoff can move on.
	registerNamedTestAgentSession(t, s, "reassign-old", 0)
	addTestAgentSession(t, s, "reassign-new")
	commander := testSessionID("reassign-commander")
	oldSub := testSessionID("reassign-old")
	newSub := testSessionID("reassign-new")
	if _, err := s.RequestGoalHandoff(ctx, "reassign-handoff", goalID, commander, "delegate"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, "reassign-handoff", goalID, oldSub); err != nil {
		t.Fatalf("ReceiveGoalHandoff old: %v", err)
	}

	ask := func(question string, session int64) int64 {
		t.Helper()
		d, err := s.AskDecision(ctx, AskInput{GoalID: goalID, Kind: domain.KindDecision, Question: question, AgentSessionID: session})
		if err != nil {
			t.Fatalf("AskDecision %q: %v", question, err)
		}
		return d.ID
	}
	open := ask("open", oldSub)
	answered := ask("answered", oldSub)
	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: answered, AnswerLabel: "yes"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	applied := ask("applied", oldSub)
	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: applied, AnswerLabel: "yes"}); err != nil {
		t.Fatalf("AnswerDecision applied: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE decisions SET applied_at = ? WHERE id = ?`, "2026-10-20T00:00:00Z", applied); err != nil {
		t.Fatalf("set applied_at: %v", err)
	}
	owned := ask("commander", commander)

	if _, err := s.ReceiveGoalHandoff(ctx, "reassign-handoff", goalID, newSub); err != nil {
		t.Fatalf("ReceiveGoalHandoff new: %v", err)
	}

	want := map[int64]int64{open: newSub, answered: newSub, applied: oldSub, owned: commander}
	for id, session := range want {
		d, err := s.GetDecision(ctx, id)
		if err != nil {
			t.Fatalf("GetDecision %d: %v", id, err)
		}
		if d.AgentSessionID != session {
			t.Errorf("decision %d agent_session_id = %d, want %d", id, d.AgentSessionID, session)
		}
	}
	if got := reconciledTargetRoles(t, s, goalID)[open]; got != "subcommander" {
		t.Errorf("moved decision target_role = %q, want subcommander", got)
	}
	if d, _ := s.GetDecision(ctx, answered); d.Status != domain.DecisionAnswered || d.AnswerLabel != "yes" {
		t.Errorf("answered decision changed: status=%q label=%q", d.Status, d.AnswerLabel)
	}
}
