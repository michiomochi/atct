package store

import (
	"context"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestPollMarksApplied(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "poll-marks-applied")

	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", AgentSessionID: testSessionID("run-1"),
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{
		DecisionID: d.ID, AnswerText: "A",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}

	unapplied, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		t.Fatalf("ListUnappliedDecisions: %v", err)
	}
	if len(unapplied) != 1 {
		t.Fatalf("%d unapplied, want 1", len(unapplied))
	}

	got, err := s.PollDecisions(ctx, testSessionID("run-1"), 0)
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(got) != 1 || got[0].Status != domain.DecisionApplied {
		t.Fatalf("poll returned %+v, want 1 applied decision", got)
	}

	again, err := s.PollDecisions(ctx, testSessionID("run-1"), 0)
	if err != nil {
		t.Fatalf("second PollDecisions: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second poll returned %d decisions, want 0", len(again))
	}
}

func TestPollAdoptsDecisionByExplicitIDAcrossAgentSessions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d := answeredDecisionForAgentSession(t, s, goalID, "run-a")
	got, err := s.PollDecisions(ctx, testSessionID("run-b"), d.ID)
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(got) != 1 || got[0].ID != d.ID || got[0].Status != domain.DecisionApplied {
		t.Fatalf("poll returned %+v, want decision %d applied", got, d.ID)
	}
}

func TestPollWithoutDecisionIDKeepsAgentSessionFilter(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d := answeredDecisionForAgentSession(t, s, goalID, "run-a")
	got, err := s.PollDecisions(ctx, testSessionID("run-b"), 0)
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("poll returned %+v, want no decisions", got)
	}

	unapplied, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		t.Fatalf("ListUnappliedDecisions: %v", err)
	}
	if len(unapplied) != 1 || unapplied[0].ID != d.ID {
		t.Fatalf("unapplied decisions = %+v, want decision %d", unapplied, d.ID)
	}
}

func TestPollAdoptionPreservesOriginalAgentSessionID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	d := answeredDecisionForAgentSession(t, s, goalID, "run-a")
	got, err := s.PollDecisions(ctx, testSessionID("run-b"), d.ID)
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("poll returned %d decisions, want 1", len(got))
	}
	if got[0].AgentSessionID != testSessionID("run-a") {
		t.Fatalf("adopted decision agent_session_id = %d, want original run-a (%d)", got[0].AgentSessionID, testSessionID("run-a"))
	}
}

func answeredDecisionForAgentSession(t *testing.T, s *Store, goalID int64, agentSessionID string) domain.Decision {
	t.Helper()
	ctx := context.Background()
	sessionID := testSessionID(agentSessionID)
	taskID := newTestDecisionTask(t, s, goalID, agentSessionID)
	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", AgentSessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{
		DecisionID: d.ID, AnswerText: "A",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	return d
}
