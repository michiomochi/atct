package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

func newWakeupTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newWakeupTestGoal(t *testing.T, s *store.Store, key string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct-"+key, filepath.Join(t.TempDir(), key))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Resume "+key, "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	return project.ID, goal.ID
}

func callRunMaintenanceWith(t *testing.T, d *Daemon, ctx context.Context, tracker *wakeupTracker, now time.Time, detect func(context.Context, int64) (store.WakeupState, error)) {
	t.Helper()
	d.runMaintenanceWith(ctx, tracker, now, detect)
}

func receiveWakeupEvent(t *testing.T, ch <-chan store.DecisionEvent) store.DecisionEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for maintenance event")
		return store.DecisionEvent{}
	}
}

func decodeEvaluateFailure(t *testing.T, event store.DecisionEvent) (string, string) {
	t.Helper()
	if event.Name != "wakeup.evaluate_failed" {
		t.Fatalf("event name = %q, want wakeup.evaluate_failed", event.Name)
	}
	payload, err := json.Marshal(event.Data)
	if err != nil {
		t.Fatalf("marshal evaluate failure: %v", err)
	}
	var failure struct {
		WakeupID string `json:"wakeup_id"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(payload, &failure); err != nil {
		t.Fatalf("unmarshal evaluate failure: %v; payload=%s", err, payload)
	}
	return failure.WakeupID, failure.Reason
}

func insertWakeupOpenTaskHandoff(t *testing.T, s *store.Store, handoffID string, taskID int64, requestedAt, receivedAt *time.Time) {
	t.Helper()
	sessionID := daemonTestSessionID(t, s, "wakeup-handoff-agent")
	var requestedValue any
	if requestedAt != nil {
		requestedValue = requestedAt.Format(time.RFC3339Nano)
	}
	var receivedValue any
	var receivedBy any
	if receivedAt != nil {
		receivedValue = receivedAt.Format(time.RFC3339Nano)
		receivedBy = sessionID
	}
	if _, err := s.DB().ExecContext(context.Background(), `
		INSERT INTO task_handoffs (id, task_id, requested_by, received_by, requested_at, received_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, handoffID, taskID, sessionID, receivedBy, requestedValue, receivedValue); err != nil {
		t.Fatalf("insert task handoff %v: %v", handoffID, err)
	}
}

func TestWakeupTrackerPublishesAfterGracePeriodAndResets(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "grace")
	if _, err := s.DeclareTasks(ctx, goalID, "agent", "grace-first", []string{"First task"}, []string{"Complete the first task."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	// Spelled out rather than reading the constant, so changing the interval
	// has to be a deliberate edit here too.
	initialWait := 3 * time.Minute
	if wakeupInitialWait != initialWait {
		t.Fatalf("wakeupInitialWait = %v, want %v", wakeupInitialWait, initialWait)
	}
	if wakeupResendInterval != 3*time.Minute {
		t.Fatalf("wakeupResendInterval = %v, want 3m", wakeupResendInterval)
	}
	if events, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("initial events = %#v, want empty", events)
	}
	if events, err := tracker.evaluate(ctx, s, start.Add(initialWait-time.Nanosecond)); err != nil {
		t.Fatalf("pre-grace evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("pre-grace events = %#v, want empty", events)
	}

	events, err := tracker.evaluate(ctx, s, start.Add(initialWait))
	if err != nil {
		t.Fatalf("publish evaluate: %v", err)
	}
	if len(events) != 1 || events[0].Name != store.EventWakeup {
		t.Fatalf("published events = %#v, want one %v event", events, store.EventWakeup)
	}
	first, ok := events[0].Data.(store.WakeupEvent)
	if !ok {
		t.Fatalf("published data type = %T, want store.WakeupEvent", events[0].Data)
	}
	if first.ProjectID != projectID || first.ActionableGoalCount != 1 || first.UnstartedTaskCount != 1 {
		t.Fatalf("published wakeup = %+v, want project %v with one active goal and task", first, projectID)
	}

	if events, err := tracker.evaluate(ctx, s, start.Add(initialWait+time.Second)); err != nil {
		t.Fatalf("duplicate evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("duplicate events = %#v, want empty", events)
	}

	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if events, err := tracker.evaluate(ctx, s, start.Add(16*time.Minute)); err != nil {
		t.Fatalf("reset evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("reset events = %#v, want empty", events)
	}

	if _, err := s.DeclareTasks(ctx, goalID, "agent", "grace-second", []string{"Second task"}, []string{"Complete the second task."}); err != nil {
		t.Fatalf("DeclareTasks second: %v", err)
	}
	if events, err := tracker.evaluate(ctx, s, start.Add(16*time.Minute+time.Second)); err != nil {
		t.Fatalf("second start evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("second start events = %#v, want empty", events)
	}
	events, err = tracker.evaluate(ctx, s, start.Add(16*time.Minute+initialWait+time.Second))
	if err != nil {
		t.Fatalf("second publish evaluate: %v", err)
	}
	if len(events) != 1 || events[0].Name != store.EventWakeup {
		t.Fatalf("second published events = %#v, want one %v event", events, store.EventWakeup)
	}
	second := events[0].Data.(store.WakeupEvent)
	if second.WakeupID == first.WakeupID {
		t.Fatalf("second wakeup ID = %v, want a fresh ID after the condition reset", second.WakeupID)
	}
}

func TestWakeupTrackerPublishesTaskBreakdown(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "breakdown")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "breakdown-tasks", []string{"Waiting task", "Untouched task one", "Untouched task two"}, []string{"Answer the first task.", "Start the second task.", "Start the third task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	state := store.WakeupState{
		ActionableGoalCount:    1,
		UnassignedGoalCount:    2,
		UnassignedGoalIDs:      []int64{136, 140},
		UnstartedTaskCount:     3,
		WaitingAnswerTaskCount: 1,
		UntouchedTaskCount:     2,
		DelegatedTaskCount:     2,
		WaitingAnswerCount:     2,
		Tasks:                  tasks[1:],
	}
	detect := func(context.Context, int64) (store.WakeupState, error) {
		return state, nil
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	if events, err := tracker.evaluateWith(ctx, s, start, detect); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("initial events = %#v, want empty", events)
	}

	events, err := tracker.evaluateWith(ctx, s, start.Add(wakeupInitialWait), detect)
	if err != nil {
		t.Fatalf("publish evaluate: %v", err)
	}
	if len(events) != 1 || events[0].Name != store.EventWakeup {
		t.Fatalf("published events = %#v, want one %v event", events, store.EventWakeup)
	}
	wakeup, ok := events[0].Data.(store.WakeupEvent)
	if !ok {
		t.Fatalf("published data type = %T, want store.WakeupEvent", events[0].Data)
	}
	if wakeup.ProjectID != projectID || wakeup.ActionableGoalCount != state.ActionableGoalCount || wakeup.UnstartedTaskCount != state.UnstartedTaskCount {
		t.Fatalf("published totals = %+v, want project %v and state totals %+v", wakeup, projectID, state)
	}
	if wakeup.WaitingAnswerCount != state.WaitingAnswerCount {
		t.Fatalf("published waiting answer count = %d, want decision count %d", wakeup.WaitingAnswerCount, state.WaitingAnswerCount)
	}
	if wakeup.WaitingAnswerTaskCount != state.WaitingAnswerTaskCount || wakeup.UntouchedTaskCount != state.UntouchedTaskCount {
		t.Fatalf("published task breakdown = %+v, want state breakdown waiting=%d untouched=%d", wakeup, state.WaitingAnswerTaskCount, state.UntouchedTaskCount)
	}
	if wakeup.DelegatedTaskCount != state.DelegatedTaskCount {
		t.Fatalf("published delegated task count = %d, want %d", wakeup.DelegatedTaskCount, state.DelegatedTaskCount)
	}
	if wakeup.UnassignedGoalCount != state.UnassignedGoalCount || !slices.Equal(wakeup.UnassignedGoalIDs, state.UnassignedGoalIDs) {
		t.Fatalf("published unassigned goals = %d %#v, want %d %#v", wakeup.UnassignedGoalCount, wakeup.UnassignedGoalIDs, state.UnassignedGoalCount, state.UnassignedGoalIDs)
	}
	if total := wakeup.WaitingAnswerTaskCount + wakeup.UntouchedTaskCount; wakeup.UnstartedTaskCount != total {
		t.Fatalf("published task total = %d, want breakdown sum %d", wakeup.UnstartedTaskCount, total)
	}
}

func TestWakeupTrackerDoesNotPublishForWaitingAnswerTasksOnly(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "waiting-only")
	if _, err := s.DeclareTasks(ctx, goalID, "agent", "waiting-only", []string{"Waiting task"}, []string{"Answer the task."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	state := store.WakeupState{
		ActionableGoalCount:    1,
		UnstartedTaskCount:     1,
		WaitingAnswerTaskCount: 1,
		UntouchedTaskCount:     0,
	}
	detect := func(context.Context, int64) (store.WakeupState, error) {
		return state, nil
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 15, 30, 0, 0, time.UTC)
	if events, err := tracker.evaluateWith(ctx, s, start, detect); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("initial events = %#v, want empty", events)
	}

	events, err := tracker.evaluateWith(ctx, s, start.Add(wakeupInitialWait), detect)
	if err != nil {
		t.Fatalf("waiting-only evaluate: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("waiting-only events = %#v, want empty", events)
	}
}

func TestWakeupTrackerPublishesForActionableTasks(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "actionable")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "actionable", []string{"Actionable task"}, []string{"Start the task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	state := store.WakeupState{
		ActionableGoalCount: 1,
		UnstartedTaskCount:  1,
		UntouchedTaskCount:  1,
		Tasks:               tasks,
	}
	detect := func(context.Context, int64) (store.WakeupState, error) {
		return state, nil
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 16, 0, 0, 0, time.UTC)
	if events, err := tracker.evaluateWith(ctx, s, start, detect); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("initial events = %#v, want empty", events)
	}

	events, err := tracker.evaluateWith(ctx, s, start.Add(wakeupInitialWait), detect)
	if err != nil {
		t.Fatalf("actionable evaluate: %v", err)
	}
	if len(events) != 1 || events[0].Name != store.EventWakeup {
		t.Fatalf("actionable events = %#v, want one %v event", events, store.EventWakeup)
	}
}

func TestWakeupTrackerRestartsGracePeriodAfterActionableTasksDisappearAndReturn(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "actionable-reset")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "actionable-reset", []string{"Actionable task"}, []string{"Start the task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	state := store.WakeupState{
		ActionableGoalCount: 1,
		UnstartedTaskCount:  1,
		UntouchedTaskCount:  1,
		Tasks:               tasks,
	}
	detect := func(context.Context, int64) (store.WakeupState, error) {
		return state, nil
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 16, 30, 0, 0, time.UTC)
	if _, err := tracker.evaluateWith(ctx, s, start, detect); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	firstAt := start.Add(wakeupInitialWait)
	if events, err := tracker.evaluateWith(ctx, s, firstAt, detect); err != nil {
		t.Fatalf("first publish evaluate: %v", err)
	} else if len(events) != 1 {
		t.Fatalf("first publish events = %#v, want one event", events)
	}

	state.WaitingAnswerTaskCount = 1
	state.UntouchedTaskCount = 0
	state.Tasks = nil
	clearedAt := firstAt.Add(time.Minute)
	if events, err := tracker.evaluateWith(ctx, s, clearedAt, detect); err != nil {
		t.Fatalf("cleared evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("cleared events = %#v, want empty", events)
	}

	state.WaitingAnswerTaskCount = 0
	state.UntouchedTaskCount = 1
	state.Tasks = tasks
	resumedAt := clearedAt.Add(time.Minute)
	if events, err := tracker.evaluateWith(ctx, s, resumedAt, detect); err != nil {
		t.Fatalf("resumed start evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("resumed start events = %#v, want empty", events)
	}
	if events, err := tracker.evaluateWith(ctx, s, resumedAt.Add(wakeupInitialWait-time.Nanosecond), detect); err != nil {
		t.Fatalf("resumed pre-grace evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("resumed pre-grace events = %#v, want empty", events)
	}
	if events, err := tracker.evaluateWith(ctx, s, resumedAt.Add(wakeupInitialWait), detect); err != nil {
		t.Fatalf("resumed publish evaluate: %v", err)
	} else if len(events) != 1 || events[0].Name != store.EventWakeup {
		t.Fatalf("resumed publish events = %#v, want one %v event", events, store.EventWakeup)
	}
}

func TestWakeupTrackerRepublishesWhileConditionRemainsActive(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "repeat")
	if _, err := s.DeclareTasks(ctx, goalID, "agent", "repeat-tasks", []string{"Unstarted task"}, []string{"Keep the task unstarted."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	evaluateAt := func(now time.Time, wantEvents int) {
		t.Helper()
		events, err := tracker.evaluateWith(ctx, s, now, s.DetectWakeup)
		if err != nil {
			t.Fatalf("evaluate at %v: %v", now, err)
		}
		if len(events) != wantEvents {
			t.Fatalf("events at %v = %#v, want %d event(s)", now, events, wantEvents)
		}
		for _, event := range events {
			if event.Name != store.EventWakeup {
				t.Fatalf("events at %v = %#v, want only %v", now, events, store.EventWakeup)
			}
		}
	}

	evaluateAt(start, 0)
	// Start at the initial wait so the baseline failure isolates missing resend behavior.
	firstAt := start.Add(wakeupInitialWait)
	evaluateAt(firstAt, 1)
	evaluateAt(firstAt.Add(wakeupResendInterval-time.Nanosecond), 0)
	evaluateAt(firstAt.Add(wakeupResendInterval), 1)
	evaluateAt(firstAt.Add(2*wakeupResendInterval), 1)
	evaluateAt(firstAt.Add(3*wakeupResendInterval), 1)
}

func TestWakeupTrackerReportsDetectorCountDiscrepancyOnce(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "discrepancy")
	if _, err := s.DeclareTasks(ctx, goalID, "agent", "discrepancy-tasks", []string{"Unstarted task"}, []string{"Start the task later."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	now := time.Date(2026, 8, 20, 13, 0, 0, 0, time.UTC)
	detect := func(context.Context, int64) (store.WakeupState, error) {
		return store.WakeupState{}, nil
	}
	events, err := tracker.evaluateWith(ctx, s, now, detect)
	if err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	if len(events) != 1 || events[0].Name != store.EventWakeupDiscrepancy {
		t.Fatalf("discrepancy events = %#v, want one %v event", events, store.EventWakeupDiscrepancy)
	}
	discrepancy, ok := events[0].Data.(store.WakeupDiscrepancyEvent)
	if !ok {
		t.Fatalf("discrepancy data type = %T, want store.WakeupDiscrepancyEvent", events[0].Data)
	}
	if discrepancy.ProjectID != projectID || discrepancy.DetectorUnstartedTaskCount != 0 || discrepancy.CountedUnstartedTaskCount != 1 {
		t.Fatalf("discrepancy = %+v, want detector 0 and counted 1 for project %v", discrepancy, projectID)
	}

	if events, err := tracker.evaluateWith(ctx, s, now.Add(time.Minute), detect); err != nil {
		t.Fatalf("duplicate evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("duplicate discrepancy events = %#v, want empty", events)
	}
}

func TestWakeupTrackerIgnoresGoalWithRunningClaimAndUnstartedTask(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "running-goal")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "running-goal-tasks", []string{"Running task", "Unstarted task"}, []string{"Keep working on the running task.", "Start the remaining task later."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	runningSessionID := daemonTestSessionID(t, s, "running-goal-session")
	if err := s.AssociateAgentSessionWithProject(ctx, runningSessionID, projectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, runningSessionID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	now := time.Date(2026, 8, 20, 13, 0, 0, 0, time.UTC)
	events, err := tracker.evaluate(ctx, s, now)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("definition-difference events = %#v, want empty", events)
	}
}

func TestWakeupTrackerIgnoresSnapshotDiscrepancyAfterTaskDeclaration(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "snapshot")

	tracker := newWakeupTracker(time.Time{})
	now := time.Date(2026, 8, 20, 14, 0, 0, 0, time.UTC)
	detectCalls := 0
	events, err := tracker.evaluateWith(ctx, s, now, func(ctx context.Context, detectedProjectID int64) (store.WakeupState, error) {
		detectCalls++
		if detectCalls == 1 {
			if _, err := s.DeclareTasks(ctx, goalID, "agent", "snapshot-tasks", []string{"First task", "Second task"}, []string{"Complete the first task.", "Complete the second task."}); err != nil {
				t.Fatalf("DeclareTasks: %v", err)
			}
			return store.WakeupState{}, nil
		}
		return s.DetectWakeup(ctx, detectedProjectID)
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("snapshot discrepancy events = %#v, want empty", events)
	}
	if detectCalls != 2 {
		t.Fatalf("DetectWakeup calls = %d, want initial snapshot plus one recheck", detectCalls)
	}

	counted, err := s.CountUnstartedTasks(ctx, projectID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != 2 {
		t.Fatalf("counted unstarted tasks = %d, want 2", counted)
	}
}

func TestRunMaintenancePublishesKeepaliveWithInjectedTime(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	ch, cancel := s.SubscribeEvents()
	defer cancel()

	now := time.Date(2026, 8, 20, 14, 0, 0, 123, time.UTC)
	newDaemonWithClock(s, func() time.Time { return now }).runMaintenance(ctx, newWakeupTracker(time.Time{}), now)

	select {
	case event := <-ch:
		if event.Name != store.EventKeepalive {
			t.Fatalf("first maintenance event name = %v, want %v", event.Name, store.EventKeepalive)
		}
		keepalive, ok := event.Data.(store.KeepaliveEvent)
		if !ok {
			t.Fatalf("keepalive data type = %T, want store.KeepaliveEvent", event.Data)
		}
		if !keepalive.At.Equal(now) {
			t.Fatalf("keepalive time = %v, want %v", keepalive.At, now)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for keepalive event")
	}
}

func TestRunMaintenancePublishesEvaluateFailure(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	newWakeupTestGoal(t, s, "evaluate-failure")
	ch, cancel := s.SubscribeEvents()
	defer cancel()

	now := time.Date(2026, 8, 20, 14, 30, 0, 0, time.UTC)
	reason := "injected evaluate failure"
	callRunMaintenanceWith(t, newDaemonWithClock(s, func() time.Time { return now }), ctx, newWakeupTracker(time.Time{}), now, func(context.Context, int64) (store.WakeupState, error) {
		return store.WakeupState{}, errors.New(reason)
	})

	if event := receiveWakeupEvent(t, ch); event.Name != store.EventKeepalive {
		t.Fatalf("first maintenance event name = %q, want %q", event.Name, store.EventKeepalive)
	}
	failure := receiveWakeupEvent(t, ch)
	if id, gotReason := decodeEvaluateFailure(t, failure); id == "" || !strings.Contains(gotReason, reason) {
		t.Fatalf("evaluate failure = (id=%q, reason=%q), want non-empty id and reason %q", id, gotReason, reason)
	}
}

func TestWakeupTrackerReturnsEventsWhenLaterProjectEvaluationFails(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	firstProjectID, firstGoalID := newWakeupTestGoal(t, s, "evaluate-partial-return-first")
	secondProjectID, _ := newWakeupTestGoal(t, s, "evaluate-partial-return-second")
	tasks, err := s.DeclareTasks(ctx, firstGoalID, "agent", "evaluate-partial-return", []string{"Completed task"}, []string{"The task is complete."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 15, 30, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	now := start.Add(wakeupPublishAfter)
	events, err := tracker.evaluateWith(ctx, s, now, func(ctx context.Context, projectID int64) (store.WakeupState, error) {
		if projectID == firstProjectID {
			return s.DetectWakeup(ctx, projectID)
		}
		if projectID == secondProjectID {
			return store.WakeupState{}, errors.New("later project evaluation failed")
		}
		return store.WakeupState{}, errors.New("unexpected project")
	})
	if err == nil {
		t.Fatal("evaluate returned nil error, want injected error")
	}
	if len(events) == 0 {
		t.Fatalf("events = %#v, want partial detection events", events)
	}
	detection, ok := events[0].Data.(store.DetectionEvent)
	if !ok || detection.GoalID != firstGoalID {
		t.Fatalf("partial event = %#v, want a detection for goal %d", events[0], firstGoalID)
	}
}

func TestRunMaintenancePublishesEventsBeforeEvaluateFailure(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	firstProjectID, firstGoalID := newWakeupTestGoal(t, s, "evaluate-partial-first")
	secondProjectID, _ := newWakeupTestGoal(t, s, "evaluate-partial-second")
	tasks, err := s.DeclareTasks(ctx, firstGoalID, "agent", "evaluate-partial", []string{"Completed task"}, []string{"The task is complete."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	ch, cancel := s.SubscribeEvents()
	defer cancel()

	reason := "second project evaluation failed"
	callRunMaintenanceWith(t, newDaemonWithClock(s, func() time.Time { return start.Add(wakeupPublishAfter) }), ctx, tracker, start.Add(wakeupPublishAfter), func(ctx context.Context, projectID int64) (store.WakeupState, error) {
		if projectID == firstProjectID {
			return s.DetectWakeup(ctx, projectID)
		}
		if projectID == secondProjectID {
			return store.WakeupState{}, errors.New(reason)
		}
		return store.WakeupState{}, errors.New("unexpected project")
	})

	if event := receiveWakeupEvent(t, ch); event.Name != store.EventKeepalive {
		t.Fatalf("first maintenance event name = %q, want %q", event.Name, store.EventKeepalive)
	}
	for range 2 {
		detection := receiveWakeupEvent(t, ch)
		if detection.Name != store.EventDetectionCompletionReportMissing && detection.Name != store.EventDetectionCommitsMissing {
			t.Fatalf("partial event name = %q, want a first-project detection", detection.Name)
		}
		detectionData, ok := detection.Data.(store.DetectionEvent)
		if !ok {
			t.Fatalf("partial event data type = %T, want store.DetectionEvent", detection.Data)
		}
		if detectionData.GoalID != firstGoalID {
			t.Fatalf("partial detection goal_id = %d, want %d", detectionData.GoalID, firstGoalID)
		}
	}
	if id, gotReason := decodeEvaluateFailure(t, receiveWakeupEvent(t, ch)); id == "" || !strings.Contains(gotReason, reason) {
		t.Fatalf("evaluate failure = (id=%q, reason=%q), want non-empty id and reason %q", id, gotReason, reason)
	}
}

func TestRunMaintenanceWithSuccessDoesNotPublishEvaluateFailure(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	newWakeupTestGoal(t, s, "evaluate-success")
	ch, cancel := s.SubscribeEvents()
	defer cancel()

	now := time.Date(2026, 8, 20, 16, 0, 0, 0, time.UTC)
	callRunMaintenanceWith(t, newDaemonWithClock(s, func() time.Time { return now }), ctx, newWakeupTracker(time.Time{}), now, func(context.Context, int64) (store.WakeupState, error) {
		return store.WakeupState{}, nil
	})
	if event := receiveWakeupEvent(t, ch); event.Name != store.EventKeepalive {
		t.Fatalf("maintenance event name = %q, want %q", event.Name, store.EventKeepalive)
	}
	select {
	case event := <-ch:
		t.Fatalf("unexpected event after successful evaluation: %q", event.Name)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRunMaintenanceReusesEvaluateFailureIDUntilRecovery(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	newWakeupTestGoal(t, s, "evaluate-failure-id")
	ch, cancel := s.SubscribeEvents()
	defer cancel()

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 17, 0, 0, 0, time.UTC)
	detectFailure := func(context.Context, int64) (store.WakeupState, error) {
		return store.WakeupState{}, errors.New("repeated evaluation failure")
	}
	readFailureID := func(now time.Time, detect func(context.Context, int64) (store.WakeupState, error)) string {
		callRunMaintenanceWith(t, newDaemonWithClock(s, func() time.Time { return now }), ctx, tracker, now, detect)
		if event := receiveWakeupEvent(t, ch); event.Name != store.EventKeepalive {
			t.Fatalf("first maintenance event name = %q, want %q", event.Name, store.EventKeepalive)
		}
		id, _ := decodeEvaluateFailure(t, receiveWakeupEvent(t, ch))
		return id
	}

	firstID := readFailureID(start, detectFailure)
	secondID := readFailureID(start.Add(time.Minute), detectFailure)
	if secondID != firstID {
		t.Fatalf("continued failure ID = %q, want reused ID %q", secondID, firstID)
	}

	successTime := start.Add(2 * time.Minute)
	callRunMaintenanceWith(t, newDaemonWithClock(s, func() time.Time { return successTime }), ctx, tracker, successTime, func(context.Context, int64) (store.WakeupState, error) {
		return store.WakeupState{}, nil
	})
	if event := receiveWakeupEvent(t, ch); event.Name != store.EventKeepalive {
		t.Fatalf("recovery maintenance event name = %q, want %q", event.Name, store.EventKeepalive)
	}
	thirdID := readFailureID(start.Add(3*time.Minute), detectFailure)
	if thirdID == firstID {
		t.Fatalf("failure after recovery reused ID %q", thirdID)
	}
}

func TestRunMaintenancePublishesEventsFromProjectsAfterEvaluationFailure(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	firstProjectID, firstGoalID := newWakeupTestGoal(t, s, "evaluate-after-first")
	secondProjectID, _ := newWakeupTestGoal(t, s, "evaluate-after-second")
	thirdProjectID, thirdGoalID := newWakeupTestGoal(t, s, "evaluate-after-third")
	for goalID, key := range map[int64]string{firstGoalID: "evaluate-after-first-task", thirdGoalID: "evaluate-after-third-task"} {
		tasks, err := s.DeclareTasks(ctx, goalID, "agent", key, []string{"Completed task"}, []string{"The task is complete."})
		if err != nil {
			t.Fatalf("DeclareTasks: %v", err)
		}
		if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
			t.Fatalf("UpdateTask: %v", err)
		}
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	ch, cancel := s.SubscribeEvents()
	defer cancel()
	reason := "middle project evaluation failed"
	now := start.Add(wakeupPublishAfter)
	callRunMaintenanceWith(t, newDaemonWithClock(s, func() time.Time { return now }), ctx, tracker, now, func(ctx context.Context, projectID int64) (store.WakeupState, error) {
		if projectID == secondProjectID {
			return store.WakeupState{}, errors.New(reason)
		}
		if projectID == firstProjectID || projectID == thirdProjectID {
			return s.DetectWakeup(ctx, projectID)
		}
		return store.WakeupState{}, errors.New("unexpected project")
	})

	if event := receiveWakeupEvent(t, ch); event.Name != store.EventKeepalive {
		t.Fatalf("first maintenance event name = %q, want %q", event.Name, store.EventKeepalive)
	}
	seenGoals := make(map[int64]bool)
	for range 4 {
		event := receiveWakeupEvent(t, ch)
		if event.Name != store.EventDetectionCompletionReportMissing && event.Name != store.EventDetectionCommitsMissing {
			t.Fatalf("project detection event name = %q, want a project detection", event.Name)
		}
		detection, ok := event.Data.(store.DetectionEvent)
		if !ok {
			t.Fatalf("project detection data type = %T, want store.DetectionEvent", event.Data)
		}
		seenGoals[detection.GoalID] = true
	}
	if !seenGoals[firstGoalID] || !seenGoals[thirdGoalID] {
		t.Fatalf("published detection goals = %v, want %d and %d", seenGoals, firstGoalID, thirdGoalID)
	}
	if id, gotReason := decodeEvaluateFailure(t, receiveWakeupEvent(t, ch)); id == "" || !strings.Contains(gotReason, reason) {
		t.Fatalf("evaluate failure = (id=%q, reason=%q), want non-empty id and reason %q", id, gotReason, reason)
	}
}

func TestWakeupTrackerPreservesDetectionGraceAfterProjectEvaluationFailure(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	firstProjectID, firstGoalID := newWakeupTestGoal(t, s, "grace-first")
	secondProjectID, secondGoalID := newWakeupTestGoal(t, s, "grace-second")
	for goalID, key := range map[int64]string{firstGoalID: "grace-first-task", secondGoalID: "grace-second-task"} {
		tasks, err := s.DeclareTasks(ctx, goalID, "agent", key, []string{"Completed task"}, []string{"The task is complete."})
		if err != nil {
			t.Fatalf("DeclareTasks: %v", err)
		}
		if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
			t.Fatalf("UpdateTask: %v", err)
		}
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 19, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	failedTick := start.Add(time.Minute)
	if _, err := tracker.evaluateWith(ctx, s, failedTick, func(ctx context.Context, projectID int64) (store.WakeupState, error) {
		if projectID == secondProjectID {
			return store.WakeupState{}, errors.New("grace project evaluation failed")
		}
		if projectID == firstProjectID {
			return s.DetectWakeup(ctx, projectID)
		}
		return store.WakeupState{}, errors.New("unexpected project")
	}); err == nil {
		t.Fatal("failed tick returned nil error, want injected error")
	}

	secondKey := detectionTrackerKey(store.EventDetectionCompletionReportMissing, secondGoalID)
	if _, ok := tracker.detectionActiveSince[secondKey]; !ok {
		t.Fatalf("detection grace key %q was removed during failed tick", secondKey)
	}

	events, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter))
	if err != nil {
		t.Fatalf("post-failure evaluate: %v", err)
	}
	seenGoals := make(map[int64]bool)
	for _, event := range events {
		if event.Name != store.EventDetectionCompletionReportMissing {
			continue
		}
		detection, ok := event.Data.(store.DetectionEvent)
		if !ok {
			t.Fatalf("project detection data type = %T, want store.DetectionEvent", event.Data)
		}
		seenGoals[detection.GoalID] = true
	}
	if !seenGoals[firstGoalID] || !seenGoals[secondGoalID] {
		t.Fatalf("post-failure detection goals = %v, want %d and %d at original grace deadline", seenGoals, firstGoalID, secondGoalID)
	}
}

func TestWakeupTrackerCleansStaleDetectionKeysAfterSuccessfulEvaluation(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "stale-cleanup")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "stale-cleanup-task", []string{"Completed task"}, []string{"The task is complete."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 20, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	if len(tracker.detectionActiveSince) == 0 {
		t.Fatal("initial evaluation did not establish a detection grace key")
	}
	if _, err := tracker.evaluateWith(ctx, s, start.Add(time.Minute), func(context.Context, int64) (store.WakeupState, error) {
		return store.WakeupState{}, nil
	}); err != nil {
		t.Fatalf("successful cleanup evaluate: %v", err)
	}
	if len(tracker.detectionActiveSince) != 0 || len(tracker.detectionPublished) != 0 {
		t.Fatalf("stale detection keys remain after successful evaluation for project %d: active=%v published=%v", projectID, tracker.detectionActiveSince, tracker.detectionPublished)
	}
}

func TestWakeupTrackerPublishesCompletionDetectionWithoutUnstartedTasks(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "completion-no-unstarted")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "completion-no-unstarted", []string{"Completed task"}, []string{"The task is already complete."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	if events, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	} else if _, ok := findDetectionEvent(events, store.EventDetectionCompletionReportMissing, goalID); ok {
		t.Fatalf("initial events = %#v, want no completion detection before grace", events)
	}

	events, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter))
	if err != nil {
		t.Fatalf("publish evaluate: %v", err)
	}
	detection, ok := findDetectionEvent(events, store.EventDetectionCompletionReportMissing, goalID)
	if !ok {
		t.Fatalf("published events = %#v, want completion detection", events)
	}
	if detection.ProjectID != projectID || detection.GoalID != goalID || detection.TaskID != 0 || detection.DetectionID == "" {
		t.Fatalf("completion detection = %+v, want project %v and goal %v", detection, projectID, goalID)
	}
}

func TestWakeupTrackerDelaysDetectionUntilGracePeriod(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "completion-grace")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "completion-grace", []string{"Completed task"}, []string{"Wait for the grace period."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 16, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	preGrace := start.Add(wakeupInitialWait - time.Nanosecond)
	if events, err := tracker.evaluate(ctx, s, preGrace); err != nil {
		t.Fatalf("pre-grace evaluate: %v", err)
	} else if _, ok := findDetectionEvent(events, store.EventDetectionCompletionReportMissing, goalID); ok {
		t.Fatalf("pre-grace events = %#v, want no completion detection", events)
	}
}

func TestWakeupTrackerDoesNotRepeatDetectionForSameCondition(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "completion-duplicate")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "completion-duplicate", []string{"Completed task"}, []string{"Do not repeat the detection."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 17, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	firstEvents, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter))
	if err != nil {
		t.Fatalf("publish evaluate: %v", err)
	}
	first, ok := findDetectionEvent(firstEvents, store.EventDetectionCompletionReportMissing, goalID)
	if !ok {
		t.Fatalf("published events = %#v, want completion detection", firstEvents)
	}
	secondEvents, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter+time.Hour))
	if err != nil {
		t.Fatalf("duplicate evaluate: %v", err)
	}
	if second, ok := findDetectionEvent(secondEvents, store.EventDetectionCompletionReportMissing, goalID); ok {
		t.Fatalf("duplicate completion detection = %+v after first %+v", second, first)
	}
}

func TestWakeupTrackerResetsDetectionAfterConditionClears(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "completion-reset")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "completion-reset-first", []string{"First completed task"}, []string{"Complete the first task."})
	if err != nil {
		t.Fatalf("DeclareTasks first: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask first: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	firstEvents, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter))
	if err != nil {
		t.Fatalf("first publish evaluate: %v", err)
	}
	first, ok := findDetectionEvent(firstEvents, store.EventDetectionCompletionReportMissing, goalID)
	if !ok {
		t.Fatalf("first published events = %#v, want completion detection", firstEvents)
	}

	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, 0); err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}
	clearedEvents, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter+time.Minute))
	if err != nil {
		t.Fatalf("cleared evaluate: %v", err)
	}
	if cleared, ok := findDetectionEvent(clearedEvents, store.EventDetectionCompletionReportMissing, goalID); ok {
		t.Fatalf("cleared completion detection = %+v, want none", cleared)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask second done: %v", err)
	}
	secondStart := start.Add(wakeupPublishAfter + 2*time.Minute)
	if events, err := tracker.evaluate(ctx, s, secondStart); err != nil {
		t.Fatalf("second start evaluate: %v", err)
	} else if detection, ok := findDetectionEvent(events, store.EventDetectionCompletionReportMissing, goalID); ok {
		t.Fatalf("second start completion detection = %+v, want none", detection)
	}
	secondEvents, err := tracker.evaluate(ctx, s, secondStart.Add(wakeupPublishAfter))
	if err != nil {
		t.Fatalf("second publish evaluate: %v", err)
	}
	second, ok := findDetectionEvent(secondEvents, store.EventDetectionCompletionReportMissing, goalID)
	if !ok {
		t.Fatalf("second published events = %#v, want completion detection", secondEvents)
	}
	if second.DetectionID == first.DetectionID {
		t.Fatalf("second detection ID = %v, want a fresh ID after reset", second.DetectionID)
	}
}

func TestWakeupTrackerKeepsDetectionGracePerTarget(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalAID := newWakeupTestGoal(t, s, "completion-target-a")
	tasksA, err := s.DeclareTasks(ctx, goalAID, "agent", "completion-target-a", []string{"Goal A task"}, []string{"Complete goal A."})
	if err != nil {
		t.Fatalf("DeclareTasks A: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasksA[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask A: %v", err)
	}

	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 19, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}

	goalB, err := s.CreateGoal(ctx, projectID, "Resume completion-target-b", "human")
	if err != nil {
		t.Fatalf("CreateGoal B: %v", err)
	}
	tasksB, err := s.DeclareTasks(ctx, goalB.ID, "agent", "completion-target-b", []string{"Goal B task"}, []string{"Complete goal B."})
	if err != nil {
		t.Fatalf("DeclareTasks B: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasksB[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask B: %v", err)
	}
	if events, err := tracker.evaluate(ctx, s, start.Add(10*time.Minute)); err != nil {
		t.Fatalf("goal B start evaluate: %v", err)
	} else if detection, ok := findDetectionEvent(events, store.EventDetectionCompletionReportMissing, goalB.ID); ok {
		t.Fatalf("goal B early completion detection = %+v, want none", detection)
	}

	goalAEvents, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter))
	if err != nil {
		t.Fatalf("goal A publish evaluate: %v", err)
	}
	if detection, ok := findDetectionEvent(goalAEvents, store.EventDetectionCompletionReportMissing, goalAID); !ok {
		t.Fatalf("goal A events = %#v, want completion detection", goalAEvents)
	} else if other, ok := findDetectionEvent(goalAEvents, store.EventDetectionCompletionReportMissing, goalB.ID); ok {
		t.Fatalf("goal B early completion detection = %+v, want none", other)
	} else if detection.GoalID != goalAID {
		t.Fatalf("goal A detection = %+v, want goal %v", detection, goalAID)
	}

	goalBEvents, err := tracker.evaluate(ctx, s, start.Add(25*time.Minute))
	if err != nil {
		t.Fatalf("goal B publish evaluate: %v", err)
	}
	if detection, ok := findDetectionEvent(goalBEvents, store.EventDetectionCompletionReportMissing, goalB.ID); !ok {
		t.Fatalf("goal B events = %#v, want completion detection", goalBEvents)
	} else if detection.GoalID != goalB.ID {
		t.Fatalf("goal B detection = %+v, want goal %v", detection, goalB.ID)
	}
}

