package daemon

import (
	"context"
	"os"
	"path/filepath"
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

func newWakeupTestGoal(t *testing.T, s *store.Store, key string) (string, string) {
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

func insertWakeupOpenTaskHandoff(t *testing.T, s *store.Store, handoffID, taskID string, requestedAt, receivedAt *time.Time) {
	t.Helper()
	const sessionID = "wakeup-handoff-agent"
	if err := s.RegisterAgentSession(context.Background(), sessionID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
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
		t.Fatalf("insert task handoff %s: %v", handoffID, err)
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
		t.Fatalf("wakeupInitialWait = %s, want %s", wakeupInitialWait, initialWait)
	}
	if wakeupResendInterval != 3*time.Minute {
		t.Fatalf("wakeupResendInterval = %s, want 3m", wakeupResendInterval)
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
		t.Fatalf("published events = %#v, want one %q event", events, store.EventWakeup)
	}
	first, ok := events[0].Data.(store.WakeupEvent)
	if !ok {
		t.Fatalf("published data type = %T, want store.WakeupEvent", events[0].Data)
	}
	if first.ProjectID != projectID || first.ActionableGoalCount != 1 || first.UnstartedTaskCount != 1 {
		t.Fatalf("published wakeup = %+v, want project %s with one active goal and task", first, projectID)
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
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
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
		t.Fatalf("second published events = %#v, want one %q event", events, store.EventWakeup)
	}
	second := events[0].Data.(store.WakeupEvent)
	if second.WakeupID == first.WakeupID {
		t.Fatalf("second wakeup ID = %q, want a fresh ID after the condition reset", second.WakeupID)
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
		UnstartedTaskCount:     3,
		WaitingAnswerTaskCount: 1,
		UntouchedTaskCount:     2,
		WaitingAnswerCount:     2,
		Tasks:                  tasks[1:],
	}
	detect := func(context.Context, string) (store.WakeupState, error) {
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
		t.Fatalf("published events = %#v, want one %q event", events, store.EventWakeup)
	}
	wakeup, ok := events[0].Data.(store.WakeupEvent)
	if !ok {
		t.Fatalf("published data type = %T, want store.WakeupEvent", events[0].Data)
	}
	if wakeup.ProjectID != projectID || wakeup.ActionableGoalCount != state.ActionableGoalCount || wakeup.UnstartedTaskCount != state.UnstartedTaskCount {
		t.Fatalf("published totals = %+v, want project %s and state totals %+v", wakeup, projectID, state)
	}
	if wakeup.WaitingAnswerCount != state.WaitingAnswerCount {
		t.Fatalf("published waiting answer count = %d, want decision count %d", wakeup.WaitingAnswerCount, state.WaitingAnswerCount)
	}
	if wakeup.WaitingAnswerTaskCount != state.WaitingAnswerTaskCount || wakeup.UntouchedTaskCount != state.UntouchedTaskCount {
		t.Fatalf("published task breakdown = %+v, want state breakdown waiting=%d untouched=%d", wakeup, state.WaitingAnswerTaskCount, state.UntouchedTaskCount)
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
	detect := func(context.Context, string) (store.WakeupState, error) {
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
	detect := func(context.Context, string) (store.WakeupState, error) {
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
		t.Fatalf("actionable events = %#v, want one %q event", events, store.EventWakeup)
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
	detect := func(context.Context, string) (store.WakeupState, error) {
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
		t.Fatalf("resumed publish events = %#v, want one %q event", events, store.EventWakeup)
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
			t.Fatalf("evaluate at %s: %v", now, err)
		}
		if len(events) != wantEvents {
			t.Fatalf("events at %s = %#v, want %d event(s)", now, events, wantEvents)
		}
		for _, event := range events {
			if event.Name != store.EventWakeup {
				t.Fatalf("events at %s = %#v, want only %q", now, events, store.EventWakeup)
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
	detect := func(context.Context, string) (store.WakeupState, error) {
		return store.WakeupState{}, nil
	}
	events, err := tracker.evaluateWith(ctx, s, now, detect)
	if err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	if len(events) != 1 || events[0].Name != store.EventWakeupDiscrepancy {
		t.Fatalf("discrepancy events = %#v, want one %q event", events, store.EventWakeupDiscrepancy)
	}
	discrepancy, ok := events[0].Data.(store.WakeupDiscrepancyEvent)
	if !ok {
		t.Fatalf("discrepancy data type = %T, want store.WakeupDiscrepancyEvent", events[0].Data)
	}
	if discrepancy.ProjectID != projectID || discrepancy.DetectorUnstartedTaskCount != 0 || discrepancy.CountedUnstartedTaskCount != 1 {
		t.Fatalf("discrepancy = %+v, want detector 0 and counted 1 for project %s", discrepancy, projectID)
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
	if err := s.RegisterAgentSession(ctx, "running-goal-session", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "running-goal-session", projectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "running-goal-session"); err != nil {
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
	events, err := tracker.evaluateWith(ctx, s, now, func(ctx context.Context, detectedProjectID string) (store.WakeupState, error) {
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
			t.Fatalf("first maintenance event name = %q, want %q", event.Name, store.EventKeepalive)
		}
		keepalive, ok := event.Data.(store.KeepaliveEvent)
		if !ok {
			t.Fatalf("keepalive data type = %T, want store.KeepaliveEvent", event.Data)
		}
		if !keepalive.At.Equal(now) {
			t.Fatalf("keepalive time = %s, want %s", keepalive.At, now)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for keepalive event")
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
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
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
	if detection.ProjectID != projectID || detection.GoalID != goalID || detection.TaskID != "" || detection.DetectionID == "" {
		t.Fatalf("completion detection = %+v, want project %s and goal %s", detection, projectID, goalID)
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
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
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
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
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
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
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

	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, ""); err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}
	clearedEvents, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter+time.Minute))
	if err != nil {
		t.Fatalf("cleared evaluate: %v", err)
	}
	if cleared, ok := findDetectionEvent(clearedEvents, store.EventDetectionCompletionReportMissing, goalID); ok {
		t.Fatalf("cleared completion detection = %+v, want none", cleared)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
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
		t.Fatalf("second detection ID = %q, want a fresh ID after reset", second.DetectionID)
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
	if _, err := s.UpdateTask(ctx, tasksA[0].ID, domain.TaskDone, ""); err != nil {
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
	if _, err := s.UpdateTask(ctx, tasksB[0].ID, domain.TaskDone, ""); err != nil {
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
		t.Fatalf("goal A detection = %+v, want goal %s", detection, goalAID)
	}

	goalBEvents, err := tracker.evaluate(ctx, s, start.Add(25*time.Minute))
	if err != nil {
		t.Fatalf("goal B publish evaluate: %v", err)
	}
	if detection, ok := findDetectionEvent(goalBEvents, store.EventDetectionCompletionReportMissing, goalB.ID); !ok {
		t.Fatalf("goal B events = %#v, want completion detection", goalBEvents)
	} else if detection.GoalID != goalB.ID {
		t.Fatalf("goal B detection = %+v, want goal %s", detection, goalB.ID)
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
	detect := func(context.Context, string) (store.WakeupState, error) {
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
		projectID string
		goalID    string
		handoffID string
		taskID    string
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
			t.Fatalf("event %s data type = %T, want store.DetectionEvent", event.Name, event.Data)
		}
		if detection.ProjectID != expected.projectID || detection.GoalID != expected.goalID || detection.HandoffID != expected.handoffID || detection.TaskID != expected.taskID || detection.DetectionID == "" {
			t.Fatalf("event %s detection = %+v, want project=%s goal=%s handoff=%s task=%s", event.Name, detection, expected.projectID, expected.goalID, expected.handoffID, expected.taskID)
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

func TestWakeupTrackerPublishesReportedTaskAndGoalHandoffsImmediately(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "reported-handoffs")
	start := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	tracker := newWakeupTracker(start)
	taskCompletedAt := start.Add(time.Second)
	goalCompletedAt := start.Add(2 * time.Second)
	state := store.WakeupState{
		HandoffsReported: []store.ReportedHandoff{
			{ID: "reported-task", TaskID: "task-1", CompleteReport: "task report", CompletedReportAt: &taskCompletedAt},
			{ID: "reported-goal", GoalID: goalID, CompleteReport: "goal report", CompletedReportAt: &goalCompletedAt},
		},
	}
	detect := func(context.Context, string) (store.WakeupState, error) {
		return state, nil
	}

	events, err := tracker.evaluateWith(ctx, s, start, detect)
	if err != nil {
		t.Fatalf("reported handoff evaluate: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("reported handoff events = %#v, want task and goal reports", events)
	}
	want := map[string]struct {
		goalID         string
		taskID         string
		handoffID      string
		completeReport string
	}{
		"reported-task": {taskID: "task-1", handoffID: "reported-task", completeReport: "task report"},
		"reported-goal": {goalID: goalID, handoffID: "reported-goal", completeReport: "goal report"},
	}
	for _, event := range events {
		if event.Name != store.EventHandoffReported {
			t.Fatalf("unexpected event = %#v, want %q", event, store.EventHandoffReported)
		}
		detection, ok := event.Data.(store.DetectionEvent)
		if !ok {
			t.Fatalf("reported event data type = %T, want store.DetectionEvent", event.Data)
		}
		expected, ok := want[detection.HandoffID]
		if !ok {
			t.Fatalf("unexpected reported handoff = %+v", detection)
		}
		if detection.ProjectID != projectID || detection.GoalID != expected.goalID || detection.TaskID != expected.taskID || detection.HandoffID != expected.handoffID || detection.CompleteReport != expected.completeReport || detection.DetectionID == "" {
			t.Fatalf("reported detection = %+v, want project=%s goal=%s task=%s handoff=%s report=%q", detection, projectID, expected.goalID, expected.taskID, expected.handoffID, expected.completeReport)
		}
		delete(want, detection.HandoffID)
	}
	if len(want) != 0 {
		t.Fatalf("missing reported handoff events = %#v", want)
	}

	if events, err := tracker.evaluateWith(ctx, s, start.Add(time.Hour), detect); err != nil {
		t.Fatalf("duplicate reported handoff evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("duplicate reported handoff events = %#v, want empty", events)
	}
}

func TestDaemonNewTrackerUsesDaemonClock(t *testing.T) {
	s := newWakeupTestStore(t)
	startedAt := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	d := newDaemonWithClock(s, func() time.Time { return startedAt })

	tracker := d.newTracker()
	if !tracker.startedAt.Equal(startedAt) {
		t.Fatalf("tracker startedAt = %s, want %s", tracker.startedAt, startedAt)
	}
}

func TestDaemonNewTrackerSuppressesReportCompletedBeforeDaemonClock(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "daemon-tracker-reported-before-start")
	startedAt := time.Date(2026, 8, 25, 16, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(-time.Second)
	d := newDaemonWithClock(s, func() time.Time { return startedAt })
	tracker := d.newTracker()
	state := store.WakeupState{
		HandoffsReported: []store.ReportedHandoff{{
			ID:                "daemon-tracker-reported-before-start",
			GoalID:            goalID,
			CompleteReport:    "old report",
			CompletedReportAt: &completedAt,
		}},
	}
	detect := func(context.Context, string) (store.WakeupState, error) {
		return state, nil
	}

	events, err := tracker.evaluateWith(ctx, s, startedAt, detect)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want no events for pre-start report", events)
	}
}

func TestWakeupTrackerSuppressesReportedHandoffCompletedBeforeDaemonStart(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "reported-before-start")
	start := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	completedAt := start.Add(-time.Second)
	tracker := newWakeupTracker(start)
	state := store.WakeupState{
		HandoffsReported: []store.ReportedHandoff{{
			ID:                "reported-before-start",
			GoalID:            goalID,
			CompleteReport:    "old report",
			CompletedReportAt: &completedAt,
		}},
	}
	detect := func(context.Context, string) (store.WakeupState, error) {
		return state, nil
	}

	for _, now := range []time.Time{start, start.Add(time.Hour)} {
		events, err := tracker.evaluateWith(ctx, s, now, detect)
		if err != nil {
			t.Fatalf("evaluate at %s: %v", now, err)
		}
		if len(events) != 0 {
			t.Fatalf("events at %s = %#v, want no events for pre-start report", now, events)
		}
	}
}

func TestWakeupTrackerPublishesOnlyReportedHandoffCompletedAfterDaemonStart(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "reported-mixed")
	start := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
	oldCompletedAt := start.Add(-time.Second)
	newCompletedAt := start.Add(time.Second)
	tracker := newWakeupTracker(start)
	state := store.WakeupState{
		HandoffsReported: []store.ReportedHandoff{
			{ID: "reported-old", GoalID: goalID, CompleteReport: "old report", CompletedReportAt: &oldCompletedAt},
			{ID: "reported-new", GoalID: goalID, CompleteReport: "new report", CompletedReportAt: &newCompletedAt},
		},
	}
	detect := func(context.Context, string) (store.WakeupState, error) {
		return state, nil
	}

	events, err := tracker.evaluateWith(ctx, s, start, detect)
	if err != nil {
		t.Fatalf("mixed reported handoff evaluate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("mixed reported handoff events = %#v, want one event", events)
	}
	if events[0].Name != store.EventHandoffReported {
		t.Fatalf("mixed reported handoff event = %#v, want %q", events[0], store.EventHandoffReported)
	}
	detection, ok := events[0].Data.(store.DetectionEvent)
	if !ok {
		t.Fatalf("mixed reported handoff data type = %T, want store.DetectionEvent", events[0].Data)
	}
	if detection.ProjectID == "" || detection.GoalID != goalID || detection.HandoffID != "reported-new" || detection.CompleteReport != "new report" || detection.DetectionID == "" {
		t.Fatalf("mixed reported handoff detection = %+v, want only new report", detection)
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
		AnsweredUnappliedDecisions: []domain.Decision{{ID: "decision-human", GoalID: goalID}},
		DefaultUnappliedDecisions: []domain.Decision{{
			ID:               "decision-default",
			GoalID:           goalID,
			DefaultAppliedAt: &defaultAppliedAt,
		}},
		StaleClaims: []domain.Task{{ID: tasks[0].ID, GoalID: goalID}},
	}
	detect := func(context.Context, string) (store.WakeupState, error) {
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
	if !ok || humanDetection.ProjectID != projectID || humanDetection.DecisionID != "decision-human" {
		t.Fatalf("human detection = %#v, want project %s and decision decision-human", events[0].Data, projectID)
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
				t.Fatalf("stale detection = %#v, want goal %s", event.Data, goalID)
			}
		}
		want[event.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing event %s: %#v", name, events)
		}
	}

	if events, err := tracker.evaluateWith(ctx, s, start.Add(time.Hour), detect); err != nil {
		t.Fatalf("duplicate evaluate: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("duplicate events = %#v, want empty", events)
	}
}

func findDetectionEvent(events []store.DecisionEvent, name, goalID string) (store.DetectionEvent, bool) {
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
