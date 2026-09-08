package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

type taskHandoffRPCTestFixture struct {
	store           *store.Store
	socketPath      string
	claimedTaskID   int64
	unclaimedTaskID int64
	claimableTaskID int64
	requesterID     int64
	receiverID      int64
}

type handoffReceiveResponse struct {
	Data          json.RawMessage `json:"data"`
	Role          string          `json:"role"`
	ClaimEvidence struct {
		Scope          string `json:"scope"`
		AgentSessionID int64  `json:"agent_session_id"`
		ProjectID      int64  `json:"project_id,omitempty"`
		GoalID         int64  `json:"goal_id,omitempty"`
		TaskID         int64  `json:"task_id,omitempty"`
		HandoffID      string `json:"handoff_id,omitempty"`
	} `json:"claim_evidence"`
}

func newTaskHandoffRPCTestFixture(t *testing.T) taskHandoffRPCTestFixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct-th-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("RemoveAll(%v): %v", dir, err)
		}
	})
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", filepath.Join(dir, "repo"))
	if err != nil {
		s.Close()
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "handoff", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "commander", "handoff-rpc", []string{
		"delegated task", "claimable task",
	}, []string{
		"A task delegated by the goal owner.",
		"A task used to verify the existing task claim RPC.",
	})
	if err != nil {
		s.Close()
		t.Fatalf("DeclareTasks: %v", err)
	}
	requesterID := daemonTestSessionID(t, s, "rpc-handoff-requester")
	receiverID := daemonTestSessionID(t, s, "rpc-handoff-receiver")
	if _, err := s.ClaimGoal(ctx, goal.ID, requesterID); err != nil {
		s.Close()
		t.Fatalf("ClaimGoal: %v", err)
	}
	unclaimedGoal, err := s.CreateGoal(ctx, project.ID, "unclaimed handoff goal", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal unclaimed: %v", err)
	}
	unclaimedTasks, err := s.DeclareTasks(ctx, unclaimedGoal.ID, "commander", "unclaimed-handoff-rpc", []string{"unclaimed task"}, []string{"A task in a goal without a claim."})
	if err != nil {
		s.Close()
		t.Fatalf("DeclareTasks unclaimed: %v", err)
	}

	socketPath := filepath.Join(dir, "daemon.sock")
	serveCtx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	d := New(s)
	go func() { serveDone <- d.Serve(serveCtx, socketPath) }()

	var conn net.Conn
	for i := 0; i < 100; i++ {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			break
		}
		select {
		case serveErr := <-serveDone:
			cancel()
			s.Close()
			t.Fatalf("daemon.Serve exited before socket appeared: %v", serveErr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		cancel()
		s.Close()
		t.Fatalf("dial daemon: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-serveDone:
			if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
				t.Errorf("daemon.Serve: %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("daemon.Serve did not stop after cancellation")
		}
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("store.Close: %v", closeErr)
		}
	})

	return taskHandoffRPCTestFixture{
		store:           s,
		socketPath:      socketPath,
		claimedTaskID:   tasks[0].ID,
		unclaimedTaskID: unclaimedTasks[0].ID,
		claimableTaskID: tasks[1].ID,
		requesterID:     requesterID,
		receiverID:      receiverID,
	}
}

