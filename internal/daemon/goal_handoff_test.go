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
	if _, err := s.UpdateGoalRequestReport(ctx, claimedGoal.ID, "# Spec", "# Plan"); err != nil {
		s.Close()
		t.Fatalf("UpdateGoalRequestReport claimed: %v", err)
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

	var received handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive by goal_id: %v", err)
	}
	var receivedData store.GoalHandoff
	if err := json.Unmarshal(received.Data, &receivedData); err != nil {
		t.Fatalf("decode goal.handoff.receive data: %v", err)
	}
	if receivedData.ID != requested.ID || receivedData.ReceivedAt == nil || receivedData.ReceivedBy != fixture.receiverID {
		t.Fatalf("received handoff = %#v, want request ID, timestamp, and receiver", receivedData)
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

func TestDeriveSessionRoleRestoresSubcommanderAfterCompletionRejection(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	completion := prepareCompletedGoalHandoffCompletion(t, fixture, "role-rejection-handoff")
	ctx := context.Background()

	if err := fixture.store.RejectCompletion(ctx, completion.ID, "Please revise the completion report"); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}

	assignment, err := New(fixture.store).deriveSessionRole(ctx, fixture.receiverID)
	if err != nil {
		t.Fatalf("deriveSessionRole after rejection: %v", err)
	}
	if assignment.Role != "subcommander" || assignment.GoalID != fixture.claimedGoalID {
		t.Fatalf("role assignment after rejection = %+v, want subcommander for goal %d", assignment, fixture.claimedGoalID)
	}
}

func TestDeriveSessionRoleRemainsExecutorAfterCompletionApproval(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	completion := prepareCompletedGoalHandoffCompletion(t, fixture, "role-approval-handoff")
	ctx := context.Background()

	if _, err := fixture.store.ApproveCompletion(ctx, completion.ID); err != nil {
		t.Fatalf("ApproveCompletion: %v", err)
	}

	assignment, err := New(fixture.store).deriveSessionRole(ctx, fixture.receiverID)
	if err != nil {
		t.Fatalf("deriveSessionRole after approval: %v", err)
	}
	if assignment.Role != "executor" || assignment.GoalID != 0 {
		t.Fatalf("role assignment after approval = %+v, want executor without a goal", assignment)
	}
}

func prepareCompletedGoalHandoffCompletion(t *testing.T, fixture goalHandoffRPCTestFixture, handoffID string) domain.Decision {
	t.Helper()
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}

	var received handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"handoff_id": requested.ID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}

	var completed store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.complete", map[string]any{
		"handoff_id": requested.ID, "goal_id": fixture.claimedGoalID, "complete_report": "Role test handoff completion report",
	}, &completed); err != nil {
		t.Fatalf("goal.handoff.complete: %v", err)
	}
	if completed.CompletedReportAt == nil {
		t.Fatalf("completed handoff = %#v, want completion timestamp", completed)
	}

	completion, err := fixture.store.CompleteGoalWithReport(ctx, fixture.claimedGoalID, domain.CompletionReport{
		WorkDone:    "Role test completion work",
		NowPossible: "Role test completion possibility",
		HowToVerify: "Role test completion verification",
		Surprises:   "Role test completion surprises",
		NeedsReview: "Role test completion review",
		NextSteps:   "Role test completion next steps",
	}, fixture.receiverID)
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	return completion
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
	var received handoffReceiveResponse
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

	var received handoffReceiveResponse
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

