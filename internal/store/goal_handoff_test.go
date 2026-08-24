package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

func addLiveGoalClaim(t *testing.T, s *Store, goalID, sessionID string) {
	t.Helper()

	ctx := context.Background()
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal failed: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, sessionID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, sessionID, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject failed: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goalID, sessionID); err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}
}

func TestGoalHandoffRequestReceiveAndComplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")
	addLiveGoalClaim(t, s, goalID, "goal-claim-owner")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-handoff-1", goalID, "goal-requester", "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff failed: %v", err)
	}
	if handoff.ID == "" || handoff.GoalID != goalID {
		t.Fatalf("unexpected requested handoff: %+v", handoff)
	}
	if handoff.RequestedAt == nil || handoff.ReceivedAt != nil || handoff.CompletedReportAt != nil {
		t.Fatalf("unexpected new handoff state: %+v", handoff)
	}

	unreceived, err := s.GetGoalHandoff(ctx, handoff.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff before receive failed: %v", err)
	}
	if unreceived.ReceivedAt != nil {
		t.Fatalf("unreceived handoff has received timestamp: %+v", unreceived)
	}

	received, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, "goal-receiver")
	if err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	if received.ID != handoff.ID || received.ReceivedBy != "goal-receiver" || received.ReceivedAt == nil {
		t.Fatalf("unexpected received handoff: %+v", received)
	}
	if received.RequestedAt == nil || received.CompletedReportAt != nil {
		t.Fatalf("receive must preserve request and incomplete state: %+v", received)
	}

	completed, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "")
	if err != nil {
		t.Fatalf("CompleteGoalHandoff failed: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.RequestedAt == nil || completed.ReceivedAt == nil {
		t.Fatalf("completion must preserve all prior timestamps: %+v", completed)
	}
}

func TestGoalHandoffReportsAreStored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-report-requester")
	addTestAgentSession(t, s, "goal-report-receiver")
	addLiveGoalClaim(t, s, goalID, "goal-report-claim-owner")

	requested, err := s.RequestGoalHandoff(ctx, "goal-report-handoff", goalID, "goal-report-requester", "Please take over the goal.")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if requested.RequestReport != "Please take over the goal." {
		t.Fatalf("request report = %q, want request body", requested.RequestReport)
	}

	if _, err := s.ReceiveGoalHandoff(ctx, requested.ID, goalID, "goal-report-receiver"); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	completed, err := s.CompleteGoalHandoff(ctx, requested.ID, goalID, "I completed the goal and verified it.")
	if err != nil {
		t.Fatalf("CompleteGoalHandoff: %v", err)
	}
	if completed.CompleteReport != "I completed the goal and verified it." {
		t.Fatalf("complete report = %q, want completion body", completed.CompleteReport)
	}
}

func TestGoalHandoffReportsMayBeOmitted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-empty-report-requester")
	addLiveGoalClaim(t, s, goalID, "goal-empty-report-claim-owner")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-empty-report-handoff", goalID, "goal-empty-report-requester", "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff without report: %v", err)
	}
	if handoff.RequestReport != "" {
		t.Fatalf("omitted request report = %q, want empty", handoff.RequestReport)
	}

	completed, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "")
	if err != nil {
		t.Fatalf("CompleteGoalHandoff without report: %v", err)
	}
	if completed.CompleteReport != "" {
		t.Fatalf("omitted complete report = %q, want empty", completed.CompleteReport)
	}
}

func TestGoalHandoffAllowsSecondHandoffForSameGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-requester")
	addLiveGoalClaim(t, s, goalID, "goal-second-claim-owner")

	first, err := s.RequestGoalHandoff(ctx, "goal-handoff-1", goalID, "goal-requester", "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	second, err := s.RequestGoalHandoff(ctx, "goal-handoff-2", goalID, "goal-requester", "")
	if err != nil {
		t.Fatalf("second RequestGoalHandoff failed: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("same goal handoffs must have distinct IDs: %q", first.ID)
	}

	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs failed: %v", err)
	}
	if len(handoffs) != 2 {
		t.Fatalf("got %d handoffs, want 2: %+v", len(handoffs), handoffs)
	}
}

func TestGoalHandoffReceiveByGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")
	addLiveGoalClaim(t, s, goalID, "goal-receive-claim-owner")

	requested, err := s.RequestGoalHandoff(ctx, "goal-receive-by-goal", goalID, "goal-requester", "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff failed: %v", err)
	}

	received, err := s.ReceiveGoalHandoffForGoal(ctx, goalID, "goal-receiver")
	if err != nil {
		t.Fatalf("ReceiveGoalHandoffForGoal failed: %v", err)
	}
	if received.ID != requested.ID || received.ReceivedBy != "goal-receiver" {
		t.Fatalf("unexpected received handoff: %+v", received)
	}
}

func TestGoalHandoffReceiveByGoalRejectsMultipleUnreceived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")
	addLiveGoalClaim(t, s, goalID, "goal-ambiguous-claim-owner")

	for _, handoffID := range []string{"goal-ambiguous-1", "goal-ambiguous-2"} {
		if _, err := s.RequestGoalHandoff(ctx, handoffID, goalID, "goal-requester", ""); err != nil {
			t.Fatalf("RequestGoalHandoff(%q) failed: %v", handoffID, err)
		}
	}

	_, err := s.ReceiveGoalHandoffForGoal(ctx, goalID, "goal-receiver")
	if !errors.Is(err, ErrGoalHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrGoalHandoffAmbiguous", err)
	}
}

func TestGoalHandoffRejectsMissingGoal(t *testing.T) {
	s := newTestStore(t)
	addTestAgentSession(t, s, "goal-requester")

	if _, err := s.RequestGoalHandoff(context.Background(), "goal-handoff-missing", "missing-goal", "goal-requester", ""); err == nil {
		t.Fatal("RequestGoalHandoff should reject a missing goal")
	}
}

func TestGoalHandoffRejectsUnclaimedGoal(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-requester")

	_, err := s.RequestGoalHandoff(context.Background(), "goal-handoff-unclaimed", goalID, "goal-requester", "")
	if !errors.Is(err, ErrGoalHandoffGoalUnclaimed) {
		t.Fatalf("error = %v, want ErrGoalHandoffGoalUnclaimed", err)
	}
}
