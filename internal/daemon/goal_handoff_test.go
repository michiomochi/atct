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
	claimedGoalID   string
	unclaimedGoalID string
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
	unclaimedGoal, err := s.CreateGoal(ctx, project.ID, "unclaimed handoff goal", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal unclaimed: %v", err)
	}

	if err := s.RegisterAgentSession(ctx, "rpc-goal-claim-owner", os.Getpid()); err != nil {
		s.Close()
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	for _, sessionID := range []string{"rpc-goal-requester", "rpc-goal-receiver"} {
		if err := s.RegisterAgentSession(ctx, sessionID, os.Getpid()); err != nil {
			s.Close()
			t.Fatalf("RegisterAgentSession(%s): %v", sessionID, err)
		}
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "rpc-goal-claim-owner", project.ID); err != nil {
		s.Close()
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, claimedGoal.ID, "rpc-goal-claim-owner"); err != nil {
		s.Close()
		t.Fatalf("ClaimGoal: %v", err)
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
	}
}

func TestGoalHandoffRoutesOverRPC(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]string{
		"handoff_id": "rpc-goal-handoff-1", "goal_id": fixture.claimedGoalID, "requested_by": "rpc-goal-requester",
		"request_report": "RPC goal request report",
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	if requested.GoalID != fixture.claimedGoalID || requested.RequestedAt == nil || requested.RequestedBy != "rpc-goal-requester" || requested.RequestReport != "RPC goal request report" {
		t.Fatalf("requested handoff = %#v, want goal, timestamp, requester, and report", requested)
	}

	var received store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.receive", map[string]string{
		"goal_id": fixture.claimedGoalID, "received_by": "rpc-goal-receiver",
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive by goal_id: %v", err)
	}
	if received.ID != requested.ID || received.ReceivedAt == nil || received.ReceivedBy != "rpc-goal-receiver" {
		t.Fatalf("received handoff = %#v, want request ID, timestamp, and receiver", received)
	}

	var completed store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.complete", map[string]string{
		"handoff_id": requested.ID, "goal_id": fixture.claimedGoalID, "complete_report": "RPC goal completion report",
	}, &completed); err != nil {
		t.Fatalf("goal.handoff.complete: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "RPC goal completion report" {
		t.Fatalf("completed handoff = %#v, want completion timestamp and report", completed)
	}

	var rejected store.GoalHandoff
	err := client.Call(ctx, "goal.handoff.request", map[string]string{
		"handoff_id": "rpc-goal-handoff-unclaimed", "goal_id": fixture.unclaimedGoalID, "requested_by": "rpc-goal-requester",
	}, &rejected)
	if err == nil {
		t.Fatalf("unclaimed goal handoff request succeeded: %#v", rejected)
	}
	if !strings.Contains(err.Error(), store.ErrGoalHandoffGoalUnclaimed.Error()) {
		t.Fatalf("unclaimed goal handoff request error = %q, want %q", err, store.ErrGoalHandoffGoalUnclaimed)
	}
}

func TestGoalHandoffCompleteByGoalOverRPC(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]string{
		"handoff_id": "rpc-complete-by-goal", "goal_id": fixture.claimedGoalID, "requested_by": "rpc-goal-requester",
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	var received store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.receive", map[string]string{
		"handoff_id": requested.ID, "goal_id": fixture.claimedGoalID, "received_by": "rpc-goal-receiver",
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}

	var completed store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.complete", map[string]string{
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

	for _, handoffID := range []string{"rpc-goal-complete-ambiguous-1", "rpc-goal-complete-ambiguous-2"} {
		var requested store.GoalHandoff
		if err := client.Call(ctx, "goal.handoff.request", map[string]string{
			"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "requested_by": "rpc-goal-requester",
		}, &requested); err != nil {
			t.Fatalf("goal.handoff.request(%q): %v", handoffID, err)
		}
		var received store.GoalHandoff
		if err := client.Call(ctx, "goal.handoff.receive", map[string]string{
			"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "received_by": "rpc-goal-receiver",
		}, &received); err != nil {
			t.Fatalf("goal.handoff.receive(%q): %v", handoffID, err)
		}
	}

	var completed store.GoalHandoff
	err := client.Call(ctx, "goal.handoff.complete", map[string]string{
		"goal_id": fixture.claimedGoalID,
	}, &completed)
	if err == nil {
		t.Fatalf("ambiguous goal handoff complete succeeded: %#v", completed)
	}
	if !strings.Contains(err.Error(), store.ErrGoalHandoffAmbiguous.Error()) {
		t.Fatalf("ambiguous goal handoff complete error = %q, want %q", err, store.ErrGoalHandoffAmbiguous)
	}
}

func TestGoalHandoffReceiveOverRPCRejectsAmbiguousPendingRequests(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	for _, handoffID := range []string{"rpc-goal-ambiguous-1", "rpc-goal-ambiguous-2"} {
		var requested store.GoalHandoff
		if err := client.Call(ctx, "goal.handoff.request", map[string]string{
			"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "requested_by": "rpc-goal-requester",
		}, &requested); err != nil {
			t.Fatalf("goal.handoff.request(%q): %v", handoffID, err)
		}
	}

	var received store.GoalHandoff
	err := client.Call(ctx, "goal.handoff.receive", map[string]string{
		"goal_id": fixture.claimedGoalID, "received_by": "rpc-goal-receiver",
	}, &received)
	if err == nil {
		t.Fatalf("ambiguous goal handoff receive succeeded: %#v", received)
	}
	if !strings.Contains(err.Error(), store.ErrGoalHandoffAmbiguous.Error()) {
		t.Fatalf("ambiguous goal handoff receive error = %q, want %q", err, store.ErrGoalHandoffAmbiguous)
	}
}
