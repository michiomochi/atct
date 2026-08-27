package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

func addLiveGoalClaim(t *testing.T, s *Store, goalID int64, sessionID string) {
	t.Helper()

	ctx := context.Background()
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal failed: %v", err)
	}
	agentSessionID := registerNamedTestAgentSession(t, s, sessionID, os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, agentSessionID, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject failed: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goalID, agentSessionID); err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}
}

func addLiveProjectClaim(t *testing.T, s *Store, goalID int64, sessionID string) {
	t.Helper()

	ctx := context.Background()
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal failed: %v", err)
	}
	agentSessionID := registerNamedTestAgentSession(t, s, sessionID, os.Getpid())
	if _, err := s.ClaimProject(ctx, goal.ProjectID, agentSessionID); err != nil {
		t.Fatalf("ClaimProject failed: %v", err)
	}
}

func addRequestOnlyGoalHandoff(t *testing.T, s *Store, handoffID string, goalID int64, requestedBy string) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := sqlcgen.New(s.DB()).RequestGoalHandoff(context.Background(), sqlcgen.RequestGoalHandoffParams{
		ID:            handoffID,
		GoalID:        goalID,
		RequestedBy:   sql.NullInt64{Int64: testSessionID(requestedBy), Valid: true},
		RequestedAt:   sql.NullString{String: now, Valid: true},
		RequestReport: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("insert request-only goal handoff failed: %v", err)
	}
}

func addReceiptOnlyGoalHandoff(t *testing.T, s *Store, handoffID string, goalID int64, receivedBy string) {
	t.Helper()

	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO goal_handoffs (id, goal_id, requested_by, received_by, received_at)
		VALUES (?, ?, ?, ?, ?)
	`, handoffID, goalID, testSessionID(receivedBy), testSessionID(receivedBy), now); err != nil {
		t.Fatalf("insert receipt-only goal handoff failed: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM goal_handoffs WHERE id = ?`, handoffID); err != nil {
			t.Errorf("delete receipt-only goal handoff %q: %v", handoffID, err)
		}
	})
}

func addGoalHandoffDirect(t *testing.T, s *Store, handoffID string, goalID int64, requestedBy, receivedBy any) {
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
		`, handoffID, goalID, testSessionRef(requestedBy), now)
	} else {
		_, err = s.DB().ExecContext(ctx, `
			INSERT INTO goal_handoffs (
				id, goal_id, requested_by, requested_at, request_report,
				received_by, received_at, completed_report_at, complete_report
			) VALUES (?, ?, ?, ?, NULL, ?, ?, NULL, NULL)
		`, handoffID, goalID, testSessionRef(requestedBy), now, testSessionRef(receivedBy), now)
	}
	if err != nil {
		t.Fatalf("insert direct goal handoff %q failed: %v", handoffID, err)
	}
}

func TestGoalHandoffRequestReceiveAndComplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-handoff-1", goalID, testSessionID("goal-requester"), "")
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

	received, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("goal-receiver"))
	if err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	if received.ID != handoff.ID || received.ReceivedBy != testSessionID("goal-receiver") || received.ReceivedAt == nil {
		t.Fatalf("unexpected received handoff: %+v", received)
	}
	if received.RequestedAt == nil || received.CompletedReportAt != nil {
		t.Fatalf("receive must preserve request and incomplete state: %+v", received)
	}

	completed, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "completed after verifying goal handoff state")
	if err != nil {
		t.Fatalf("CompleteGoalHandoff failed: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.RequestedAt == nil || completed.ReceivedAt == nil {
		t.Fatalf("completion must preserve all prior timestamps: %+v", completed)
	}
}

func TestListGoalSessionsIncludesSubcommander(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-sessions-subcommander")

	canonicalID, _, err := s.IdentifyAgentSession(ctx, testSessionID("goal-sessions-subcommander"), "atct-goal-sessions-subcommander")
	if err != nil {
		t.Fatalf("IdentifyAgentSession failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "goal-sessions-goal-handoff", goalID, canonicalID, canonicalID)

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(sessions), sessions)
	}
	if got := sessions[0]; got.SessionKey != "atct-goal-sessions-subcommander" || got.Role != "subcommander" || !got.HandoffOpen {
		t.Fatalf("unexpected subcommander session: %+v", got)
	}
}

func TestListGoalSessionsDeduplicatesExecutorTaskHandoffs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskIDs := addTestTasks(t, s, 2)
	var goalID int64
	if err := s.DB().QueryRowContext(ctx, "SELECT goal_id FROM tasks WHERE id = ?", taskIDs[0]).Scan(&goalID); err != nil {
		t.Fatalf("find task goal: %v", err)
	}
	addTestAgentSession(t, s, "goal-sessions-executor")

	canonicalID, _, err := s.IdentifyAgentSession(ctx, testSessionID("goal-sessions-executor"), "atct-goal-sessions-executor")
	if err != nil {
		t.Fatalf("IdentifyAgentSession failed: %v", err)
	}
	addTaskHandoffDirect(t, s, "goal-sessions-task-handoff-1", taskIDs[0], canonicalID, canonicalID)
	const secondHandoffID = "goal-sessions-task-handoff-2"
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_handoffs (
			id, task_id, requested_by, requested_at, request_report,
			received_by, received_at, completed_report_at, complete_report
		) VALUES (?, ?, ?, ?, NULL, ?, ?, NULL, NULL)
	`, secondHandoffID, taskIDs[1], canonicalID, "now", canonicalID, "now"); err != nil {
		t.Fatalf("insert second task handoff: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM task_handoffs WHERE id = ?`, secondHandoffID); err != nil {
			t.Errorf("delete second task handoff %q: %v", secondHandoffID, err)
		}
	})

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want one deduplicated executor: %+v", len(sessions), sessions)
	}
	if got := sessions[0]; got.SessionKey != "atct-goal-sessions-executor" || got.Role != "executor" || !got.HandoffOpen {
		t.Fatalf("unexpected executor session: %+v", got)
	}
}

