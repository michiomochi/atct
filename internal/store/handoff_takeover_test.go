package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// Receiving a handoff is what gives a session its scope, and MonitorAssignment
// reads that scope from received_by. A second session receiving the same
// handoff therefore does not just duplicate a record: it silently strips the
// first session of its role, which then fails every ATCT call it makes and is
// never told why.
func TestGoalHandoffCannotBeTakenFromItsReceiver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "takeover-requester")
	addTestAgentSession(t, s, "takeover-receiver")
	addTestAgentSession(t, s, "takeover-intruder")

	const handoffID = "goal-handoff-takeover"
	if _, err := s.RequestGoalHandoff(ctx, handoffID, goalID, testSessionID("takeover-requester"), ""); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	receiver := testSessionID("takeover-receiver")
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, receiver); err != nil {
		t.Fatalf("ReceiveGoalHandoff(receiver): %v", err)
	}

	_, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, testSessionID("takeover-intruder"))
	if err == nil {
		t.Fatal("a second session received a handoff that was already received")
	}
	// "not found" sent the caller looking for a handoff that was in front of
	// them the whole time.
	if !errors.Is(err, ErrGoalHandoffLiveReceiver) {
		t.Fatalf("refusal = %v, want it to name the live receiver", err)
	}

	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetGoalHandoff: %v", err)
	}
	if handoff.ReceivedBy != receiver {
		t.Fatalf("received_by = %d, want the original receiver %d", handoff.ReceivedBy, receiver)
	}
}

// The receiver may say it again. An agent that lost its context re-reads the
// handoff id from its launch message and receives once more; refusing that
// would strand it with no way back to its own work.
func TestGoalHandoffReceiverCanReceiveAgain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "retry-requester")
	addTestAgentSession(t, s, "retry-receiver")

	const handoffID = "goal-handoff-retry"
	if _, err := s.RequestGoalHandoff(ctx, handoffID, goalID, testSessionID("retry-requester"), ""); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	receiver := testSessionID("retry-receiver")
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, receiver); err != nil {
		t.Fatalf("ReceiveGoalHandoff(first): %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, receiver); err != nil {
		t.Fatalf("ReceiveGoalHandoff(same receiver again): %v", err)
	}
}

func TestTaskHandoffCannotBeTakenFromItsReceiver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "task-takeover-requester")
	addTestAgentSession(t, s, "task-takeover-receiver")
	addTestAgentSession(t, s, "task-takeover-intruder")

	const handoffID = "task-handoff-takeover"
	if _, err := s.RequestTaskHandoff(ctx, handoffID, taskID, testSessionID("task-takeover-requester"), ""); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	receiver := testSessionID("task-takeover-receiver")
	if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, receiver); err != nil {
		t.Fatalf("ReceiveTaskHandoff(receiver): %v", err)
	}

	_, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, testSessionID("task-takeover-intruder"))
	if err == nil {
		t.Fatal("a second session received a task handoff that was already received")
	}
	if !errors.Is(err, ErrTaskHandoffLiveReceiver) {
		t.Fatalf("refusal = %v, want it to name the live receiver", err)
	}

	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetTaskHandoff: %v", err)
	}
	if handoff.ReceivedBy != receiver {
		t.Fatalf("received_by = %d, want the original receiver %d", handoff.ReceivedBy, receiver)
	}
}

func TestTaskHandoffReceiverCanReceiveAgain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "task-retry-requester")
	addTestAgentSession(t, s, "task-retry-receiver")

	const handoffID = "task-handoff-retry"
	if _, err := s.RequestTaskHandoff(ctx, handoffID, taskID, testSessionID("task-retry-requester"), ""); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	receiver := testSessionID("task-retry-receiver")
	if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, receiver); err != nil {
		t.Fatalf("ReceiveTaskHandoff(first): %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, receiver); err != nil {
		t.Fatalf("ReceiveTaskHandoff(same receiver again): %v", err)
	}
}

