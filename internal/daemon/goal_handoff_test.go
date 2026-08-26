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

	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

type goalHandoffRPCTestFixture struct {
	store           *store.Store
	socketPath      string
	claimedGoalID   int64
	unclaimedGoalID int64
	requesterID     int64
	receiverID      int64
}

func newGoalHandoffRPCTestFixture(t *testing.T) goalHandoffRPCTestFixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct-gh-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
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
	claimedGoal, err := s.CreateGoal(ctx, project.ID, "claimed handoff goal", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal claimed: %v", err)
	}
	unclaimedProject, err := s.CreateProject(ctx, "other", filepath.Join(dir, "other-repo"))
	if err != nil {
		s.Close()
		t.Fatalf("CreateProject unclaimed: %v", err)
	}
	unclaimedGoal, err := s.CreateGoal(ctx, unclaimedProject.ID, "unclaimed handoff goal", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal unclaimed: %v", err)
	}

	requesterID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		s.Close()
		t.Fatalf("RegisterAgentSession(requester): %v", err)
	}
	receiverID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		s.Close()
		t.Fatalf("RegisterAgentSession(receiver): %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, requesterID, project.ID); err != nil {
		s.Close()
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimProject(ctx, project.ID, requesterID); err != nil {
		s.Close()
		t.Fatalf("ClaimProject: %v", err)
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

	return goalHandoffRPCTestFixture{
		store:           s,
		socketPath:      socketPath,
		claimedGoalID:   claimedGoal.ID,
		unclaimedGoalID: unclaimedGoal.ID,
		requesterID:     requesterID,
		receiverID:      receiverID,
	}
}

func addGoalHandoffDirect(t *testing.T, s *store.Store, handoffID string, goalID, requestedBy, receivedBy int64) {
	t.Helper()

	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `DROP INDEX IF EXISTS idx_goal_handoffs_open_goal_id`); err != nil {
		t.Fatalf("drop goal handoff uniqueness index: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM goal_handoffs WHERE id = ?`, handoffID); err != nil {
			t.Errorf("delete direct goal handoff %v: %v", handoffID, err)
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
	if receivedBy == 0 {
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
		t.Fatalf("insert direct goal handoff %v failed: %v", handoffID, err)
	}
}

func TestGoalHandoffRoutesOverRPC(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": "rpc-goal-handoff-1", "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
		"request_report": "RPC goal request report",
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	if requested.GoalID != fixture.claimedGoalID || requested.RequestedAt == nil || requested.RequestedBy != fixture.requesterID || requested.RequestReport != "RPC goal request report" {
		t.Fatalf("requested handoff = %#v, want goal, timestamp, requester, and report", requested)
	}

	var received store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive by goal_id: %v", err)
	}
	if received.ID != requested.ID || received.ReceivedAt == nil || received.ReceivedBy != fixture.receiverID {
		t.Fatalf("received handoff = %#v, want request ID, timestamp, and receiver", received)
	}

	var completed store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.complete", map[string]any{
		"handoff_id": requested.ID, "goal_id": fixture.claimedGoalID, "complete_report": "RPC goal completion report",
	}, &completed); err != nil {
		t.Fatalf("goal.handoff.complete: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "RPC goal completion report" {
		t.Fatalf("completed handoff = %#v, want completion timestamp and report", completed)
	}

	var rejected store.GoalHandoff
	err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": "rpc-goal-handoff-unclaimed", "goal_id": fixture.unclaimedGoalID, "requested_by": fixture.requesterID,
	}, &rejected)
	if err == nil {
		t.Fatalf("unclaimed goal handoff request succeeded: %#v", rejected)
	}
	if !strings.Contains(err.Error(), store.ErrGoalHandoffProjectNotHeld.Error()) {
		t.Fatalf("unclaimed goal handoff request error = %v, want %v", err, store.ErrGoalHandoffProjectNotHeld)
	}
}

func TestGoalHandoffCompleteByGoalOverRPC(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": "rpc-complete-by-goal", "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	var received store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"handoff_id": requested.ID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}

	var completed store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.complete", map[string]any{
		"goal_id": fixture.claimedGoalID, "complete_report": "RPC goal-ID completion report",
	}, &completed); err != nil {
		t.Fatalf("goal.handoff.complete by goal_id: %v", err)
	}
	if completed.ID != requested.ID || completed.CompletedReportAt == nil || completed.CompleteReport != "RPC goal-ID completion report" {
		t.Fatalf("completed handoff = %#v, want request ID, timestamp, and report", completed)
	}
}

func TestGoalHandoffCompleteByGoalOverRPCRejectsAmbiguousPendingRequests(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": "rpc-goal-complete-ambiguous-1", "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	var received store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"handoff_id": requested.ID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}
	addGoalHandoffDirect(t, fixture.store, "rpc-goal-complete-ambiguous-2", fixture.claimedGoalID, fixture.requesterID, fixture.receiverID)

	var completed store.GoalHandoff
	err := client.Call(ctx, "goal.handoff.complete", map[string]any{
		"goal_id": fixture.claimedGoalID,
	}, &completed)
	if err == nil {
		t.Fatalf("ambiguous goal handoff complete succeeded: %#v", completed)
	}
	if !strings.Contains(err.Error(), store.ErrGoalHandoffAmbiguous.Error()) {
		t.Fatalf("ambiguous goal handoff complete error = %v, want %v", err, store.ErrGoalHandoffAmbiguous)
	}
}

func TestGoalHandoffReceiveOverRPCRejectsAmbiguousPendingRequests(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": "rpc-goal-ambiguous-1", "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	addGoalHandoffDirect(t, fixture.store, "rpc-goal-ambiguous-2", fixture.claimedGoalID, fixture.requesterID, 0)

	var received store.GoalHandoff
	err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &received)
	if err == nil {
		t.Fatalf("ambiguous goal handoff receive succeeded: %#v", received)
	}
	if !strings.Contains(err.Error(), store.ErrGoalHandoffAmbiguous.Error()) {
		t.Fatalf("ambiguous goal handoff receive error = %v, want %v", err, store.ErrGoalHandoffAmbiguous)
	}
}
