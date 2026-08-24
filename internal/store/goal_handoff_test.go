package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
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

func addRequestOnlyGoalHandoff(t *testing.T, s *Store, handoffID, goalID, requestedBy string) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := sqlcgen.New(s.DB()).RequestGoalHandoff(context.Background(), sqlcgen.RequestGoalHandoffParams{
		ID:            handoffID,
		GoalID:        goalID,
		RequestedBy:   sql.NullString{String: requestedBy, Valid: true},
		RequestedAt:   sql.NullString{String: now, Valid: true},
		RequestReport: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("insert request-only goal handoff failed: %v", err)
	}
}

func addGoalHandoffDirect(t *testing.T, s *Store, handoffID, goalID, requestedBy, receivedBy string) {
	t.Helper()

	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `DROP INDEX IF EXISTS idx_goal_handoffs_open_goal_id`); err != nil {
		t.Fatalf("drop goal handoff uniqueness index: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM goal_handoffs WHERE id = ?`, handoffID); err != nil {
			t.Errorf("delete direct goal handoff %q: %v", handoffID, err)
		}
		if _, err := s.DB().ExecContext(ctx, `
			CREATE UNIQUE INDEX idx_goal_handoffs_open_goal_id
			ON goal_handoffs(goal_id)
			WHERE completed_report_at IS NULL
		`); err != nil {
			t.Errorf("restore goal handoff uniqueness index: %v", err)
		}
	})

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var err error
	if receivedBy == "" {
		_, err = s.DB().ExecContext(ctx, `
			INSERT INTO goal_handoffs (
				id, goal_id, requested_by, requested_at, request_report,
				received_by, received_at, completed_report_at, complete_report
			) VALUES (?, ?, ?, ?, NULL, NULL, NULL, NULL, NULL)
		`, handoffID, goalID, requestedBy, now)
	} else {
		_, err = s.DB().ExecContext(ctx, `
			INSERT INTO goal_handoffs (
				id, goal_id, requested_by, requested_at, request_report,
				received_by, received_at, completed_report_at, complete_report
			) VALUES (?, ?, ?, ?, NULL, ?, ?, NULL, NULL)
		`, handoffID, goalID, requestedBy, now, receivedBy, now)
	}
	if err != nil {
		t.Fatalf("insert direct goal handoff %q failed: %v", handoffID, err)
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
	addTestAgentSession(t, s, "goal-dead-receiver")
	addLiveGoalClaim(t, s, goalID, "goal-second-claim-owner")

	first, err := s.RequestGoalHandoff(ctx, "goal-handoff-1", goalID, "goal-requester", "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, "goal-dead-receiver"); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	second, err := s.RequestGoalHandoff(ctx, "goal-handoff-2", goalID, "goal-requester", "")
	if err != nil {
		t.Fatalf("second RequestGoalHandoff failed: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("same goal handoffs must have distinct IDs: %q", first.ID)
	}
	first, err = s.GetGoalHandoff(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff for reclaimed handoff failed: %v", err)
	}
	if first.CompletedReportAt == nil || first.CompleteReport != "セッションが停止した" {
		t.Fatalf("dead receiver handoff was not reclaimed: %+v", first)
	}

	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs failed: %v", err)
	}
	if len(handoffs) != 2 {
		t.Fatalf("got %d handoffs, want 2: %+v", len(handoffs), handoffs)
	}
}

func TestGoalHandoffRejectsSecondHandoffForLiveReceiver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-live-receiver-requester")
	addLiveGoalClaim(t, s, goalID, "goal-live-receiver-claim-owner")
	if err := s.RegisterAgentSession(ctx, "goal-live-receiver", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}

	first, err := s.RequestGoalHandoff(ctx, "goal-live-receiver-handoff-1", goalID, "goal-live-receiver-requester", "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, "goal-live-receiver"); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}

	if _, err := s.RequestGoalHandoff(ctx, "goal-live-receiver-handoff-2", goalID, "goal-live-receiver-requester", ""); err == nil {
		t.Fatal("RequestGoalHandoff should reject takeover from a live receiver")
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

func TestGoalHandoffCompleteByGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "complete-by-goal-requester")
	addTestAgentSession(t, s, "complete-by-goal-receiver")

	addRequestOnlyGoalHandoff(t, s, "complete-by-goal", goalID, "complete-by-goal-requester")
	requested, err := s.GetGoalHandoff(ctx, "complete-by-goal")
	if err != nil {
		t.Fatalf("GetGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, requested.ID, goalID, "complete-by-goal-receiver"); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}

	completed, err := s.CompleteGoalHandoffForGoal(ctx, goalID, "completed by goal ID")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffForGoal failed: %v", err)
	}
	if completed.ID != requested.ID || completed.GoalID != goalID || completed.CompletedReportAt == nil {
		t.Fatalf("unexpected completed handoff: %+v", completed)
	}
	if completed.CompleteReport != "completed by goal ID" {
		t.Fatalf("complete report = %q, want goal-ID completion report", completed.CompleteReport)
	}
}

func TestGoalHandoffCompleteByGoalRejectsUnreceived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	handoffID := "complete-unreceived-goal"
	addTestAgentSession(t, s, "complete-unreceived-goal-requester")
	addRequestOnlyGoalHandoff(t, s, handoffID, goalID, "complete-unreceived-goal-requester")

	_, err := s.CompleteGoalHandoffForGoal(ctx, goalID, "should not complete")
	if !errors.Is(err, ErrGoalHandoffNotFound) {
		t.Fatalf("error = %v, want ErrGoalHandoffNotFound", err)
	}

	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetGoalHandoff failed: %v", err)
	}
	if handoff.CompletedReportAt != nil {
		t.Fatalf("unreceived handoff has completion time: %+v", handoff)
	}
	if handoff.CompleteReport != "" {
		t.Fatalf("unreceived handoff complete report = %q, want empty", handoff.CompleteReport)
	}
}