// Holding the project's claim is what makes a session its commander. Requiring
// the session's own project column to agree was a second copy of the same
// fact, and it refused the holder whenever that column was not filled in.
func TestSessionDiscardAcceptsTheClaimHolder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	commander := registerNamedTestAgentSession(t, s, "discard-commander", os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, commander, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimProject(ctx, goal.ProjectID, commander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	target := registerNamedTestAgentSession(t, s, "discard-target", os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, target, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject(target): %v", err)
	}

	if _, err := s.RequestSessionDiscard(ctx, SessionDiscardRequest{
		ProjectID: goal.ProjectID, GoalID: goalID, TargetSessionID: target,
		RequestedBy: commander, Reason: "the pane is gone",
	}); err != nil {
		t.Fatalf("RequestSessionDiscard by the claim holder: %v", err)
	}
}

// A session with no project never identified from inside one, so it is in no
// position to revoke another.
func TestSessionDiscardRefusesASessionWithNoProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	unbound := registerNamedTestAgentSession(t, s, "discard-unbound", os.Getpid())
	if _, err := s.ClaimProject(ctx, goal.ProjectID, unbound); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	target := registerNamedTestAgentSession(t, s, "discard-unbound-target", os.Getpid())

	_, err = s.RequestSessionDiscard(ctx, SessionDiscardRequest{
		ProjectID: goal.ProjectID, GoalID: goalID, TargetSessionID: target,
		RequestedBy: unbound, Reason: "no project",
	})
	if !errors.Is(err, ErrSessionDiscardForbidden) {
		t.Fatalf("RequestSessionDiscard error = %v, want ErrSessionDiscardForbidden", err)
	}
	if !strings.Contains(err.Error(), "has no project") {
		t.Fatalf("error %q does not say the session has no project", err)
	}
}

// A handoff must not be taken off a receiver that is still working, and must
// be takeable from one that stopped: the delegator's only other move is to
// recover it, which needs a commander that may not be the one retrying.
func TestGoalHandoffCanBeTakenFromAStaleReceiver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "stale-takeover-requester")
	addTestAgentSession(t, s, "stale-takeover-receiver")
	addTestAgentSession(t, s, "stale-takeover-successor")

	const handoffID = "goal-handoff-stale-takeover"
	if _, err := s.RequestGoalHandoff(ctx, handoffID, goalID, testSessionID("stale-takeover-requester"), ""); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	receiver := testSessionID("stale-takeover-receiver")
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, receiver); err != nil {
		t.Fatalf("ReceiveGoalHandoff(receiver): %v", err)
	}

	// While it is running, nobody else may take it.
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, testSessionID("stale-takeover-successor")); err == nil {
		t.Fatal("a second session took a handoff from a running receiver")
	}

	// Its monitor stops; the pane is gone.
	expireTestAgentSessionLease(t, s, receiver)

	successor := testSessionID("stale-takeover-successor")
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, successor); err != nil {
		t.Fatalf("ReceiveGoalHandoff(successor) after the receiver went stale: %v", err)
	}
	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetGoalHandoff: %v", err)
	}
	if handoff.ReceivedBy != successor {
		t.Fatalf("received_by = %d, want the successor %d", handoff.ReceivedBy, successor)
	}
}

func TestTaskHandoffCanBeTakenFromAStaleReceiver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	addLiveParentGoalClaim(t, s, taskID, "task-stale-requester")
	addTestAgentSession(t, s, "task-stale-receiver")
	addTestAgentSession(t, s, "task-stale-successor")

	const handoffID = "task-handoff-stale-takeover"
	if _, err := s.RequestTaskHandoff(ctx, handoffID, taskID, testSessionID("task-stale-requester"), ""); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	receiver := testSessionID("task-stale-receiver")
	if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, receiver); err != nil {
		t.Fatalf("ReceiveTaskHandoff(receiver): %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, testSessionID("task-stale-successor")); err == nil {
		t.Fatal("a second session took a task handoff from a running receiver")
	}

	expireTestAgentSessionLease(t, s, receiver)

	if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, testSessionID("task-stale-successor")); err != nil {
		t.Fatalf("ReceiveTaskHandoff(successor) after the receiver went stale: %v", err)
	}
}