func addTaskHandoffDirect(t *testing.T, s *store.Store, handoffID string, taskID, requestedBy, receivedBy int64) {
	t.Helper()

	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `DROP INDEX IF EXISTS idx_task_handoffs_open_task_id`); err != nil {
		t.Fatalf("drop task handoff uniqueness index: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM task_handoffs WHERE id = ?`, handoffID); err != nil {
			t.Errorf("delete direct task handoff %v: %v", handoffID, err)
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
	if receivedBy == 0 {
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
		t.Fatalf("insert direct task handoff %v failed: %v", handoffID, err)
	}
}

func TestTaskHandoffRoutesOverRPC(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.TaskHandoff
	if err := client.Call(ctx, "handoff.request", map[string]any{
		"handoff_id": "rpc-handoff-1", "task_id": fixture.claimedTaskID, "requested_by": fixture.requesterID,
		"request_report": "RPC task request report",
	}, &requested); err != nil {
		t.Fatalf("handoff.request: %v", err)
	}
	if requested.RequestedAt == nil || requested.RequestedBy != fixture.requesterID || requested.RequestReport != "RPC task request report" {
		t.Fatalf("requested handoff = %#v, want request timestamp, requester, and report", requested)
	}

	var received store.TaskHandoff
	if err := client.Call(ctx, "handoff.receive", map[string]any{
		"handoff_id": "rpc-handoff-1", "task_id": fixture.claimedTaskID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("handoff.receive: %v", err)
	}
	if received.ReceivedAt == nil || received.ReceivedBy != fixture.receiverID {
		t.Fatalf("received handoff = %#v, want receipt timestamp and receiver", received)
	}

	var completed store.TaskHandoff
	if err := client.Call(ctx, "handoff.complete", map[string]any{
		"handoff_id": "rpc-handoff-1", "task_id": fixture.claimedTaskID, "complete_report": "RPC task completion report",
	}, &completed); err != nil {
		t.Fatalf("handoff.complete: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "RPC task completion report" {
		t.Fatalf("completed handoff = %#v, want completion timestamp and report", completed)
	}

	var claimed domain.Task
	claimerID := daemonTestSessionID(t, fixture.store, "rpc-claimer")
	if err := client.Call(ctx, "task.claim", map[string]any{
		"task_id": fixture.claimableTaskID, "agent_session_id": claimerID,
		"include_unapplied_answers": false,
	}, &claimed); err != nil {
		t.Fatalf("existing task.claim RPC: %v", err)
	}
	if claimed.ID != fixture.claimableTaskID {
		t.Fatalf("task.claim returned %v, want %v", claimed.ID, fixture.claimableTaskID)
	}

	var rejected store.TaskHandoff
	err := client.Call(ctx, "handoff.request", map[string]any{
		"handoff_id": "rpc-handoff-unclaimed", "task_id": fixture.unclaimedTaskID, "requested_by": fixture.requesterID,
	}, &rejected)
	if err == nil {
		t.Fatalf("unclaimed handoff request succeeded: %#v", rejected)
	}
	if !strings.Contains(err.Error(), store.ErrTaskHandoffGoalNotHeld.Error()) {
		t.Fatalf("unclaimed handoff request error = %v, want %v", err, store.ErrTaskHandoffGoalNotHeld)
	}
}

func TestNamedTaskHandoffReviewRoutesReturnRoleEvidence(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	const handoffID = "named-task-review"
	var requested store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.request", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "requested_by": fixture.requesterID,
		"request_report": "named task request report",
	}, &requested); err != nil {
		t.Fatalf("task.handoff.request: %v", err)
	}

	var received handoffReceiveResponse
	if err := client.Call(ctx, "task.handoff.receive", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("task.handoff.receive: %v", err)
	}
	var receivedData store.TaskHandoff
	if err := json.Unmarshal(received.Data, &receivedData); err != nil {
		t.Fatalf("decode task.handoff.receive data: %v", err)
	}
	if receivedData.ID != handoffID || receivedData.ReceivedBy != fixture.receiverID || receivedData.ReceivedAt == nil {
		t.Fatalf("received data = %#v, want received task handoff", receivedData)
	}
	if received.Role != "executor" || received.ClaimEvidence.Scope != "task" || received.ClaimEvidence.TaskID != fixture.claimedTaskID || received.ClaimEvidence.HandoffID != handoffID || received.ClaimEvidence.AgentSessionID != fixture.receiverID {
		t.Fatalf("receive role/evidence = %+v, want executor task evidence", received)
	}

	var reviewRequested store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "requested_by": fixture.receiverID,
		"review_request_report": "named task review report",
	}, &reviewRequested); err != nil {
		t.Fatalf("task.handoff.review.request: %v", err)
	}
	if reviewRequested.ReviewRequestedBy != fixture.receiverID || reviewRequested.ReviewRequestedAt == nil {
		t.Fatalf("review-requested handoff = %#v, want review request metadata", reviewRequested)
	}

	var reviewReceived handoffReceiveResponse
	if err := client.Call(ctx, "task.handoff.review.receive", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "received_by": fixture.requesterID,
	}, &reviewReceived); err != nil {
		t.Fatalf("task.handoff.review.receive: %v", err)
	}
	if reviewReceived.Role == "" || reviewReceived.ClaimEvidence.Scope == "" || reviewReceived.ClaimEvidence.HandoffID != handoffID {
		t.Fatalf("review receive role/evidence = %+v, want role and handoff evidence", reviewReceived)
	}

	var completed store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.complete", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "agent_session_id": fixture.requesterID,
		"complete_report": "named task completion report",
	}, &completed); err != nil {
		t.Fatalf("task.handoff.complete: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "named task completion report" {
		t.Fatalf("completed handoff = %#v, want reviewer completion", completed)
	}
	goalID, err := fixture.store.GetTaskGoalID(ctx, fixture.claimedTaskID)
	if err != nil {
		t.Fatalf("GetTaskGoalID: %v", err)
	}
	tasks, err := fixture.store.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, task := range tasks {
		if task.ID == fixture.claimedTaskID && task.Status != "done" {
			t.Fatalf("task status = %q, want done after named handoff completion", task.Status)
		}
	}
}

