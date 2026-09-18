package daemon

import (
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

// These tests tie doc/continuous-execution.md to the flow in
// doc/execution-flow.md: as the flow advances, the notifications that keep a
// role moving must actually be produced, and must stop once the state clears.

func (f *flowFixture) evaluateWakeups(at time.Time) []store.DecisionEvent {
	f.t.Helper()
	events, err := newWakeupTracker(time.Time{}).evaluate(f.ctx, f.store, at)
	if err != nil {
		f.t.Fatalf("evaluate: %v", err)
	}
	return events
}

func hasWakeup(events []store.DecisionEvent, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

// A task handoff that is requested but never received must surface, otherwise
// a subcommander waits forever on an executor that never started.
func TestContinuousExecutionUnreceivedTaskHandoffWakesTheParent(t *testing.T) {
	f := newFlowFixture(t)
	goal := f.newGoal("executor never receives")
	f.throughPlan("gh-1", goal.ID)
	tasks := f.createTasks(goal.ID, "only task")

	f.call("task.handoff.request", taskHandoffRequestParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, RequestedBy: f.subcommanderID,
		RequestReport: "implement it",
	})

	now := time.Now().UTC()
	if events := f.evaluateWakeups(now); hasWakeup(events, store.EventWakeupHandoffUnreceived) {
		t.Fatal("handoff_unreceived fired immediately; doc requires it to wait 30 minutes")
	}

	late := now.Add(31 * time.Minute)
	if events := f.evaluateWakeups(late); !hasWakeup(events, store.EventWakeupHandoffUnreceived) {
		t.Fatalf("no handoff_unreceived wakeup after 31 minutes; a stalled delegation stays invisible")
	}

	// Receiving it clears the condition.
	f.call("task.handoff.receive", taskHandoffReceiveParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, ReceivedBy: f.executorID,
	})
	if events := f.evaluateWakeups(late); hasWakeup(events, store.EventWakeupHandoffUnreceived) {
		t.Fatal("handoff_unreceived still fires after the handoff was received")
	}
}

// doc/continuous-execution.md: monitor_lost reaches the parent role once the
// monitor health lease expires, so the parent can recover the handoff.
func TestContinuousExecutionMonitorLossReachesTheParent(t *testing.T) {
	f := newFlowFixture(t)
	goal := f.newGoal("executor monitor dies")
	f.throughPlan("gh-1", goal.ID)
	tasks := f.createTasks(goal.ID, "only task")
	f.call("task.handoff.request", taskHandoffRequestParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, RequestedBy: f.subcommanderID,
		RequestReport: "implement it",
	})
	f.call("task.handoff.receive", taskHandoffReceiveParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, ReceivedBy: f.executorID,
	})

	start := time.Now().UTC().Truncate(time.Second)
	health := store.MonitorHealth{
		CWD: t.TempDir(), Role: "executor", State: "healthy",
		ProjectID: f.project.ID, GoalID: &goal.ID, TaskID: &tasks[0].ID,
		PID: 4321, ProcessStartedAt: start.Add(-2 * time.Minute),
		TransitionedAt: start, LastSeenAt: start,
	}
	health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
	if err := f.store.UpsertMonitorHealth(f.ctx, health); err != nil {
		t.Fatalf("UpsertMonitorHealth: %v", err)
	}

	if events := f.evaluateWakeups(start.Add(store.MonitorHealthLease / 2)); hasWakeup(events, store.EventWakeupMonitorLost) {
		t.Fatal("monitor_lost fired while the lease was still valid")
	}

	if err := f.store.StopMonitorHealth(f.ctx, health.MonitorID, start); err != nil {
		t.Fatalf("StopMonitorHealth: %v", err)
	}
	events := f.evaluateWakeups(start.Add(store.MonitorHealthLease + time.Second))
	if !hasWakeup(events, store.EventWakeupMonitorLost) {
		t.Fatalf("no monitor_lost after the lease expired; a dead executor monitor stays invisible")
	}
	wakeup, ok := findWakeupEvent(events, store.EventWakeupMonitorLost, goal.ID)
	if !ok {
		t.Fatalf("monitor_lost did not carry goal %d", goal.ID)
	}
	if wakeup.TaskID != tasks[0].ID {
		t.Fatalf("monitor_lost task = %d, want %d; the parent cannot tell which worker died",
			wakeup.TaskID, tasks[0].ID)
	}
}
