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
	registeredAt := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB().ExecContext(ctx, `
		INSERT INTO agent_sessions (id, project_id, pid, started_at, registered_at)
		VALUES (?, NULL, 0, '', ?)
	`, id, registeredAt)
	if err != nil {
		t.Fatalf("insert agent session %q: %v", id, err)
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

func TestTaskHandoffRequestReceiveAndComplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "requester")
	addTestAgentSession(t, s, "receiver")
	addLiveTaskClaim(t, s, taskID, "request-claim-owner")

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
	addTestAgentSession(t, s, "task-report-requester")
	addTestAgentSession(t, s, "task-report-receiver")
	addLiveTaskClaim(t, s, taskID, "task-report-claim-owner")

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
	addTestAgentSession(t, s, "task-empty-report-requester")
	addLiveTaskClaim(t, s, taskID, "task-empty-report-claim-owner")

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
	addTestAgentSession(t, s, "requester")
	addLiveTaskClaim(t, s, taskID, "second-handoff-claim-owner")

	first, err := s.RequestTaskHandoff(ctx, "handoff-1", taskID, "requester", "")
	if err != nil {
		t.Fatalf("first RequestTaskHandoff failed: %v", err)
	}
	second, err := s.RequestTaskHandoff(ctx, "handoff-2", taskID, "requester", "")
	if err != nil {
		t.Fatalf("second RequestTaskHandoff failed: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("same task handoffs must have distinct IDs: %q", first.ID)
	}

	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		t.Fatalf("ListTaskHandoffs failed: %v", err)
	}
	if len(handoffs) != 2 {
		t.Fatalf("got %d handoffs, want 2: %+v", len(handoffs), handoffs)
	}
}

func TestTaskHandoffReceiveByTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "requester")
	addTestAgentSession(t, s, "receiver")
	addLiveTaskClaim(t, s, taskID, "receive-by-task-claim-owner")

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

func TestTaskHandoffReceiveByTaskRejectsMultipleUnreceived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "requester")
	addTestAgentSession(t, s, "receiver")
	addLiveTaskClaim(t, s, taskID, "ambiguous-receive-claim-owner")

	for _, handoffID := range []string{"ambiguous-handoff-1", "ambiguous-handoff-2"} {
		if _, err := s.RequestTaskHandoff(ctx, handoffID, taskID, "requester", ""); err != nil {
			t.Fatalf("RequestTaskHandoff(%q) failed: %v", handoffID, err)
		}
	}

	_, err := s.ReceiveTaskHandoffForTask(ctx, taskID, "receiver")
	if !errors.Is(err, ErrTaskHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrTaskHandoffAmbiguous", err)
	}
}

