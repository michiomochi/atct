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
	dir := t.TempDir()
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
		"claimed task", "unclaimed task", "claimable task",
	}, []string{
		"A task with a live claim for the handoff request.",
		"A task without a claim for rejection coverage.",
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
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "rpc-handoff-requester"); err != nil {
		s.Close()
		t.Fatalf("ClaimTask: %v", err)
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
		unclaimedTaskID: tasks[1].ID,
		claimableTaskID: tasks[2].ID,
	}
}

func TestTaskHandoffRoutesOverRPC(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.TaskHandoff
	if err := client.Call(ctx, "handoff.request", map[string]string{
		"handoff_id": "rpc-handoff-1", "task_id": fixture.claimedTaskID, "requested_by": "rpc-handoff-requester",
	}, &requested); err != nil {
		t.Fatalf("handoff.request: %v", err)
	}
	if requested.RequestedAt == nil || requested.RequestedBy != "rpc-handoff-requester" {
		t.Fatalf("requested handoff = %#v, want request timestamp and requester", requested)
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
		"handoff_id": "rpc-handoff-1", "task_id": fixture.claimedTaskID,
	}, &completed); err != nil {
		t.Fatalf("handoff.complete: %v", err)
	}
	if completed.CompletedReportAt == nil {
		t.Fatalf("completed handoff = %#v, want completion timestamp", completed)
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
	if !strings.Contains(err.Error(), store.ErrTaskHandoffTaskUnclaimed.Error()) {
		t.Fatalf("unclaimed handoff request error = %q, want %q", err, store.ErrTaskHandoffTaskUnclaimed)
	}
}
