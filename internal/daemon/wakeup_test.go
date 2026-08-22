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

func TestWakeupTrackerPublishesAfterGracePeriodAndResets(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "grace")
	if _, err := s.DeclareTasks(ctx, goalID, "agent", "grace-first", []string{"First task"}, []string{"Complete the first task."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	tracker := newWakeupTracker()
	start := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	initialWait := 10 * time.Minute
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
	if first.ProjectID != projectID || first.ActiveGoalCount != 1 || first.UnstartedTaskCount != 1 {
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

func TestWakeupTrackerRepublishesWhileConditionRemainsActive(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	_, goalID := newWakeupTestGoal(t, s, "repeat")
	if _, err := s.DeclareTasks(ctx, goalID, "agent", "repeat-tasks", []string{"Unstarted task"}, []string{"Keep the task unstarted."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	tracker := newWakeupTracker()
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
	// Start at the existing grace boundary so the baseline failure isolates missing resend behavior.
	firstAt := start.Add(wakeupPublishAfter)
	evaluateAt(firstAt, 1)
	resendInterval := 10 * time.Minute
	evaluateAt(firstAt.Add(resendInterval-time.Nanosecond), 0)
	evaluateAt(firstAt.Add(resendInterval), 1)
	evaluateAt(firstAt.Add(2*resendInterval), 1)
	evaluateAt(firstAt.Add(3*resendInterval), 1)
}

func TestWakeupTrackerReportsDetectorCountDiscrepancyOnce(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, goalID := newWakeupTestGoal(t, s, "discrepancy")
	if _, err := s.DeclareTasks(ctx, goalID, "agent", "discrepancy-tasks", []string{"Unstarted task"}, []string{"Start the task later."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	tracker := newWakeupTracker()
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

	tracker := newWakeupTracker()
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

	tracker := newWakeupTracker()
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
	newDaemonWithClock(s, func() time.Time { return now }).runMaintenance(ctx, newWakeupTracker(), now)

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

	tracker := newWakeupTracker()
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

	tracker := newWakeupTracker()
	start := time.Date(2026, 8, 20, 16, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	preGrace := start.Add(wakeupPublishAfter - time.Nanosecond)
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

	tracker := newWakeupTracker()
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

	tracker := newWakeupTracker()
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

	tracker := newWakeupTracker()
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