func TestListGoalSessionsExcludesEmptySessionKeys(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-sessions-empty")
	addGoalHandoffDirect(t, s, "goal-sessions-empty-key-handoff", goalID, "goal-sessions-empty", "goal-sessions-empty")

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("got sessions for an empty session key: %+v", sessions)
	}
}

func TestListGoalSessionsIncludesCompletedHandoffs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-sessions-completed")

	canonicalID, _, err := s.IdentifyAgentSession(ctx, testSessionID("goal-sessions-completed"), "atct-goal-sessions-completed")
	if err != nil {
		t.Fatalf("IdentifyAgentSession failed: %v", err)
	}
	const handoffID = "goal-sessions-completed-handoff"
	addGoalHandoffDirect(t, s, handoffID, goalID, canonicalID, canonicalID)
	if _, err := s.DB().ExecContext(ctx, "UPDATE goal_handoffs SET completed_report_at = ? WHERE id = ?", "completed", handoffID); err != nil {
		t.Fatalf("complete direct goal handoff: %v", err)
	}

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].HandoffOpen {
		t.Fatalf("completed handoff was not returned as closed: %+v", sessions)
	}
}

func TestListGoalSessionsPrefersSubcommanderRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskIDs := addTestTasks(t, s, 1)
	var goalID int64
	if err := s.DB().QueryRowContext(ctx, "SELECT goal_id FROM tasks WHERE id = ?", taskIDs[0]).Scan(&goalID); err != nil {
		t.Fatalf("find task goal: %v", err)
	}
	addTestAgentSession(t, s, "goal-sessions-dual")

	canonicalID, _, err := s.IdentifyAgentSession(ctx, testSessionID("goal-sessions-dual"), "atct-goal-sessions-dual")
	if err != nil {
		t.Fatalf("IdentifyAgentSession failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "goal-sessions-dual-goal-handoff", goalID, canonicalID, canonicalID)
	addTaskHandoffDirect(t, s, "goal-sessions-dual-task-handoff", taskIDs[0], canonicalID, canonicalID)

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Role != "subcommander" {
		t.Fatalf("subcommander role was not preferred: %+v", sessions)
	}
}

func TestGoalHandoffReportsAreStored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-report-requester")
	addTestAgentSession(t, s, "goal-report-receiver")

	requested, err := s.RequestGoalHandoff(ctx, "goal-report-handoff", goalID, testSessionID("goal-report-requester"), "Please take over the goal.")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if requested.RequestReport != "Please take over the goal." {
		t.Fatalf("request report = %q, want request body", requested.RequestReport)
	}

	if _, err := s.ReceiveGoalHandoff(ctx, requested.ID, goalID, testSessionID("goal-report-receiver")); err != nil {
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

func TestCompleteGoalHandoffPublishesReportedEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "publish-goal-requester")
	addTestAgentSession(t, s, "publish-goal-receiver")
	handoffID := "publish-goal-handoff"
	addRequestOnlyGoalHandoff(t, s, handoffID, goalID, "publish-goal-requester")
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, testSessionID("publish-goal-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	events, cancel := s.SubscribeEvents()
	defer cancel()

	const report = "goal completion report"
	completed, err := s.CompleteGoalHandoff(ctx, handoffID, goalID, report)
	if err != nil {
		t.Fatalf("CompleteGoalHandoff: %v", err)
	}
	detection := waitForHandoffReported(t, events)

	if completed.ID != handoffID || completed.CompletedReportAt == nil || completed.CompleteReport != report {
		t.Fatalf("completed handoff = %+v, want report %q", completed, report)
	}
	if detection.DetectionID == "" || detection.ProjectID != goal.ProjectID || detection.GoalID != goalID || detection.TaskID != 0 || detection.HandoffID != handoffID || detection.CompleteReport != report {
		t.Fatalf("reported detection = %+v, want project=%d goal=%d task=0 handoff=%q report=%q", detection, goal.ProjectID, goalID, handoffID, report)
	}
}

func TestWithdrawActiveGoalDoesNotPublishReportedTaskHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "withdraw-test", "withdraw-goal", []string{"Open task"}, []string{"Task remains open during withdrawal."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	addTestAgentSession(t, s, "withdraw-requester")
	addTestAgentSession(t, s, "withdraw-receiver")
	const handoffID = "withdraw-task-handoff"
	addTaskHandoffDirect(t, s, handoffID, tasks[0].ID, "withdraw-requester", "withdraw-receiver")

	events, cancel := s.SubscribeEvents()
	defer cancel()
	if err := s.WithdrawActiveGoal(ctx, goalID, "withdraw the goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	for {
		select {
		case event := <-events:
			if event.Name == EventHandoffReported {
				t.Fatalf("withdrawal published handoff_reported event: %#v", event)
			}
		default:
			return
		}
	}
}

func TestGoalHandoffCompletionRejectsEmptyReport(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-empty-report-requester")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-empty-report-handoff", goalID, testSessionID("goal-empty-report-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff without report: %v", err)
	}
	if handoff.RequestReport != "" {
		t.Fatalf("omitted request report = %q, want empty", handoff.RequestReport)
	}

	_, err = s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "")
	if err == nil {
		t.Fatal("CompleteGoalHandoff unexpectedly accepted an empty complete report")
	}
	if !strings.Contains(err.Error(), "complete_report") {
		t.Fatalf("empty report error = %q, want complete_report", err)
	}
}