func TestWakeupTrackerPublishesStalledHandoffDetections(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "stalled-handoff")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "stalled-handoff-task", []string{"Stalled task", "Stale task"}, []string{"Keep the task handoff open.", "Keep the stale claim open."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	tracker := newWakeupTracker(time.Time{})
	start := time.Date(2026, 8, 20, 20, 0, 0, 0, time.UTC)

	requestedAt := start.Add(-detectionHandoffUnreceivedAfter + time.Nanosecond)
	receivedAt := start.Add(-detectionHandoffUnreportedAfter + time.Nanosecond)
	claimedAt := start.Add(-detectionClaimUndelegatedAfter + time.Nanosecond)
	staleClaimedAt := start.Add(-detectionStaleClaimAfter + time.Nanosecond)
	insertWakeupOpenTaskHandoff(t, s, "handoff-undelegated", tasks[0].ID, &claimedAt, nil)
	insertWakeupOpenTaskHandoff(t, s, "handoff-stale", tasks[1].ID, &staleClaimedAt, &staleClaimedAt)
	state := store.WakeupState{
		UnstartedTaskCount: 2,
		HandoffsAwaitingReceipt: []store.TaskHandoff{{
			ID: "handoff-unreceived", TaskID: tasks[0].ID, RequestedAt: &requestedAt,
		}},
		HandoffsAwaitingReport: []store.TaskHandoff{{
			ID: "handoff-unreported", TaskID: tasks[0].ID, ReceivedAt: &receivedAt,
		}},
		UnclaimedDoingTasks: []domain.Task{{ID: tasks[0].ID, GoalID: goalID}},
		UndelegatedClaims:   []domain.Task{{ID: tasks[0].ID, GoalID: goalID}},
		StaleClaims:         []domain.Task{{ID: tasks[1].ID, GoalID: goalID}},
	}
	detect := func(context.Context, int64) (store.WakeupState, error) {
		return state, nil
	}

	if events, err := tracker.evaluateWith(ctx, s, start, detect); err != nil {
		t.Fatalf("pre-threshold evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("pre-threshold events = %#v, want empty", events)
	}

	after := start.Add(detectionHandoffUnreceivedAfter + time.Nanosecond)
	events, err := tracker.evaluateWith(ctx, s, after, detect)
	if err != nil {
		t.Fatalf("post-threshold evaluate: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("post-threshold events = %#v, want five detections", events)
	}
	want := map[string]struct {
		projectID int64
		goalID    int64
		handoffID string
		taskID    int64
	}{
		store.EventDetectionUnclaimedDoing:    {projectID: projectID, goalID: goalID, taskID: tasks[0].ID},
		store.EventDetectionHandoffUnreceived: {projectID: projectID, goalID: goalID, handoffID: "handoff-unreceived", taskID: tasks[0].ID},
		store.EventDetectionHandoffUnreported: {projectID: projectID, goalID: goalID, handoffID: "handoff-unreported", taskID: tasks[0].ID},
		store.EventDetectionClaimUndelegated:  {projectID: projectID, goalID: goalID, taskID: tasks[0].ID},
		store.EventDetectionClaimStale:        {projectID: projectID, goalID: goalID, taskID: tasks[1].ID},
	}
	for _, event := range events {
		expected, ok := want[event.Name]
		if !ok {
			t.Fatalf("unexpected event = %#v", event)
		}
		detection, ok := event.Data.(store.DetectionEvent)
		if !ok {
			t.Fatalf("event %v data type = %T, want store.DetectionEvent", event.Name, event.Data)
		}
		if detection.ProjectID != expected.projectID || detection.GoalID != expected.goalID || detection.HandoffID != expected.handoffID || detection.TaskID != expected.taskID || detection.DetectionID == "" {
			t.Fatalf("event %v detection = %+v, want project=%v goal=%v handoff=%v task=%v", event.Name, detection, expected.projectID, expected.goalID, expected.handoffID, expected.taskID)
		}
		delete(want, event.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing stalled handoff events = %#v", want)
	}

	if events, err := tracker.evaluateWith(ctx, s, after.Add(time.Hour), detect); err != nil {
		t.Fatalf("duplicate evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("duplicate events = %#v, want empty", events)
	}
}

// Unchanged is stronger evidence: changed only shows that someone used the shared worktree.
func TestWakeupTrackerReportsHandoffWorktreeActivity(t *testing.T) {
	const goalID int64 = 1
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	receivedAt := now.Add(-detectionHandoffUnreportedAfter)

	for _, tc := range []struct {
		name          string
		validWorktree bool
		setup         func(t *testing.T, root, worktree string)
		want          string
	}{
		{
			name:          "changed",
			validWorktree: true,
			setup: func(t *testing.T, root, worktree string) {
				path := filepath.Join(worktree, "worked-on.txt")
				if err := os.WriteFile(path, []byte("work\n"), 0o644); err != nil {
					t.Fatalf("write changed file: %v", err)
				}
				changedAt := receivedAt.Add(time.Second)
				if err := os.Chtimes(path, changedAt, changedAt); err != nil {
					t.Fatalf("set changed file mtime: %v", err)
				}
			},
			want: "changed",
		},
		{
			name:          "changed by commit",
			validWorktree: true,
			setup: func(t *testing.T, root, worktree string) {
				commitWakeupTestWorktree(t, worktree, receivedAt.Add(time.Second))
			},
			want: "changed",
		},
		{name: "unchanged", validWorktree: true, want: "unchanged"},
		{
			name: "unassessed when git fails",
			setup: func(t *testing.T, root, worktree string) {
				if err := os.MkdirAll(worktree, 0o755); err != nil {
					t.Fatalf("create non-git worktree: %v", err)
				}
			},
			want: "",
		},
		{name: "unassessed when worktree is absent", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newWakeupTestStore(t)
			root := t.TempDir()
			worktree := filepath.Join(root, ".worktrees", strconv.FormatInt(goalID, 10))
			if tc.validWorktree {
				worktree = newWakeupTestWorktree(t, root, goalID, receivedAt.Add(-time.Second))
			}
			if tc.setup != nil {
				tc.setup(t, root, worktree)
			}
			project, err := s.CreateProject(ctx, "worktree-activity-"+tc.name, root)
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			goal, err := s.CreateGoal(ctx, project.ID, "Resume worktree activity", "human")
			if err != nil {
				t.Fatalf("CreateGoal: %v", err)
			}
			if goal.ID != goalID {
				t.Fatalf("goal ID = %d, want %d", goal.ID, goalID)
			}
			tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "worktree-activity-"+tc.name, []string{"Worktree activity"}, []string{"Keep the handoff open."})
			if err != nil {
				t.Fatalf("DeclareTasks: %v", err)
			}
			tracker := newWakeupTracker(time.Time{})
			events, err := tracker.evaluateWith(ctx, s, now, func(context.Context, int64) (store.WakeupState, error) {
				return store.WakeupState{HandoffsAwaitingReport: []store.TaskHandoff{{
					ID: "handoff-worktree-activity", TaskID: tasks[0].ID, ReceivedAt: &receivedAt,
				}}}, nil
			})
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			detection, ok := findDetectionEvent(events, store.EventDetectionHandoffUnreported, goalID)
			if !ok {
				t.Fatalf("events = %#v, want handoff detection", events)
			}
			payload, err := json.Marshal(detection)
			if err != nil {
				t.Fatalf("marshal detection: %v", err)
			}
			var data map[string]any
			if err := json.Unmarshal(payload, &data); err != nil {
				t.Fatalf("unmarshal detection: %v", err)
			}
			if got, _ := data["worktree_activity"].(string); got != tc.want {
				t.Fatalf("worktree_activity = %q, want %q; payload=%s", got, tc.want, payload)
			}
		})
	}
}

func TestHandoffWorktreeActivityReturnsUnknownForExpiredContext(t *testing.T) {
	const goalID int64 = 1
	root := t.TempDir()
	receivedAt := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	newWakeupTestWorktree(t, root, goalID, receivedAt.Add(-time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if got := handoffWorktreeActivity(ctx, root, goalID, receivedAt); got != "" {
		t.Fatalf("handoffWorktreeActivity with expired context = %q, want unknown", got)
	}
}

func newWakeupTestWorktree(t *testing.T, root string, goalID int64, initialCommitAt time.Time) string {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", root}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test User")
	cmd := exec.CommandContext(context.Background(), "git", "-C", root, "commit", "--allow-empty", "-m", "initial")
	initialCommitDate := initialCommitAt.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+initialCommitDate, "GIT_COMMITTER_DATE="+initialCommitDate)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit initial: %v: %s", err, output)
	}
	goal8 := strconv.FormatInt(goalID, 10)
	if len(goal8) > 8 {
		goal8 = goal8[:8]
	}
	worktree := filepath.Join(root, ".worktrees", goal8)
	git("worktree", "add", "-b", "wt/goal-"+goal8, worktree)
	return worktree
}

func commitWakeupTestWorktree(t *testing.T, worktree string, committedAt time.Time) {
	t.Helper()
	path := filepath.Join(worktree, "committed-work.txt")
	if err := os.WriteFile(path, []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write committed file: %v", err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", worktree}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	git("add", "committed-work.txt")
	cmd := exec.CommandContext(context.Background(), "git", "-C", worktree, "commit", "-m", "worked")
	commitDate := committedAt.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+commitDate, "GIT_COMMITTER_DATE="+commitDate)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit worked: %v: %s", err, output)
	}
}