func TestNamedTaskHandoffReviewRejectsAndRetriesWithSameWorker(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	const handoffID = "named-task-review-retry"
	var requested store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.request", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "requested_by": fixture.requesterID,
	}, &requested); err != nil {
		t.Fatalf("task.handoff.request: %v", err)
	}

	var received handoffReceiveResponse
	if err := client.Call(ctx, "task.handoff.receive", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("task.handoff.receive: %v", err)
	}
	if received.Role != "executor" || received.ClaimEvidence.AgentSessionID != fixture.receiverID {
		t.Fatalf("task handoff receive = %+v, want executor evidence for receiver %d", received, fixture.receiverID)
	}

	var reviewRequested store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "requested_by": fixture.receiverID,
		"review_request_report": "initial implementation is ready",
	}, &reviewRequested); err != nil {
		t.Fatalf("task.handoff.review.request: %v", err)
	}

	var reviewReceived handoffReceiveResponse
	if err := client.Call(ctx, "task.handoff.review.receive", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "received_by": fixture.requesterID,
	}, &reviewReceived); err != nil {
		t.Fatalf("task.handoff.review.receive: %v", err)
	}
	if reviewReceived.Role == "" || reviewReceived.ClaimEvidence.AgentSessionID != fixture.requesterID {
		t.Fatalf("task review receive = %+v, want reviewer evidence for requester %d", reviewReceived, fixture.requesterID)
	}

	var rejected store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.review.reject", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "reviewer_id": fixture.requesterID,
		"reject_report": "add focused coverage",
	}, &rejected); err != nil {
		t.Fatalf("task.handoff.review.reject: %v", err)
	}
	if rejected.ID != handoffID || rejected.ReceivedBy != fixture.receiverID || rejected.ReviewReceivedBy != 0 || rejected.ReviewReceivedAt != nil || rejected.ReviewRejectedAt == nil || rejected.ReviewRejectReport != "add focused coverage" {
		t.Fatalf("rejected task handoff = %+v, want same receiver with cleared reviewer receipt", rejected)
	}
	if err := client.Call(ctx, "task.handoff.review.reject.receive", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "received_by": fixture.requesterID,
	}, &rejected); err == nil {
		t.Fatal("task.handoff.review.reject.receive accepted the reviewer instead of the original submitter")
	}
	if err := client.Call(ctx, "task.handoff.review.reject.receive", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "received_by": fixture.receiverID,
	}, &rejected); err != nil {
		t.Fatalf("task.handoff.review.reject.receive: %v", err)
	}

	var retried store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "requested_by": fixture.receiverID,
		"review_request_report": "focused coverage added",
	}, &retried); err != nil {
		t.Fatalf("retry task.handoff.review.request: %v", err)
	}
	if retried.ID != handoffID || retried.ReceivedBy != fixture.receiverID || retried.ReviewRequestedBy != fixture.receiverID || retried.ReviewRejectedAt != nil || retried.ReviewRejectReport != "" {
		t.Fatalf("retried task handoff = %+v, want same handoff and same receiver with fresh review state", retried)
	}

	if err := client.Call(ctx, "task.handoff.review.receive", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "received_by": fixture.requesterID,
	}, &reviewReceived); err != nil {
		t.Fatalf("retry task.handoff.review.receive: %v", err)
	}

	var completed store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.complete", map[string]any{
		"handoff_id": handoffID, "task_id": fixture.claimedTaskID, "agent_session_id": fixture.requesterID,
		"complete_report": "approved after focused coverage",
	}, &completed); err != nil {
		t.Fatalf("task.handoff.complete after retry: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "approved after focused coverage" {
		t.Fatalf("completed task handoff = %+v, want completion after same-worker retry", completed)
	}

	const newTaskHandoffID = "named-task-new-worker"
	var newTaskRequested store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.request", map[string]any{
		"handoff_id": newTaskHandoffID, "task_id": fixture.claimableTaskID, "requested_by": fixture.requesterID,
	}, &newTaskRequested); err != nil {
		t.Fatalf("new task.handoff.request: %v", err)
	}
	var newTaskReceived handoffReceiveResponse
	if err := client.Call(ctx, "task.handoff.receive", map[string]any{
		"handoff_id": newTaskHandoffID, "task_id": fixture.claimableTaskID, "received_by": fixture.receiverID,
	}, &newTaskReceived); err != nil {
		t.Fatalf("new task.handoff.receive: %v", err)
	}
	if newTaskReceived.Role != "executor" || newTaskReceived.ClaimEvidence.AgentSessionID != fixture.receiverID || newTaskReceived.ClaimEvidence.HandoffID == handoffID {
		t.Fatalf("new task handoff receive = %+v, want the idle executor to reuse its new handoff", newTaskReceived)
	}
}