func TestGoalHandoffCompletionRejectsWhitespaceOnlyReport(t *testing.T) {
	for _, report := range []string{" ", "　", "\n", "\t"} {
		t.Run(fmt.Sprintf("%q", report), func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			goalID := newTestGoal(t, s)
			addLiveProjectClaim(t, s, goalID, "goal-whitespace-report-requester")
			handoff, err := s.RequestGoalHandoff(ctx, "goal-whitespace-report-handoff", goalID, testSessionID("goal-whitespace-report-requester"), "")
			if err != nil {
				t.Fatalf("RequestGoalHandoff: %v", err)
			}
			if _, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, report); err == nil {
				t.Fatalf("CompleteGoalHandoff unexpectedly accepted whitespace-only report %q", report)
			}
		})
	}
}

func TestGoalHandoffAllowsSecondHandoffForSameGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-requester")
	addTestAgentSession(t, s, "goal-dead-receiver")
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?
	`, 999999, "dead", testSessionID("goal-dead-receiver")); err != nil {
		t.Fatalf("dead receiver session fixture update failed: %v", err)
	}

	first, err := s.RequestGoalHandoff(ctx, "goal-handoff-1", goalID, testSessionID("goal-requester"), "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, testSessionID("goal-dead-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	second, err := s.RequestGoalHandoff(ctx, "goal-handoff-2", goalID, testSessionID("goal-requester"), "")
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
	addLiveProjectClaim(t, s, goalID, "goal-live-receiver-requester")
	goalLiveReceiverID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}

	first, err := s.RequestGoalHandoff(ctx, "goal-live-receiver-handoff-1", goalID, testSessionID("goal-live-receiver-requester"), "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, goalLiveReceiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}

	if _, err := s.RequestGoalHandoff(ctx, "goal-live-receiver-handoff-2", goalID, testSessionID("goal-live-receiver-requester"), ""); err == nil {
		t.Fatal("RequestGoalHandoff should reject takeover from a live receiver")
	}
}

func TestGoalHandoffReceiveByGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")

	requested, err := s.RequestGoalHandoff(ctx, "goal-receive-by-goal", goalID, testSessionID("goal-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff failed: %v", err)
	}

	received, err := s.ReceiveGoalHandoffForGoal(ctx, goalID, testSessionID("goal-receiver"))
	if err != nil {
		t.Fatalf("ReceiveGoalHandoffForGoal failed: %v", err)
	}
	if received.ID != requested.ID || received.ReceivedBy != testSessionID("goal-receiver") {
		t.Fatalf("unexpected received handoff: %+v", received)
	}
}

func TestGoalHandoffReceiveRejectsUnrequestedHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	handoffID := "goal-receipt-only"
	addTestAgentSession(t, s, "receiver")
	addReceiptOnlyGoalHandoff(t, s, handoffID, goalID, "receiver")

	_, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, testSessionID("receiver"))
	if !errors.Is(err, ErrGoalHandoffNotFound) {
		t.Fatalf("error = %v, want ErrGoalHandoffNotFound", err)
	}
}

func TestGoalHandoffReceiveRejectsUnknownUUIDNotFound(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	unknownHandoffID := "9dfc8983-f430-4c7f-92a0-3882941dd393"

	_, err := s.ReceiveGoalHandoff(context.Background(), unknownHandoffID, goalID, testSessionID("receiver"))
	if !errors.Is(err, ErrGoalHandoffNotFound) {
		t.Fatalf("error = %v, want ErrGoalHandoffNotFound", err)
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
	if _, err := s.ReceiveGoalHandoff(ctx, requested.ID, goalID, testSessionID("complete-by-goal-receiver")); err != nil {
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

func TestGoalHandoffAmendReportUpdatesCompletedHandoffWithoutChangingCompletionTime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "amend-goal-requester")
	addTestAgentSession(t, s, "amend-goal-receiver")
	handoff, err := s.RequestGoalHandoff(ctx, "amend-goal-handoff", goalID, testSessionID("amend-goal-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("amend-goal-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	completed, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "original report")
	if err != nil {
		t.Fatalf("CompleteGoalHandoff: %v", err)
	}

	amended, err := s.AmendGoalHandoffReport(ctx, handoff.ID, goalID, "amended report")
	if err != nil {
		t.Fatalf("AmendGoalHandoffReport: %v", err)
	}
	if amended.CompleteReport != "amended report" {
		t.Fatalf("complete report = %q, want amended report", amended.CompleteReport)
	}
	if amended.CompletedReportAt == nil || !amended.CompletedReportAt.Equal(*completed.CompletedReportAt) {
		t.Fatalf("completed report timestamp = %v, want %v", amended.CompletedReportAt, completed.CompletedReportAt)
	}
}

func TestGoalHandoffAmendReportRejectsIncompleteHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "amend-incomplete-goal-requester")
	addTestAgentSession(t, s, "amend-incomplete-goal-receiver")
	handoff, err := s.RequestGoalHandoff(ctx, "amend-incomplete-goal-handoff", goalID, testSessionID("amend-incomplete-goal-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("amend-incomplete-goal-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	_, err = s.AmendGoalHandoffReport(ctx, handoff.ID, goalID, "replacement report")
	if err == nil || !strings.Contains(err.Error(), "handoff_complete") {
		t.Fatalf("error = %v, want incomplete handoff to name handoff_complete", err)
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
	if _, err := s.ReceiveGoalHandoff(ctx, "complete-goal-ambiguous-1", goalID, testSessionID("complete-goal-ambiguous-receiver")); err != nil {
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
	addLiveProjectClaim(t, s, goalID, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")

	if _, err := s.RequestGoalHandoff(ctx, "goal-ambiguous-1", goalID, testSessionID("goal-requester"), ""); err != nil {
		t.Fatalf("RequestGoalHandoff failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "goal-ambiguous-2", goalID, "goal-requester", "")

	_, err := s.ReceiveGoalHandoffForGoal(ctx, goalID, testSessionID("goal-receiver"))
	if !errors.Is(err, ErrGoalHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrGoalHandoffAmbiguous", err)
	}
}

func TestGoalHandoffAllowsNewHandoffAfterCompletion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "completed-goal-requester")
	addTestAgentSession(t, s, "completed-goal-receiver")

	first, err := s.RequestGoalHandoff(ctx, "completed-goal-1", goalID, testSessionID("completed-goal-requester"), "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, testSessionID("completed-goal-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	if _, err := s.CompleteGoalHandoff(ctx, first.ID, goalID, "done"); err != nil {
		t.Fatalf("CompleteGoalHandoff failed: %v", err)
	}

	if _, err := s.RequestGoalHandoff(ctx, "completed-goal-2", goalID, testSessionID("completed-goal-requester"), ""); err != nil {
		t.Fatalf("new handoff after completion failed: %v", err)
	}
}

func TestGoalHandoffCompletionDoesNotOverwriteReportedHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "overwrite-goal-requester")
	addTestAgentSession(t, s, "overwrite-goal-receiver")
	handoff, err := s.RequestGoalHandoff(ctx, "overwrite-goal-handoff", goalID, testSessionID("overwrite-goal-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("overwrite-goal-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "original report"); err != nil {
		t.Fatalf("first CompleteGoalHandoff: %v", err)
	}
	if _, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "replacement report"); err == nil {
		t.Fatal("second CompleteGoalHandoff unexpectedly overwrote the completed handoff")
	}
	stored, err := s.GetGoalHandoff(ctx, handoff.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff: %v", err)
	}
	if stored.CompleteReport != "original report" {
		t.Fatalf("complete report = %q, want original report", stored.CompleteReport)
	}
}

func TestGoalHandoffRejectsMultipleOpenHandoffsInDatabase(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-database-requester")
	if _, err := s.RequestGoalHandoff(ctx, "goal-database-handoff-1", goalID, testSessionID("goal-database-requester"), ""); err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}

	err := sqlcgen.New(s.DB()).RequestGoalHandoff(ctx, sqlcgen.RequestGoalHandoffParams{
		ID:            "goal-database-handoff-2",
		GoalID:        goalID,
		RequestedBy:   sql.NullInt64{Int64: testSessionID("goal-database-requester"), Valid: true},
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

	if _, err := s.RequestGoalHandoff(context.Background(), "goal-handoff-missing", 0, testSessionID("goal-requester"), ""); err == nil {
		t.Fatal("RequestGoalHandoff should reject a missing goal")
	}
}

func TestGoalHandoffRequiresRequesterProjectClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal failed: %v", err)
	}
	requesterID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}
	if _, err := s.ClaimProject(ctx, goal.ProjectID, requesterID); err != nil {
		t.Fatalf("ClaimProject failed: %v", err)
	}

	if _, err := s.RequestGoalHandoff(ctx, "goal-handoff-project-owner", goalID, requesterID, ""); err != nil {
		t.Fatalf("RequestGoalHandoff with requester project claim failed: %v", err)
	}
}

func TestGoalHandoffRejectsUnclaimedGoal(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-requester")

	_, err := s.RequestGoalHandoff(context.Background(), "goal-handoff-unclaimed", goalID, testSessionID("goal-requester"), "")
	if !errors.Is(err, ErrGoalHandoffProjectNotHeld) {
		t.Fatalf("error = %v, want ErrGoalHandoffProjectNotHeld", err)
	}
}
