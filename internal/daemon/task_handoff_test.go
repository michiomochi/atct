package daemon

import (
	"context"
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
	claimedTaskID   string
	unclaimedTaskID string
	claimableTaskID string
}

func newTaskHandoffRPCTestFixture(t *testing.T) taskHandoffRPCTestFixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct-th-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("RemoveAll(%q): %v", dir, err)
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
	if err := s.RegisterAgentSession(ctx, "rpc-handoff-requester", os.Getpid()); err != nil {
		s.Close()
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "rpc-handoff-receiver", os.Getpid()); err != nil {
		s.Close()
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goal.ID, "rpc-handoff-requester"); err != nil {
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
	}
}

func addTaskHandoffDirect(t *testing.T, s *store.Store, handoffID, taskID, requestedBy, receivedBy string) {
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

func TestTaskHandoffRoutesOverRPC(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.TaskHandoff
	if err := client.Call(ctx, "handoff.request", map[string]string{
		"handoff_id": "rpc-handoff-1", "task_id": fixture.claimedTaskID, "requested_by": "rpc-handoff-requester",
		"request_report": "RPC task request report",
	}, &requested); err != nil {
		t.Fatalf("handoff.request: %v", err)
	}
	if requested.RequestedAt == nil || requested.RequestedBy != "rpc-handoff-requester" || requested.RequestReport != "RPC task request report" {
		t.Fatalf("requested handoff = %#v, want request timestamp, requester, and report", requested)
	}

	var received store.TaskHandoff
	if err := client.Call(ctx, "handoff.receive", map[string]string{
		"handoff_id": "rpc-handoff-1", "task_id": fixture.claimedTaskID, "received_by": "rpc-handoff-receiver",
	}, &received); err != nil {
		t.Fatalf("handoff.receive: %v", err)
	}
	if received.ReceivedAt == nil || received.ReceivedBy != "rpc-handoff-receiver" {
		t.Fatalf("received handoff = %#v, want receipt timestamp and receiver", received)
	}

	var completed store.TaskHandoff
	if err := client.Call(ctx, "handoff.complete", map[string]string{
		"handoff_id": "rpc-handoff-1", "task_id": fixture.claimedTaskID, "complete_report": "RPC task completion report",
	}, &completed); err != nil {
		t.Fatalf("handoff.complete: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "RPC task completion report" {
		t.Fatalf("completed handoff = %#v, want completion timestamp and report", completed)
	}

	var claimed domain.Task
	if err := client.Call(ctx, "task.claim", map[string]any{
		"task_id": fixture.claimableTaskID, "agent_session_id": "rpc-claimer",
		"include_unapplied_answers": false,
	}, &claimed); err != nil {
		t.Fatalf("existing task.claim RPC: %v", err)
	}
	if claimed.ID != fixture.claimableTaskID {
		t.Fatalf("task.claim returned %q, want %q", claimed.ID, fixture.claimableTaskID)
	}

	var rejected store.TaskHandoff
	err := client.Call(ctx, "handoff.request", map[string]string{
		"handoff_id": "rpc-handoff-unclaimed", "task_id": fixture.unclaimedTaskID, "requested_by": "rpc-handoff-requester",
	}, &rejected)
	if err == nil {
		t.Fatalf("unclaimed handoff request succeeded: %#v", rejected)
	}
	if !strings.Contains(err.Error(), store.ErrTaskHandoffGoalNotHeld.Error()) {
		t.Fatalf("unclaimed handoff request error = %q, want %q", err, store.ErrTaskHandoffGoalNotHeld)
	}
}

func TestTaskHandoffCompleteByTaskOverRPC(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.TaskHandoff
	if err := client.Call(ctx, "handoff.request", map[string]string{
		"handoff_id": "rpc-complete-by-task", "task_id": fixture.claimedTaskID, "requested_by": "rpc-handoff-requester",
	}, &requested); err != nil {
		t.Fatalf("handoff.request: %v", err)
	}
	var received store.TaskHandoff
	if err := client.Call(ctx, "handoff.receive", map[string]string{
		"handoff_id": requested.ID, "task_id": fixture.claimedTaskID, "received_by": "rpc-handoff-receiver",
	}, &received); err != nil {
		t.Fatalf("handoff.receive: %v", err)
	}

	var completed store.TaskHandoff
	if err := client.Call(ctx, "handoff.complete", map[string]string{
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
	if err := client.Call(ctx, "handoff.request", map[string]string{
		"handoff_id": "rpc-task-complete-ambiguous-1", "task_id": fixture.claimedTaskID, "requested_by": "rpc-handoff-requester",
	}, &requested); err != nil {
		t.Fatalf("handoff.request: %v", err)
	}
	var received store.TaskHandoff
	if err := client.Call(ctx, "handoff.receive", map[string]string{
		"handoff_id": requested.ID, "task_id": fixture.claimedTaskID, "received_by": "rpc-handoff-receiver",
	}, &received); err != nil {
		t.Fatalf("handoff.receive: %v", err)
	}
	addTaskHandoffDirect(t, fixture.store, "rpc-task-complete-ambiguous-2", fixture.claimedTaskID, "rpc-handoff-requester", "rpc-handoff-receiver")

	var completed store.TaskHandoff
	err := client.Call(ctx, "handoff.complete", map[string]string{
		"task_id": fixture.claimedTaskID,
	}, &completed)
	if err == nil {
		t.Fatalf("ambiguous task handoff complete succeeded: %#v", completed)
	}
	if !strings.Contains(err.Error(), store.ErrTaskHandoffAmbiguous.Error()) {
		t.Fatalf("ambiguous task handoff complete error = %q, want %q", err, store.ErrTaskHandoffAmbiguous)
	}
}