func TestTaskHandoffCompleteByTaskOverRPC(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.TaskHandoff
	if err := client.Call(ctx, "handoff.request", map[string]any{
		"handoff_id": "rpc-complete-by-task", "task_id": fixture.claimedTaskID, "requested_by": fixture.requesterID,
	}, &requested); err != nil {
		t.Fatalf("handoff.request: %v", err)
	}
	var received store.TaskHandoff
	if err := client.Call(ctx, "handoff.receive", map[string]any{
		"handoff_id": requested.ID, "task_id": fixture.claimedTaskID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("handoff.receive: %v", err)
	}

	var completed store.TaskHandoff
	if err := client.Call(ctx, "handoff.complete", map[string]any{
		"task_id": fixture.claimedTaskID, "complete_report": "RPC task-ID completion report",
	}, &completed); err != nil {
		t.Fatalf("handoff.complete by task_id: %v", err)
	}
	if completed.ID != requested.ID || completed.CompletedReportAt == nil || completed.CompleteReport != "RPC task-ID completion report" {
		t.Fatalf("completed handoff = %#v, want request ID, timestamp, and report", completed)
	}
}

func TestTaskHandoffCompleteByTaskOverRPCRejectsAmbiguousPendingRequests(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.TaskHandoff
	if err := client.Call(ctx, "handoff.request", map[string]any{
		"handoff_id": "rpc-task-complete-ambiguous-1", "task_id": fixture.claimedTaskID, "requested_by": fixture.requesterID,
	}, &requested); err != nil {
		t.Fatalf("handoff.request: %v", err)
	}
	var received store.TaskHandoff
	if err := client.Call(ctx, "handoff.receive", map[string]any{
		"handoff_id": requested.ID, "task_id": fixture.claimedTaskID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("handoff.receive: %v", err)
	}
	addTaskHandoffDirect(t, fixture.store, "rpc-task-complete-ambiguous-2", fixture.claimedTaskID, fixture.requesterID, fixture.receiverID)

	var completed store.TaskHandoff
	err := client.Call(ctx, "handoff.complete", map[string]any{
		"task_id": fixture.claimedTaskID,
	}, &completed)
	if err == nil {
		t.Fatalf("ambiguous task handoff complete succeeded: %#v", completed)
	}
	if !strings.Contains(err.Error(), store.ErrTaskHandoffAmbiguous.Error()) {
		t.Fatalf("ambiguous task handoff complete error = %v, want %v", err, store.ErrTaskHandoffAmbiguous)
	}
}

func TestTaskHandoffYieldedPublishesOnlyForReceivedIncompleteHandoff(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	events, cancel := fixture.store.SubscribeEvents()
	defer cancel()

	if err := client.Call(ctx, "handoff.yielded", map[string]any{
		"task_id": fixture.claimedTaskID,
	}, nil); err != nil {
		t.Fatalf("handoff.yielded: %v", err)
	}
	assertNoTaskHandoffYieldedEvent(t, events, "task without a handoff")

	addTaskHandoffDirect(t, fixture.store, "yielded-open", fixture.claimedTaskID, fixture.requesterID, fixture.receiverID)
	if err := client.Call(ctx, "handoff.yielded", map[string]any{
		"task_id": fixture.claimedTaskID,
	}, nil); err != nil {
		t.Fatalf("handoff.yielded with open handoff: %v", err)
	}

	var event store.DecisionEvent
	select {
	case event = <-events:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handoff_yielded event")
	}
	if event.Name != store.EventHandoffYielded {
		t.Fatalf("event name = %v, want %v", event.Name, store.EventHandoffYielded)
	}
	detection, ok := event.Data.(store.DetectionEvent)
	if !ok {
		t.Fatalf("event data type = %T, want store.DetectionEvent", event.Data)
	}
	goalID, err := fixture.store.GetTaskGoalID(ctx, fixture.claimedTaskID)
	if err != nil {
		t.Fatalf("GetTaskGoalID: %v", err)
	}
	goal, err := fixture.store.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if detection.ProjectID != goal.ProjectID || detection.GoalID != goalID || detection.TaskID != fixture.claimedTaskID || detection.HandoffID != "" || detection.CompleteReport != "" {
		t.Fatalf("yielded event data = %+v, want project=%v goal=%v task=%v with no handoff/report", detection, goal.ProjectID, goalID, fixture.claimedTaskID)
	}

	handoffs, err := fixture.store.ListTaskHandoffs(ctx, fixture.claimedTaskID)
	if err != nil {
		t.Fatalf("ListTaskHandoffs: %v", err)
	}
	if len(handoffs) != 1 || handoffs[0].CompletedReportAt != nil {
		t.Fatalf("handoffs after yielded = %#v, want one incomplete handoff", handoffs)
	}
}

func TestTaskHandoffYieldedIgnoresCompletedHandoff(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	addTaskHandoffDirect(t, fixture.store, "yielded-completed", fixture.claimedTaskID, fixture.requesterID, fixture.receiverID)
	if _, err := fixture.store.CompleteTaskHandoff(ctx, "yielded-completed", fixture.claimedTaskID, "already complete"); err != nil {
		t.Fatalf("CompleteTaskHandoff: %v", err)
	}
	events, cancel := fixture.store.SubscribeEvents()
	defer cancel()

	if err := client.Call(ctx, "handoff.yielded", map[string]any{
		"task_id": fixture.claimedTaskID,
	}, nil); err != nil {
		t.Fatalf("handoff.yielded with completed handoff: %v", err)
	}
	assertNoTaskHandoffYieldedEvent(t, events, "completed handoff")

	handoffs, err := fixture.store.ListTaskHandoffs(ctx, fixture.claimedTaskID)
	if err != nil {
		t.Fatalf("ListTaskHandoffs: %v", err)
	}
	if len(handoffs) != 1 || handoffs[0].CompletedReportAt == nil {
		t.Fatalf("handoffs after completed yielded = %#v, want one completed handoff", handoffs)
	}
}

func assertNoTaskHandoffYieldedEvent(t *testing.T, events <-chan store.DecisionEvent, caseName string) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("%v published unexpected event: %#v", caseName, event)
	case <-time.After(50 * time.Millisecond):
	}
}