func TestTaskHandoffRejectsMissingTask(t *testing.T) {
	s := newTestStore(t)
	addTestAgentSession(t, s, "requester")

	if _, err := s.RequestTaskHandoff(context.Background(), "handoff-missing", "missing-task", "requester", ""); err == nil {
		t.Fatal("RequestTaskHandoff should reject a missing task")
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
	addTestAgentSession(t, s, "requester")
	addLiveTaskClaim(t, s, taskID, "live-claim-owner")

	if _, err := s.RequestTaskHandoff(ctx, "handoff-live-claim", taskID, "requester", ""); err != nil {
		t.Fatalf("RequestTaskHandoff with a live claim failed: %v", err)
	}
}

func TestTaskHandoffRejectsDeadClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "requester")
	addTestAgentSession(t, s, "dead-claim-owner")

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE tasks SET claimed_by = ?, claimed_at = ?, updated_at = ? WHERE id = ?
	`, "dead-claim-owner", now, now, taskID); err != nil {
		t.Fatalf("dead claim fixture insert failed: %v", err)
	}

	if _, err := s.RequestTaskHandoff(ctx, "handoff-dead-claim", taskID, "requester", ""); err == nil {
		t.Fatal("RequestTaskHandoff should reject a task with only a dead claim")
	}
}

func TestTaskHandoffReceiveAllowsUnclaimedTask(t *testing.T) {
	s := newTestStore(t)
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "receiver")

	handoff, err := s.ReceiveTaskHandoff(context.Background(), "handoff-receipt-only", taskID, "receiver")
	if err != nil {
		t.Fatalf("ReceiveTaskHandoff without a claim failed: %v", err)
	}
	if handoff.RequestedAt != nil || handoff.ReceivedAt == nil {
		t.Fatalf("receipt-only handoff has unexpected state: %+v", handoff)
	}
}

func TestTaskHandoffUnclaimedErrorIsDistinguishable(t *testing.T) {
	s := newTestStore(t)
	taskID := addTestTasks(t, s, 1)[0]
	addTestAgentSession(t, s, "requester")

	_, err := s.RequestTaskHandoff(context.Background(), "handoff-unclaimed-error", taskID, "requester", "")
	if !errors.Is(err, ErrTaskHandoffTaskUnclaimed) {
		t.Fatalf("error = %v, want ErrTaskHandoffTaskUnclaimed", err)
	}
	if errors.Is(err, ErrTaskHandoffNotFound) {
		t.Fatalf("unclaimed error must not be ErrTaskHandoffNotFound: %v", err)
	}
}

func TestTaskHandoffStatesRemainDistinct(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskIDs := addTestTasks(t, s, 4)
	addTestAgentSession(t, s, "requester")
	addTestAgentSession(t, s, "receiver")

	// A task claim without a handoff request.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB().ExecContext(ctx, `
		UPDATE tasks SET claimed_by = ?, claimed_at = ?, updated_at = ? WHERE id = ?
	`, "receiver", now, now, taskIDs[0])
	if err != nil {
		t.Fatalf("claim fixture insert failed: %v", err)
	}

	// A handoff request without a task claim. This fixture bypasses the public
	// request method because the state is intentionally a detected anomaly.
	addRequestOnlyTaskHandoff(t, s, "request-only", taskIDs[1], "requester")

	// A handoff request without a receipt.
	addLiveTaskClaim(t, s, taskIDs[2], "unreceived-claim-owner")
	requested, err := s.RequestTaskHandoff(ctx, "unreceived", taskIDs[2], "requester", "")
	if err != nil {
		t.Fatalf("unreceived fixture insert failed: %v", err)
	}

	// A receipt without a request, proving the three timestamp columns are
	// independently nullable rather than one overloaded handoff timestamp.
	_, err = s.ReceiveTaskHandoff(ctx, "receipt-without-request", taskIDs[3], "receiver")
	if err != nil {
		t.Fatalf("receipt-only fixture insert failed: %v", err)
	}

	claimed := make(map[string]string, len(taskIDs))
	for _, taskID := range taskIDs {
		var claimedBy string
		if err := s.DB().QueryRowContext(ctx, `SELECT claimed_by FROM tasks WHERE id = ?`, taskID).Scan(&claimedBy); err != nil {
			t.Fatalf("read claim fixture for %q failed: %v", taskID, err)
		}
		claimed[taskID] = claimedBy
	}
	if claimed[taskIDs[0]] != "receiver" || claimed[taskIDs[1]] != "" {
		t.Fatalf("claim/request states are not distinct: %+v", claimed)
	}

	if got, err := s.ListTaskHandoffs(ctx, taskIDs[0]); err != nil {
		t.Fatalf("ListTaskHandoffs for claim-only task failed: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("claim-only task unexpectedly has handoffs: %+v", got)
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
	if got, err := s.GetTaskHandoff(ctx, "receipt-without-request"); err != nil {
		t.Fatalf("GetTaskHandoff for receipt-only state failed: %v", err)
	} else if got.RequestedAt != nil || got.ReceivedAt == nil || got.CompletedReportAt != nil {
		t.Fatalf("receipt-only state is not distinct: %+v", got)
	}
}