func TestNamedGoalAndPlanHandoffReviewRoutesReturnRoleEvidence(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	const goalHandoffID = "named-goal-review"
	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
		"request_report": "named goal request report",
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	var received handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}
	if received.Role != "subcommander" || received.ClaimEvidence.Scope != "goal" || received.ClaimEvidence.GoalID != fixture.claimedGoalID || received.ClaimEvidence.HandoffID != goalHandoffID || received.ClaimEvidence.AgentSessionID != fixture.receiverID {
		t.Fatalf("goal receive role/evidence = %+v, want subcommander goal evidence", received)
	}

	var goalReviewRequested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.review.request", map[string]any{
		"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID,
		"review_request_report": "named goal review report",
	}, &goalReviewRequested); err != nil {
		t.Fatalf("goal.handoff.review.request: %v", err)
	}
	if goalReviewRequested.ReviewRequestedBy != fixture.receiverID || goalReviewRequested.ReviewRequestedAt == nil {
		t.Fatalf("goal review request = %#v, want review request metadata", goalReviewRequested)
	}

	var goalReviewReceived handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.review.receive", map[string]any{
		"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID,
	}, &goalReviewReceived); err != nil {
		t.Fatalf("goal.handoff.review.receive: %v", err)
	}
	if goalReviewReceived.Role != "commander" || goalReviewReceived.ClaimEvidence.Scope != "project" || goalReviewReceived.ClaimEvidence.HandoffID != goalHandoffID {
		t.Fatalf("goal review receive role/evidence = %+v, want commander project evidence", goalReviewReceived)
	}

	const planHandoffID = "named-plan-review"
	var planRequested store.PlanHandoff
	if err := client.Call(ctx, "plan.handoff.review.request", map[string]any{
		"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID,
		"review_request_report": "named plan review report",
	}, &planRequested); err != nil {
		t.Fatalf("plan.handoff.review.request: %v", err)
	}
	if planRequested.ID != planHandoffID || planRequested.GoalID != fixture.claimedGoalID || planRequested.ReviewRequestedBy != fixture.receiverID {
		t.Fatalf("plan handoff = %#v, want goal-only review handoff", planRequested)
	}

	var planReviewReceived handoffReceiveResponse
	if err := client.Call(ctx, "plan.handoff.review.receive", map[string]any{
		"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID,
	}, &planReviewReceived); err != nil {
		t.Fatalf("plan.handoff.review.receive: %v", err)
	}
	if planReviewReceived.Role != "commander" || planReviewReceived.ClaimEvidence.Scope != "project" || planReviewReceived.ClaimEvidence.GoalID != fixture.claimedGoalID || planReviewReceived.ClaimEvidence.TaskID != 0 || planReviewReceived.ClaimEvidence.HandoffID != planHandoffID {
		t.Fatalf("plan review receive role/evidence = %+v, want commander goal-only evidence", planReviewReceived)
	}

	var planCompleted store.PlanHandoff
	if err := client.Call(ctx, "plan.handoff.complete", map[string]any{
		"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "agent_session_id": fixture.requesterID,
		"complete_report": "named plan completion report",
	}, &planCompleted); err != nil {
		t.Fatalf("plan.handoff.complete: %v", err)
	}
	if planCompleted.CompletedReportAt == nil || planCompleted.CompleteReport != "named plan completion report" {
		t.Fatalf("completed plan handoff = %#v, want reviewer completion", planCompleted)
	}

	var goalCompleted store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.complete", map[string]any{
		"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "agent_session_id": fixture.requesterID,
		"complete_report": "named goal completion report",
	}, &goalCompleted); err != nil {
		t.Fatalf("goal.handoff.complete: %v", err)
	}
	if goalCompleted.CompletedReportAt == nil || goalCompleted.CompleteReport != "named goal completion report" {
		t.Fatalf("completed goal handoff = %#v, want reviewer completion", goalCompleted)
	}
}