func TestDaemonNewTrackerUsesDaemonClock(t *testing.T) {
	s := newWakeupTestStore(t)
	startedAt := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	d := newDaemonWithClock(s, func() time.Time { return startedAt })

	tracker := d.newTracker()
	if !tracker.startedAt.Equal(startedAt) {
		t.Fatalf("tracker startedAt = %v, want %v", tracker.startedAt, startedAt)
	}
}

func TestWakeupTrackerDoesNotPublishCompletedHandoffFromSweep(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "reported-sweep")
	tasks, err := s.DeclareTasks(ctx, goalID, "reported-sweep", "reported-sweep", []string{"Completed handoff task"}, []string{"The completed handoff must not be swept."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	start := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
	requestedAt := start.Add(-time.Minute)
	receivedAt := start.Add(-time.Minute)
	insertWakeupOpenTaskHandoff(t, s, "reported-sweep-handoff", tasks[0].ID, &requestedAt, &receivedAt)
	completedAt := start.Add(time.Second)
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE task_handoffs
		SET completed_report_at = ?, complete_report = ?
		WHERE id = ?
	`, completedAt.Format(time.RFC3339Nano), "completed report", "reported-sweep-handoff"); err != nil {
		t.Fatalf("complete task handoff fixture: %v", err)
	}

	tracker := newWakeupTracker(start)
	events, err := tracker.evaluate(ctx, s, start.Add(2*time.Second))
	if err != nil {
		t.Fatalf("sweep evaluate: %v", err)
	}
	for _, event := range events {
		if event.Name == store.EventHandoffReported {
			t.Fatalf("sweep published handoff_reported event: %#v", event)
		}
	}
}

func TestWakeupTrackerPublishesUnappliedDecisionAndStaleClaimDetections(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "unapplied-decisions")
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "stale-claim-task", []string{"Stale task"}, []string{"Keep the task handoff open."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	start := time.Date(2026, 8, 20, 20, 0, 0, 0, time.UTC)
	defaultAppliedAt := start.Add(-detectionDefaultDecisionUnappliedAfter + time.Nanosecond)
	claimedAt := start.Add(-detectionStaleClaimAfter + time.Nanosecond)
	insertWakeupOpenTaskHandoff(t, s, "handoff-stale", tasks[0].ID, &claimedAt, &claimedAt)
	state := store.WakeupState{
		AnsweredUnappliedDecisions: []domain.Decision{{ID: 1, GoalID: goalID}},
		DefaultUnappliedDecisions: []domain.Decision{{
			ID:               2,
			GoalID:           goalID,
			DefaultAppliedAt: &defaultAppliedAt,
		}},
		StaleClaims: []domain.Task{{ID: tasks[0].ID, GoalID: goalID}},
	}
	detect := func(context.Context, int64) (store.WakeupState, error) {
		return state, nil
	}
	tracker := newWakeupTracker(time.Time{})

	events, err := tracker.evaluateWith(ctx, s, start, detect)
	if err != nil {
		t.Fatalf("pre-threshold evaluate: %v", err)
	}
	if len(events) != 1 || events[0].Name != store.EventDetectionDecisionAnsweredUnapplied {
		t.Fatalf("pre-threshold events = %#v, want immediate answered decision event", events)
	}
	humanDetection, ok := events[0].Data.(store.DetectionEvent)
	if !ok || humanDetection.ProjectID != projectID || humanDetection.DecisionID != 1 {
		t.Fatalf("human detection = %#v, want project %v and decision decision-human", events[0].Data, projectID)
	}

	events, err = tracker.evaluateWith(ctx, s, start.Add(2*time.Nanosecond), detect)
	if err != nil {
		t.Fatalf("post-threshold evaluate: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("post-threshold events = %#v, want default decision and stale claim", events)
	}
	want := map[string]bool{
		store.EventDetectionDecisionDefaultUnapplied: false,
		store.EventDetectionClaimStale:               false,
	}
	for _, event := range events {
		if _, ok := want[event.Name]; !ok {
			t.Fatalf("unexpected event = %#v", event)
		}
		if event.Name == store.EventDetectionClaimStale {
			detection, ok := event.Data.(store.DetectionEvent)
			if !ok || detection.GoalID != goalID {
				t.Fatalf("stale detection = %#v, want goal %v", event.Data, goalID)
			}
		}
		want[event.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing event %v: %#v", name, events)
		}
	}

	if events, err := tracker.evaluateWith(ctx, s, start.Add(time.Hour), detect); err != nil {
		t.Fatalf("duplicate evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("duplicate events = %#v, want empty", events)
	}
}

func findDetectionEvent(events []store.DecisionEvent, name string, goalID int64) (store.DetectionEvent, bool) {
	for _, event := range events {
		if event.Name != name {
			continue
		}
		detection, ok := event.Data.(store.DetectionEvent)
		if ok && detection.GoalID == goalID {
			return detection, true
		}
	}
	return store.DetectionEvent{}, false
}
