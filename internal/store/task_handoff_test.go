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

func addTestAgentSession(t *testing.T, s *Store, id string) {
	t.Helper()

	ctx := context.Background()
	if err := s.RegisterAgentSession(ctx, id, 0); err != nil {
		t.Fatalf("RegisterAgentSession %q: %v", id, err)
	}
}

func addTestTasks(t *testing.T, s *Store, count int) []string {
	t.Helper()

	goalID := newTestGoal(t, s)
	titles := make([]string, count)
	descriptions := make([]string, count)
	for i := range titles {
		titles[i] = "handoff task " + string(rune('a'+i))
		descriptions[i] = "handoff task fixture"
	}

	tasks, err := s.DeclareTasks(context.Background(), goalID, "handoff-test", "handoff-fixture", titles, descriptions)
	if err != nil {
		t.Fatalf("DeclareTasks failed: %v", err)
	}
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
	}
	return ids
}

func addLiveTaskClaim(t *testing.T, s *Store, taskID, sessionID string) {
	t.Helper()

	ctx := context.Background()
	projectID, err := sqlcgen.New(s.DB()).GetTaskProjectID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskProjectID failed: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, sessionID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, sessionID, projectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject failed: %v", err)
	}
	if _, err := s.ClaimTask(ctx, taskID, sessionID); err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}
}

func addLiveParentGoalClaim(t *testing.T, s *Store, taskID, sessionID string) {
	t.Helper()

	goalID, err := sqlcgen.New(s.DB()).GetTaskGoalID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTaskGoalID failed: %v", err)
	}
	addLiveGoalClaim(t, s, goalID, sessionID)
}

func addRequestOnlyTaskHandoff(t *testing.T, s *Store, handoffID, taskID, requestedBy string) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := sqlcgen.New(s.DB()).RequestTaskHandoff(context.Background(), sqlcgen.RequestTaskHandoffParams{
		ID:            handoffID,
		TaskID:        taskID,
		RequestedBy:   sql.NullString{String: requestedBy, Valid: true},
		RequestedAt:   sql.NullString{String: now, Valid: true},
		RequestReport: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("insert request-only task handoff failed: %v", err)
	}
}

func addReceiptOnlyTaskHandoff(t *testing.T, s *Store, handoffID, taskID, receivedBy string) {
	t.Helper()

	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_handoffs (id, task_id, requested_by, received_by, received_at)
		VALUES (?, ?, ?, ?, ?)
	`, handoffID, taskID, receivedBy, receivedBy, now); err != nil {
		t.Fatalf("insert receipt-only task handoff failed: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM task_handoffs WHERE id = ?`, handoffID); err != nil {
			t.Errorf("delete receipt-only task handoff %q: %v", handoffID, err)
		}
	})
}

func addTaskHandoffDirect(t *testing.T, s *Store, handoffID, taskID, requestedBy, receivedBy string) {
	t.Helper()

	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `DROP INDEX IF EXISTS idx_task_handoffs_open_task_id`); err != nil {
		t.Fatalf("drop task handoff uniqueness index: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM task_handoffs WHERE id = ?`, handoffID); err != nil {
			t.Errorf("delete direct task handoff %q: %v", handoffID, err)
		}
		if _, err := s.DB().ExecContext(ctx, `
			CREATE UNIQUE INDEX idx_task_handoffs_open_task_id
			ON task_handoffs(task_id)
			WHERE completed_report_at IS NULL
		`); err != nil {
			t.Errorf("restore task handoff uniqueness index: %v", err)
		}
	})

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var err error
	if receivedBy == "" {
		_, err = s.DB().ExecContext(ctx, `
			INSERT INTO task_handoffs (
				id, task_id, requested_by, requested_at, request_report,
				received_by, received_at, completed_report_at, complete_report
			) VALUES (?, ?, ?, ?, NULL, NULL, NULL, NULL, NULL)
		`, handoffID, taskID, requestedBy, now)
	} else {
		_, err = s.DB().ExecContext(ctx, `
			INSERT INTO task_handoffs (
				id, task_id, requested_by, requested_at, request_report,
				received_by, received_at, completed_report_at, complete_report
			) VALUES (?, ?, ?, ?, NULL, ?, ?, NULL, NULL)
		`, handoffID, taskID, requestedBy, now, receivedBy, now)
	}
	if err != nil {
		t.Fatalf("insert direct task handoff %q failed: %v", handoffID, err)
	}
}

func TestTaskHandoffRequestReceiveAndComplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "requester")
	addTestAgentSession(t, s, "receiver")

	handoff, err := s.RequestTaskHandoff(ctx, "handoff-1", taskID, "requester", "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff failed: %v", err)
	}
	if handoff.ID == "" || handoff.TaskID != taskID {
		t.Fatalf("unexpected requested handoff: %+v", handoff)
	}
	if handoff.RequestedAt == nil {
		t.Fatal("requested timestamp is nil")
	}
	if handoff.ReceivedAt != nil || handoff.CompletedReportAt != nil {
		t.Fatalf("new handoff should be unreceived and incomplete: %+v", handoff)
	}

	unreceived, err := s.GetTaskHandoff(ctx, handoff.ID)
	if err != nil {
		t.Fatalf("GetTaskHandoff before receive failed: %v", err)
	}
	if unreceived.ReceivedAt != nil {
		t.Fatalf("unreceived handoff has received timestamp: %+v", unreceived)
	}

	received, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, "receiver")
	if err != nil {
		t.Fatalf("ReceiveTaskHandoff failed: %v", err)
	}
	if received.ID != handoff.ID || received.ReceivedBy != "receiver" || received.ReceivedAt == nil {
		t.Fatalf("unexpected received handoff: %+v", received)
	}
	if received.RequestedAt == nil || received.CompletedReportAt != nil {
		t.Fatalf("receive must preserve request and incomplete state: %+v", received)
	}

	completed, err := s.CompleteTaskHandoff(ctx, handoff.ID, taskID, "")
	if err != nil {
		t.Fatalf("CompleteTaskHandoff failed: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.RequestedAt == nil || completed.ReceivedAt == nil {
		t.Fatalf("completion must preserve all prior timestamps: %+v", completed)
	}
}

func TestTaskHandoffReportsAreStored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "task-report-requester")
	addTestAgentSession(t, s, "task-report-receiver")

	requested, err := s.RequestTaskHandoff(ctx, "task-report-handoff", taskID, "task-report-requester", "Please take over the task.")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if requested.RequestReport != "Please take over the task." {
		t.Fatalf("request report = %q, want request body", requested.RequestReport)
	}

	if _, err := s.ReceiveTaskHandoff(ctx, requested.ID, taskID, "task-report-receiver"); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	completed, err := s.CompleteTaskHandoff(ctx, requested.ID, taskID, "I completed the task and verified it.")
	if err != nil {
		t.Fatalf("CompleteTaskHandoff: %v", err)
	}
	if completed.CompleteReport != "I completed the task and verified it." {
		t.Fatalf("complete report = %q, want completion body", completed.CompleteReport)
	}
}

func TestTaskHandoffReportsMayBeOmitted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "task-empty-report-requester")

	handoff, err := s.RequestTaskHandoff(ctx, "task-empty-report-handoff", taskID, "task-empty-report-requester", "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff without report: %v", err)
	}
	if handoff.RequestReport != "" {
		t.Fatalf("omitted request report = %q, want empty", handoff.RequestReport)
	}

	completed, err := s.CompleteTaskHandoff(ctx, handoff.ID, taskID, "")
	if err != nil {
		t.Fatalf("CompleteTaskHandoff without report: %v", err)
	}
	if completed.CompleteReport != "" {
		t.Fatalf("omitted complete report = %q, want empty", completed.CompleteReport)
	}
}