func TestNamedGoalAndPlanHandoffReviewRejectReceiveRetriesOverRPC(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	const goalHandoffID = "named-goal-review-reject-receive"
	var goal store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID}, &goal); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	var received handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID}, &received); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}
	if err := client.Call(ctx, "goal.handoff.review.request", map[string]any{"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID, "review_request_report": "ready"}, &goal); err != nil {
		t.Fatalf("goal.handoff.review.request: %v", err)
	}
	if err := client.Call(ctx, "goal.handoff.review.receive", map[string]any{"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID}, &received); err != nil {
		t.Fatalf("goal.handoff.review.receive: %v", err)
	}
	if err := client.Call(ctx, "goal.handoff.review.reject", map[string]any{"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "reviewer_id": fixture.requesterID, "reject_report": "revise"}, &goal); err != nil {
		t.Fatalf("goal.handoff.review.reject: %v", err)
	}
	if err := client.Call(ctx, "goal.handoff.review.reject.receive", map[string]any{"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID}, &goal); err == nil {
		t.Fatal("goal.handoff.review.reject.receive accepted the reviewer")
	}
	if err := client.Call(ctx, "goal.handoff.review.reject.receive", map[string]any{"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID}, &goal); err != nil {
		t.Fatalf("goal.handoff.review.reject.receive: %v", err)
	}
	if goal.ReviewRejectionReceivedBy != fixture.receiverID || goal.ReviewRejectionReceivedAt == nil {
		t.Fatalf("goal rejection receipt = %+v, want receiver and timestamp", goal)
	}
	if err := client.Call(ctx, "goal.handoff.review.request", map[string]any{"handoff_id": goalHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID, "review_request_report": "revised"}, &goal); err != nil {
		t.Fatalf("retry goal.handoff.review.request: %v", err)
	}

	const planHandoffID = "named-plan-review-reject-receive"
	var plan store.PlanHandoff
	if err := client.Call(ctx, "plan.handoff.review.request", map[string]any{"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID, "review_request_report": "ready"}, &plan); err != nil {
		t.Fatalf("plan.handoff.review.request: %v", err)
	}
	if err := client.Call(ctx, "plan.handoff.review.receive", map[string]any{"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID}, &received); err != nil {
		t.Fatalf("plan.handoff.review.receive: %v", err)
	}
	if err := client.Call(ctx, "plan.handoff.review.reject", map[string]any{"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "reviewer_id": fixture.requesterID, "reject_report": "revise"}, &plan); err != nil {
		t.Fatalf("plan.handoff.review.reject: %v", err)
	}
	if err := client.Call(ctx, "plan.handoff.review.reject.receive", map[string]any{"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID}, &plan); err == nil {
		t.Fatal("plan.handoff.review.reject.receive accepted the reviewer")
	}
	if err := client.Call(ctx, "plan.handoff.review.reject.receive", map[string]any{"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID}, &plan); err != nil {
		t.Fatalf("plan.handoff.review.reject.receive: %v", err)
	}
	if plan.ReviewRejectionReceivedBy != fixture.receiverID || plan.ReviewRejectionReceivedAt == nil {
		t.Fatalf("plan rejection receipt = %+v, want receiver and timestamp", plan)
	}
	if err := client.Call(ctx, "plan.handoff.review.request", map[string]any{"handoff_id": planHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID, "review_request_report": "revised"}, &plan); err != nil {
		t.Fatalf("retry plan.handoff.review.request: %v", err)
	}
}

func registerGoalHandoffRPCTestSession(t *testing.T, fixture goalHandoffRPCTestFixture) int64 {
	t.Helper()

	sessionID, err := fixture.store.RegisterAgentSession(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	return sessionID
}

func rotateGoalHandoffRPCTestProjectClaim(t *testing.T, fixture goalHandoffRPCTestFixture) int64 {
	t.Helper()

	ctx := context.Background()
	goal, err := fixture.store.GetGoal(ctx, fixture.claimedGoalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if err := fixture.store.ReleaseProject(ctx, goal.ProjectID); err != nil {
		t.Fatalf("ReleaseProject: %v", err)
	}
	currentCommanderID := registerGoalHandoffRPCTestSession(t, fixture)
	if err := fixture.store.AssociateAgentSessionWithProject(ctx, currentCommanderID, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := fixture.store.ClaimProject(ctx, goal.ProjectID, currentCommanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	return currentCommanderID
}

func claimGoalHandoffRPCTestForeignProject(t *testing.T, fixture goalHandoffRPCTestFixture) int64 {
	t.Helper()

	ctx := context.Background()
	goal, err := fixture.store.GetGoal(ctx, fixture.unclaimedGoalID)
	if err != nil {
		t.Fatalf("GetGoal foreign: %v", err)
	}
	foreignCommanderID := registerGoalHandoffRPCTestSession(t, fixture)
	if err := fixture.store.AssociateAgentSessionWithProject(ctx, foreignCommanderID, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject foreign: %v", err)
	}
	if _, err := fixture.store.ClaimProject(ctx, goal.ProjectID, foreignCommanderID); err != nil {
		t.Fatalf("ClaimProject foreign: %v", err)
	}
	return foreignCommanderID
}

func TestNamedGoalHandoffReviewReceiveRecoveryRoutesOverRPC(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, fixture goalHandoffRPCTestFixture) int64
		wantError bool
	}{
		{
			name: "normal requester success",
			setup: func(_ *testing.T, fixture goalHandoffRPCTestFixture) int64 {
				return fixture.requesterID
			},
		},
		{
			name: "live requester rejects another caller",
			setup: func(t *testing.T, fixture goalHandoffRPCTestFixture) int64 {
				return registerGoalHandoffRPCTestSession(t, fixture)
			},
			wantError: true,
		},
		{
			name: "current commander succeeds after claim turnover",
			setup: func(t *testing.T, fixture goalHandoffRPCTestFixture) int64 {
				return rotateGoalHandoffRPCTestProjectClaim(t, fixture)
			},
		},
		{
			name: "turnover rejects noncommander",
			setup: func(t *testing.T, fixture goalHandoffRPCTestFixture) int64 {
				rotateGoalHandoffRPCTestProjectClaim(t, fixture)
				return registerGoalHandoffRPCTestSession(t, fixture)
			},
			wantError: true,
		},
		{
			name: "foreign project commander is rejected",
			setup: func(t *testing.T, fixture goalHandoffRPCTestFixture) int64 {
				return claimGoalHandoffRPCTestForeignProject(t, fixture)
			},
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newGoalHandoffRPCTestFixture(t)
			client := mcpshim.NewClient(fixture.socketPath)
			ctx := context.Background()
			handoffID := "rpc-goal-review-recovery-" + strings.ReplaceAll(tc.name, " ", "-")

			var requested store.GoalHandoff
			if err := client.Call(ctx, "goal.handoff.request", map[string]any{
				"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
			}, &requested); err != nil {
				t.Fatalf("goal.handoff.request: %v", err)
			}
			var received handoffReceiveResponse
			if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
				"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
			}, &received); err != nil {
				t.Fatalf("goal.handoff.receive: %v", err)
			}
			var reviewRequested store.GoalHandoff
			if err := client.Call(ctx, "goal.handoff.review.request", map[string]any{
				"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID,
				"review_request_report": "RPC recovery review request",
			}, &reviewRequested); err != nil {
				t.Fatalf("goal.handoff.review.request: %v", err)
			}

			before, err := fixture.store.GetGoalHandoff(ctx, handoffID)
			if err != nil {
				t.Fatalf("GetGoalHandoff before review receive: %v", err)
			}
			callerID := tc.setup(t, fixture)
			var response handoffReceiveResponse
			err = client.Call(ctx, "goal.handoff.review.receive", map[string]any{
				"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "received_by": callerID,
			}, &response)
			if tc.wantError {
				if err == nil {
					t.Fatalf("goal.handoff.review.receive unexpectedly succeeded: %#v", response)
				}
				if !strings.Contains(err.Error(), store.ErrGoalHandoffReviewReviewerMismatch.Error()) {
					t.Fatalf("goal.handoff.review.receive error = %v, want %v", err, store.ErrGoalHandoffReviewReviewerMismatch)
				}
				after, getErr := fixture.store.GetGoalHandoff(ctx, handoffID)
				if getErr != nil {
					t.Fatalf("GetGoalHandoff after rejected review receive: %v", getErr)
				}
				if after.ReviewRequestedBy != before.ReviewRequestedBy || after.ReviewRequestReport != before.ReviewRequestReport || before.ReviewRequestedAt == nil || after.ReviewRequestedAt == nil || !after.ReviewRequestedAt.Equal(*before.ReviewRequestedAt) || after.ReviewReceivedBy != before.ReviewReceivedBy || after.ReviewReceivedAt != before.ReviewReceivedAt {
					t.Fatalf("rejected review receive changed review fields: before=%+v after=%+v", before, after)
				}
				return
			}

			if err != nil {
				t.Fatalf("goal.handoff.review.receive: %v", err)
			}
			goal, err := fixture.store.GetGoal(ctx, fixture.claimedGoalID)
			if err != nil {
				t.Fatalf("GetGoal: %v", err)
			}
			if response.Role != "commander" || response.ClaimEvidence.Scope != "project" || response.ClaimEvidence.AgentSessionID != callerID || response.ClaimEvidence.ProjectID != goal.ProjectID || response.ClaimEvidence.GoalID != fixture.claimedGoalID || response.ClaimEvidence.TaskID != 0 || response.ClaimEvidence.HandoffID != handoffID {
				t.Fatalf("goal review receive role/evidence = %+v, want commander project evidence for project %d", response, goal.ProjectID)
			}
			var receivedData store.GoalHandoff
			if err := json.Unmarshal(response.Data, &receivedData); err != nil {
				t.Fatalf("decode goal.handoff.review.receive data: %v", err)
			}
			if receivedData.ReviewReceivedBy != callerID || receivedData.ReviewReceivedAt == nil {
				t.Fatalf("goal review receive data = %+v, want persisted reviewer %d", receivedData, callerID)
			}
			persisted, err := fixture.store.GetGoalHandoff(ctx, handoffID)
			if err != nil {
				t.Fatalf("GetGoalHandoff after review receive: %v", err)
			}
			if persisted.ReviewReceivedBy != callerID || persisted.ReviewReceivedAt == nil {
				t.Fatalf("persisted goal review receive = %+v, want reviewer %d", persisted, callerID)
			}
		})
	}
}

func TestNamedGoalReviewRequiresCommanderAndHumanApprovalOrdering(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	const handoffID = "named-goal-final-review"
	var requested store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
	}, &requested); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	var received handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &received); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}
	if received.Role != "subcommander" || received.ClaimEvidence.AgentSessionID != fixture.receiverID {
		t.Fatalf("goal handoff receive = %+v, want subcommander evidence for receiver %d", received, fixture.receiverID)
	}
	var handoffReview store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID,
		"review_request_report": "receiver reviewed the delegated goal",
	}, &handoffReview); err != nil {
		t.Fatalf("goal.handoff.review.request: %v", err)
	}
	var handoffReviewReceived handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.review.receive", map[string]any{
		"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID,
	}, &handoffReviewReceived); err != nil {
		t.Fatalf("goal.handoff.review.receive: %v", err)
	}
	var completedHandoff store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.complete", map[string]any{
		"handoff_id": handoffID, "goal_id": fixture.claimedGoalID, "agent_session_id": fixture.requesterID,
		"complete_report": "commander accepted the reviewed goal handoff",
	}, &completedHandoff); err != nil {
		t.Fatalf("goal.handoff.complete: %v", err)
	}
	report := domain.CompletionReport{
		WorkDone:    "commander-approved work",
		NowPossible: "commander-approved result",
		HowToVerify: "run focused integration tests",
		Surprises:   "none",
		NeedsReview: "none",
		NextSteps:   "merge",
	}

	var review domain.Decision
	if err := client.Call(ctx, "goal.review.request", map[string]any{
		"goal_id": fixture.claimedGoalID, "agent_session_id": fixture.requesterID,
		"work_done": report.WorkDone, "now_possible": report.NowPossible,
		"how_to_verify": report.HowToVerify, "surprises": report.Surprises,
		"needs_review": report.NeedsReview, "next_steps": report.NextSteps,
	}, &review); err != nil {
		t.Fatalf("goal.review.request: %v", err)
	}
	if review.Kind != domain.KindGoalReview || review.Status != domain.DecisionOpen || review.TaskID != 0 {
		t.Fatalf("goal review = %+v, want open taskless human review", review)
	}

	completeParams := map[string]any{
		"goal_id":          fixture.claimedGoalID,
		"agent_session_id": fixture.requesterID,
	}
	var beforeApproval domain.Goal
	if err := client.Call(ctx, "goal.review.complete", completeParams, &beforeApproval); err == nil {
		t.Fatalf("goal.review.complete before human approval succeeded: %+v", beforeApproval)
	} else if !strings.Contains(err.Error(), "open decision") {
		t.Fatalf("goal.review.complete before human approval error = %v, want open-decision denial", err)
	}
	before, err := fixture.store.GetGoal(ctx, fixture.claimedGoalID)
	if err != nil {
		t.Fatalf("GetGoal before human approval: %v", err)
	}
	if before.Status != domain.GoalActive {
		t.Fatalf("goal before human approval = %q, want active", before.Status)
	}
	assertGoalCompletionReport(t, before, report)

	if err := fixture.store.RejectGoalReview(ctx, review.ID, "needs another review"); err != nil {
		t.Fatalf("RejectGoalReview: %v", err)
	}
	var afterReject domain.Goal
	if err := client.Call(ctx, "goal.review.complete", completeParams, &afterReject); err == nil {
		t.Fatalf("goal.review.complete after human rejection succeeded: %+v", afterReject)
	} else if !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("goal.review.complete after human rejection error = %v, want not-approved denial", err)
	}

	const retryHandoffID = "named-goal-final-review-retry-handoff"
	var retryHandoff store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{
		"handoff_id": retryHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID,
		"request_report": "commander delegated the revised goal",
	}, &retryHandoff); err != nil {
		t.Fatalf("retry goal.handoff.request: %v", err)
	}
	var retryReceived handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{
		"handoff_id": retryHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID,
	}, &retryReceived); err != nil {
		t.Fatalf("retry goal.handoff.receive: %v", err)
	}
	var retryHandoffReview store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.review.request", map[string]any{
		"handoff_id": retryHandoffID, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID,
		"review_request_report": "receiver reviewed the revised goal",
	}, &retryHandoffReview); err != nil {
		t.Fatalf("retry goal.handoff.review.request: %v", err)
	}
	var retryHandoffReviewReceived handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.review.receive", map[string]any{
		"handoff_id": retryHandoffID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID,
	}, &retryHandoffReviewReceived); err != nil {
		t.Fatalf("retry goal.handoff.review.receive: %v", err)
	}
	var retryCompletedHandoff store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.complete", map[string]any{
		"handoff_id": retryHandoffID, "goal_id": fixture.claimedGoalID, "agent_session_id": fixture.requesterID,
		"complete_report": "commander accepted the revised goal handoff",
	}, &retryCompletedHandoff); err != nil {
		t.Fatalf("retry goal.handoff.complete: %v", err)
	}

	var retryReview domain.Decision
	if err := client.Call(ctx, "goal.review.request", map[string]any{
		"goal_id": fixture.claimedGoalID, "agent_session_id": fixture.requesterID,
		"work_done": report.WorkDone, "now_possible": report.NowPossible,
		"how_to_verify": report.HowToVerify, "surprises": report.Surprises,
		"needs_review": report.NeedsReview, "next_steps": report.NextSteps,
	}, &retryReview); err != nil {
		t.Fatalf("retry goal.review.request: %v", err)
	}
	if retryReview.ID == review.ID || retryReview.Kind != domain.KindGoalReview || retryReview.Status != domain.DecisionOpen {
		t.Fatalf("retry goal review = %+v, want a new open goal review", retryReview)
	}
	if _, err := fixture.store.ApproveGoalReview(ctx, retryReview.ID); err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}
	approvedGoal, err := fixture.store.GetGoal(ctx, fixture.claimedGoalID)
	if err != nil {
		t.Fatalf("GetGoal after human approval: %v", err)
	}
	if approvedGoal.Status != domain.GoalActive {
		t.Fatalf("goal after human approval = %q, want active until commander finalization", approvedGoal.Status)
	}

	subcommanderParams := map[string]any{"goal_id": fixture.claimedGoalID}
	subcommanderParams["agent_session_id"] = fixture.receiverID
	var subcommanderDone domain.Goal
	if err := client.Call(ctx, "goal.review.complete", subcommanderParams, &subcommanderDone); err == nil {
		t.Fatalf("subcommander goal.review.complete succeeded: %+v", subcommanderDone)
	} else if !strings.Contains(err.Error(), "commander") {
		t.Fatalf("subcommander goal.review.complete error = %v, want commander-only denial", err)
	}

	var done domain.Goal
	if err := client.Call(ctx, "goal.review.complete", completeParams, &done); err != nil {
		t.Fatalf("commander goal.review.complete after human approval: %v", err)
	}
	if done.Status != domain.GoalDone || done.WorkDone != report.WorkDone || done.NowPossible != report.NowPossible || done.HowToVerify != report.HowToVerify || done.Surprises != report.Surprises || done.NeedsReview != report.NeedsReview || done.NextSteps != report.NextSteps {
		t.Fatalf("completed goal = %+v, want final report after commander completion", done)
	}
}