func TestGoalHandoffCompleteByGoalRejectsMultipleReceivedIncomplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "complete-goal-ambiguous-requester")
	addTestAgentSession(t, s, "complete-goal-ambiguous-receiver")

	addRequestOnlyGoalHandoff(t, s, "complete-goal-ambiguous-1", goalID, "complete-goal-ambiguous-requester")
	if _, err := s.ReceiveGoalHandoff(ctx, "complete-goal-ambiguous-1", goalID, "complete-goal-ambiguous-receiver"); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "complete-goal-ambiguous-2", goalID, "complete-goal-ambiguous-requester", "complete-goal-ambiguous-receiver")

	_, err := s.CompleteGoalHandoffForGoal(ctx, goalID, "")
	if !errors.Is(err, ErrGoalHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrGoalHandoffAmbiguous", err)
	}
}

func TestGoalHandoffReceiveByGoalRejectsMultipleUnreceived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")
	addLiveGoalClaim(t, s, goalID, "goal-ambiguous-claim-owner")

	if _, err := s.RequestGoalHandoff(ctx, "goal-ambiguous-1", goalID, "goal-requester", ""); err != nil {
		t.Fatalf("RequestGoalHandoff failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "goal-ambiguous-2", goalID, "goal-requester", "")

	_, err := s.ReceiveGoalHandoffForGoal(ctx, goalID, "goal-receiver")
	if !errors.Is(err, ErrGoalHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrGoalHandoffAmbiguous", err)
	}
}

func TestGoalHandoffAllowsNewHandoffAfterCompletion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "completed-goal-requester")
	addTestAgentSession(t, s, "completed-goal-receiver")
	addLiveGoalClaim(t, s, goalID, "completed-goal-claim-owner")

	first, err := s.RequestGoalHandoff(ctx, "completed-goal-1", goalID, "completed-goal-requester", "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, "completed-goal-receiver"); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	if _, err := s.CompleteGoalHandoff(ctx, first.ID, goalID, "done"); err != nil {
		t.Fatalf("CompleteGoalHandoff failed: %v", err)
	}

	if _, err := s.RequestGoalHandoff(ctx, "completed-goal-2", goalID, "completed-goal-requester", ""); err != nil {
		t.Fatalf("new handoff after completion failed: %v", err)
	}
}

func TestGoalHandoffRejectsMultipleOpenHandoffsInDatabase(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-database-requester")
	addLiveGoalClaim(t, s, goalID, "goal-database-claim-owner")
	if _, err := s.RequestGoalHandoff(ctx, "goal-database-handoff-1", goalID, "goal-database-requester", ""); err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}

	err := sqlcgen.New(s.DB()).RequestGoalHandoff(ctx, sqlcgen.RequestGoalHandoffParams{
		ID:            "goal-database-handoff-2",
		GoalID:        goalID,
		RequestedBy:   sql.NullString{String: "goal-database-requester", Valid: true},
		RequestedAt:   sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true},
		RequestReport: sql.NullString{},
	})
	if err == nil {
		t.Fatal("database should reject a second open handoff for the same goal")
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