func TestTaskHandoffAllowsSecondHandoffForSameTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "requester")
	addTestAgentSession(t, s, "dead-receiver")
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?
	`, 999999, "dead", "dead-receiver"); err != nil {
		t.Fatalf("dead receiver session fixture update failed: %v", err)
	}

	first, err := s.RequestTaskHandoff(ctx, "handoff-1", taskID, "requester", "")
	if err != nil {
		t.Fatalf("first RequestTaskHandoff failed: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, first.ID, taskID, "dead-receiver"); err != nil {
		t.Fatalf("ReceiveTaskHandoff failed: %v", err)
	}
	second, err := s.RequestTaskHandoff(ctx, "handoff-2", taskID, "requester", "")
	if err != nil {
		t.Fatalf("second RequestTaskHandoff failed: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("same task handoffs must have distinct IDs: %q", first.ID)
	}
	first, err = s.GetTaskHandoff(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetTaskHandoff for reclaimed handoff failed: %v", err)
	}
	if first.CompletedReportAt == nil || first.CompleteReport != "セッションが停止した" {
		t.Fatalf("dead receiver handoff was not reclaimed: %+v", first)
	}

	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		t.Fatalf("ListTaskHandoffs failed: %v", err)
	}
	if len(handoffs) != 2 {
		t.Fatalf("got %d handoffs, want 2: %+v", len(handoffs), handoffs)
	}
}

func TestTaskHandoffRejectsSecondHandoffForLiveReceiver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "live-receiver-requester")
	if err := s.RegisterAgentSession(ctx, "live-receiver", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}

	first, err := s.RequestTaskHandoff(ctx, "live-receiver-handoff-1", taskID, "live-receiver-requester", "")
	if err != nil {
		t.Fatalf("first RequestTaskHandoff failed: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, first.ID, taskID, "live-receiver"); err != nil {
		t.Fatalf("ReceiveTaskHandoff failed: %v", err)
	}

	if _, err := s.RequestTaskHandoff(ctx, "live-receiver-handoff-2", taskID, "live-receiver-requester", ""); err == nil {
		t.Fatal("RequestTaskHandoff should reject takeover from a live receiver")
	}
}

func TestTaskHandoffReceiveByTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "requester")
	addTestAgentSession(t, s, "receiver")

	requested, err := s.RequestTaskHandoff(ctx, "receive-by-task", taskID, "requester", "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff failed: %v", err)
	}

	received, err := s.ReceiveTaskHandoffForTask(ctx, taskID, "receiver")
	if err != nil {
		t.Fatalf("ReceiveTaskHandoffForTask failed: %v", err)
	}
	if received.ID != requested.ID || received.ReceivedBy != "receiver" {
		t.Fatalf("unexpected received handoff: %+v", received)
	}
}

func TestTaskHandoffCompleteByTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "complete-by-task-requester")
	addTestAgentSession(t, s, "complete-by-task-receiver")

	addRequestOnlyTaskHandoff(t, s, "complete-by-task", taskID, "complete-by-task-requester")
	requested, err := s.GetTaskHandoff(ctx, "complete-by-task")
	if err != nil {
		t.Fatalf("GetTaskHandoff failed: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, requested.ID, taskID, "complete-by-task-receiver"); err != nil {
		t.Fatalf("ReceiveTaskHandoff failed: %v", err)
	}

	completed, err := s.CompleteTaskHandoffForTask(ctx, taskID, "completed by task ID")
	if err != nil {
		t.Fatalf("CompleteTaskHandoffForTask failed: %v", err)
	}
	if completed.ID != requested.ID || completed.TaskID != taskID || completed.CompletedReportAt == nil {
		t.Fatalf("unexpected completed handoff: %+v", completed)
	}
	if completed.CompleteReport != "completed by task ID" {
		t.Fatalf("complete report = %q, want task-ID completion report", completed.CompleteReport)
	}
}

func TestTaskHandoffCompleteByTaskRejectsUnreceived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	handoffID := "complete-unreceived-task"
	addTestAgentSession(t, s, "complete-unreceived-task-requester")
	addRequestOnlyTaskHandoff(t, s, handoffID, taskID, "complete-unreceived-task-requester")

	_, err := s.CompleteTaskHandoffForTask(ctx, taskID, "should not complete")
	if !errors.Is(err, ErrTaskHandoffNotFound) {
		t.Fatalf("error = %v, want ErrTaskHandoffNotFound", err)
	}

	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetTaskHandoff failed: %v", err)
	}
	if handoff.CompletedReportAt != nil {
		t.Fatalf("unreceived handoff has completion time: %+v", handoff)
	}
	if handoff.CompleteReport != "" {
		t.Fatalf("unreceived handoff complete report = %q, want empty", handoff.CompleteReport)
	}
}

func TestTaskHandoffCompleteByTaskRejectsMultipleReceivedIncomplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "complete-task-ambiguous-requester")
	addTestAgentSession(t, s, "complete-task-ambiguous-receiver")

	addRequestOnlyTaskHandoff(t, s, "complete-task-ambiguous-1", taskID, "complete-task-ambiguous-requester")
	if _, err := s.ReceiveTaskHandoff(ctx, "complete-task-ambiguous-1", taskID, "complete-task-ambiguous-receiver"); err != nil {
		t.Fatalf("ReceiveTaskHandoff failed: %v", err)
	}
	addTaskHandoffDirect(t, s, "complete-task-ambiguous-2", taskID, "complete-task-ambiguous-requester", "complete-task-ambiguous-receiver")

	_, err := s.CompleteTaskHandoffForTask(ctx, taskID, "")
	if !errors.Is(err, ErrTaskHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrTaskHandoffAmbiguous", err)
	}
}

func TestTaskHandoffReceiveByTaskRejectsMultipleUnreceived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "requester")
	addTestAgentSession(t, s, "receiver")

	if _, err := s.RequestTaskHandoff(ctx, "ambiguous-handoff-1", taskID, "requester", ""); err != nil {
		t.Fatalf("RequestTaskHandoff failed: %v", err)
	}
	addTaskHandoffDirect(t, s, "ambiguous-handoff-2", taskID, "requester", "")

	_, err := s.ReceiveTaskHandoffForTask(ctx, taskID, "receiver")
	if !errors.Is(err, ErrTaskHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrTaskHandoffAmbiguous", err)
	}
}

func TestTaskHandoffAllowsNewHandoffAfterCompletion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "completed-task-requester")
	addTestAgentSession(t, s, "completed-task-receiver")

	first, err := s.RequestTaskHandoff(ctx, "completed-task-1", taskID, "completed-task-requester", "")
	if err != nil {
		t.Fatalf("first RequestTaskHandoff failed: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, first.ID, taskID, "completed-task-receiver"); err != nil {
		t.Fatalf("ReceiveTaskHandoff failed: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, first.ID, taskID, "done"); err != nil {
		t.Fatalf("CompleteTaskHandoff failed: %v", err)
	}

	if _, err := s.RequestTaskHandoff(ctx, "completed-task-2", taskID, "completed-task-requester", ""); err != nil {
		t.Fatalf("new handoff after completion failed: %v", err)
	}
}

func TestTaskHandoffRejectsMultipleOpenHandoffsInDatabase(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "database-requester")
	if _, err := s.RequestTaskHandoff(ctx, "database-handoff-1", taskID, "database-requester", ""); err != nil {
		t.Fatalf("first RequestTaskHandoff failed: %v", err)
	}

	err := sqlcgen.New(s.DB()).RequestTaskHandoff(ctx, sqlcgen.RequestTaskHandoffParams{
		ID:            "database-handoff-2",
		TaskID:        taskID,
		RequestedBy:   sql.NullString{String: "database-requester", Valid: true},
		RequestedAt:   sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true},
		RequestReport: sql.NullString{},
	})
	if err == nil {
		t.Fatal("database should reject a second open handoff for the same task")
	}
}

func TestTaskHandoffRejectsMissingTask(t *testing.T) {
	s := newTestStore(t)
	addTestAgentSession(t, s, "requester")

	if _, err := s.RequestTaskHandoff(context.Background(), "handoff-missing", "missing-task", "requester", ""); err == nil {
		t.Fatal("RequestTaskHandoff should reject a missing task")
	}
}

func TestTaskHandoffRequiresRequesterGoalHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	goalID, err := sqlcgen.New(s.DB()).GetTaskGoalID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskGoalID failed: %v", err)
	}
	requester := "task-goal-owner"
	if err := s.RegisterAgentSession(ctx, requester, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goalID, requester); err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}

	if _, err := s.RequestTaskHandoff(ctx, "task-handoff-goal-owner", taskID, requester, ""); err != nil {
		t.Fatalf("RequestTaskHandoff with requester goal handoff failed: %v", err)
	}
}

func TestTaskHandoffRejectsUnclaimedTask(t *testing.T) {
	s := newTestStore(t)
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "requester")

	if _, err := s.RequestTaskHandoff(context.Background(), "handoff-unclaimed", taskID, "requester", ""); err == nil {
		t.Fatal("RequestTaskHandoff should reject an unclaimed task")
	}
}

func TestTaskHandoffAllowsLiveClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "requester")

	if _, err := s.RequestTaskHandoff(ctx, "handoff-live-claim", taskID, "requester", ""); err != nil {
		t.Fatalf("RequestTaskHandoff with a live parent claim failed: %v", err)
	}
}

func TestTaskHandoffReclaimsDeadClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "requester")
	addTestAgentSession(t, s, "dead-claim-owner")

	if _, err := s.DB().ExecContext(ctx, `
		UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?
	`, 999999, "dead", "dead-claim-owner"); err != nil {
		t.Fatalf("dead claim session fixture update failed: %v", err)
	}
	addTaskHandoffDirect(t, s, "handoff-dead-claim-existing", taskID, "dead-claim-owner", "dead-claim-owner")

	handoff, err := s.RequestTaskHandoff(ctx, "handoff-dead-claim", taskID, "requester", "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff should reclaim a dead task claim: %v", err)
	}
	if handoff.ID != "handoff-dead-claim" || handoff.RequestedBy != "requester" {
		t.Fatalf("unexpected replacement handoff: %+v", handoff)
	}
	previous, err := s.GetTaskHandoff(ctx, "handoff-dead-claim-existing")
	if err != nil {
		t.Fatalf("GetTaskHandoff reclaimed claim: %v", err)
	}
	if previous.CompletedReportAt == nil || previous.CompleteReport != "セッションが停止した" {
		t.Fatalf("dead claim was not completed during reclaim: %+v", previous)
	}
}

func TestTaskHandoffReceiveRejectsUnrequestedHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	handoffID := "handoff-receipt-only"
	addTestAgentSession(t, s, "receiver")
	addReceiptOnlyTaskHandoff(t, s, handoffID, taskID, "receiver")

	_, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, "receiver")
	if !errors.Is(err, ErrTaskHandoffNotFound) {
		t.Fatalf("error = %v, want ErrTaskHandoffNotFound", err)
	}
}

func TestTaskHandoffReceiveRejectsEmptyHandoffID(t *testing.T) {
	s := newTestStore(t)
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "receiver")

	_, err := s.ReceiveTaskHandoff(context.Background(), "", taskID, "receiver")
	if !errors.Is(err, ErrTaskHandoffNotFound) {
		t.Fatalf("error = %v, want ErrTaskHandoffNotFound", err)
	}
}

func TestTaskHandoffCompleteRejectsUnrequestedHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	handoffID := "complete-receipt-only"
	addTestAgentSession(t, s, "receiver")
	addReceiptOnlyTaskHandoff(t, s, handoffID, taskID, "receiver")

	_, err := s.CompleteTaskHandoff(ctx, handoffID, taskID, "should not complete")
	if !errors.Is(err, ErrTaskHandoffNotFound) {
		t.Fatalf("error = %v, want ErrTaskHandoffNotFound", err)
	}
}

func TestTaskHandoffRejectsTaskMismatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskIDs := addTestTasks(t, s, 2)
	handoffID := "task-mismatch"
	addTestAgentSession(t, s, "requester")
	addRequestOnlyTaskHandoff(t, s, handoffID, taskIDs[0], "requester")

	_, err := s.ReceiveTaskHandoff(ctx, handoffID, taskIDs[1], "receiver")
	if !errors.Is(err, ErrTaskHandoffTaskMismatch) {
		t.Fatalf("error = %v, want ErrTaskHandoffTaskMismatch", err)
	}
	if errors.Is(err, ErrTaskHandoffNotFound) {
		t.Fatalf("task mismatch must not be ErrTaskHandoffNotFound: %v", err)
	}
}

func TestTaskHandoffUnclaimedErrorIsDistinguishable(t *testing.T) {
	s := newTestStore(t)
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "requester")

	_, err := s.RequestTaskHandoff(context.Background(), "handoff-unclaimed-error", taskID, "requester", "")
	if !errors.Is(err, ErrTaskHandoffGoalNotHeld) {
		t.Fatalf("error = %v, want ErrTaskHandoffGoalNotHeld", err)
	}
	if errors.Is(err, ErrTaskHandoffNotFound) {
		t.Fatalf("unclaimed error must not be ErrTaskHandoffNotFound: %v", err)
	}
}

func TestTaskHandoffStatesRemainDistinct(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskIDs := addTestTasks(t, s, 3)
	addLiveParentGoalClaim(t, s, taskIDs[0], "requester")
	addLiveTaskClaim(t, s, taskIDs[0], "receiver")

	// A handoff request without a task claim. This fixture bypasses the public
	// request method because the state is intentionally a detected anomaly.
	addRequestOnlyTaskHandoff(t, s, "request-only", taskIDs[1], "requester")

	// A handoff request without a receipt.
	requested, err := s.RequestTaskHandoff(ctx, "unreceived", taskIDs[2], "requester", "")
	if err != nil {
		t.Fatalf("unreceived fixture insert failed: %v", err)
	}

	if got, err := s.ListTaskHandoffs(ctx, taskIDs[0]); err != nil {
		t.Fatalf("ListTaskHandoffs for claim handoff failed: %v", err)
	} else if len(got) != 1 || got[0].RequestedAt == nil || got[0].ReceivedAt == nil || got[0].CompletedReportAt != nil || got[0].ReceivedBy != "receiver" {
		t.Fatalf("claim handoff state is not distinct: %+v", got)
	}
	if got, err := s.ListTaskHandoffs(ctx, taskIDs[1]); err != nil {
		t.Fatalf("ListTaskHandoffs for request-only task failed: %v", err)
	} else if len(got) != 1 || got[0].RequestedAt == nil || got[0].ReceivedAt != nil {
		t.Fatalf("request-only task state is not distinct: %+v", got)
	}
	if got, err := s.GetTaskHandoff(ctx, requested.ID); err != nil {
		t.Fatalf("GetTaskHandoff for unreceived task failed: %v", err)
	} else if got.RequestedAt == nil || got.ReceivedAt != nil || got.CompletedReportAt != nil {
		t.Fatalf("unreceived state is not distinct: %+v", got)
	}
}
