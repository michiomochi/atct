package store

import (
	"context"
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

	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, testSessionID("takeover-intruder")); err == nil {
		t.Fatal("a second session received a handoff that was already received")
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

	if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, testSessionID("task-takeover-intruder")); err == nil {
		t.Fatal("a second session received a task handoff that was already received")
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
