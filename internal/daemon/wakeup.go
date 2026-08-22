package daemon

import (
	"context"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

const (
	wakeupPublishAfter   = 15 * time.Minute
	wakeupInitialWait    = 10 * time.Minute
	wakeupResendInterval = 10 * time.Minute
)

// wakeupTracker keeps the transition state that is intentionally not stored
// in SQLite. A condition must remain true for the grace period before it is
// published, and becoming false resets that period so a later occurrence gets
// a fresh wakeup ID.
type wakeupTracker struct {
	activeSince          map[string]time.Time
	published            map[string]time.Time
	discrepancySeen      map[string]bool
	detectionActiveSince map[string]time.Time
	detectionPublished   map[string]bool
}

func newWakeupTracker() *wakeupTracker {
	return &wakeupTracker{
		activeSince:          make(map[string]time.Time),
		published:            make(map[string]time.Time),
		discrepancySeen:      make(map[string]bool),
		detectionActiveSince: make(map[string]time.Time),
		detectionPublished:   make(map[string]bool),
	}
}

func detectionTrackerKey(name, targetID string) string {
	return name + "\x00" + targetID
}

func (t *wakeupTracker) publishDetection(now time.Time, name, targetID, projectID, goalID, taskID string) (store.DecisionEvent, bool) {
	key := detectionTrackerKey(name, targetID)
	startedAt, ok := t.detectionActiveSince[key]
	if !ok {
		t.detectionActiveSince[key] = now
		delete(t.detectionPublished, key)
		return store.DecisionEvent{}, false
	}
	if t.detectionPublished[key] || now.Before(startedAt.Add(wakeupPublishAfter)) {
		return store.DecisionEvent{}, false
	}
	t.detectionPublished[key] = true
	return store.DecisionEvent{
		Name: name,
		Data: store.DetectionEvent{
			DetectionID: store.NewDetectionID(),
			ProjectID:   projectID,
			GoalID:      goalID,
			TaskID:      taskID,
		},
	}, true
}

func (t *wakeupTracker) evaluate(ctx context.Context, s *store.Store, now time.Time) ([]store.DecisionEvent, error) {
	return t.evaluateWith(ctx, s, now, s.DetectWakeup)
}

func (t *wakeupTracker) evaluateWith(ctx context.Context, s *store.Store, now time.Time, detect func(context.Context, string) (store.WakeupState, error)) ([]store.DecisionEvent, error) {
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	var events []store.DecisionEvent
	currentDetectionKeys := make(map[string]struct{})
	for _, project := range projects {
		state, err := detect(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		counted, err := s.CountUnstartedTasksForWakeup(ctx, project.ID)
		if err != nil {
			return nil, err
		}

		mismatch := state.UnstartedTaskCount == 0 && counted > 0
		if mismatch {
			state, err = detect(ctx, project.ID)
			if err != nil {
				return nil, err
			}
			counted, err = s.CountUnstartedTasksForWakeup(ctx, project.ID)
			if err != nil {
				return nil, err
			}
			mismatch = state.UnstartedTaskCount == 0 && counted > 0
		}
		if mismatch {
			if !t.discrepancySeen[project.ID] {
				id := store.NewWakeupID()
				events = append(events, store.DecisionEvent{
					Name: store.EventWakeupDiscrepancy,
					Data: store.WakeupDiscrepancyEvent{
						WakeupID:                   id,
						ProjectID:                  project.ID,
						DetectorUnstartedTaskCount: state.UnstartedTaskCount,
						CountedUnstartedTaskCount:  counted,
					},
				})
				t.discrepancySeen[project.ID] = true
			}
		} else {
			delete(t.discrepancySeen, project.ID)
		}

		active := state.ActiveGoalCount > 0 && state.UnstartedTaskCount > 0
		if !active {
			delete(t.activeSince, project.ID)
			delete(t.published, project.ID)
		} else {
			startedAt, ok := t.activeSince[project.ID]
			if !ok {
				t.activeSince[project.ID] = now
				delete(t.published, project.ID)
			} else {
				lastPublishedAt, hasPublished := t.published[project.ID]
				shouldPublish := !hasPublished && !now.Before(startedAt.Add(wakeupInitialWait))
				if hasPublished {
					shouldPublish = !now.Before(lastPublishedAt.Add(wakeupResendInterval))
				}
				if shouldPublish {
					events = append(events, store.DecisionEvent{
						Name: store.EventWakeup,
						Data: store.WakeupEvent{
							WakeupID:           store.NewWakeupID(),
							ProjectID:          project.ID,
							ActiveGoalCount:    state.ActiveGoalCount,
							UnstartedTaskCount: state.UnstartedTaskCount,
							WaitingAnswerCount: state.WaitingAnswerCount,
						},
					})
					t.published[project.ID] = now
				}
			}
		}

		recordDetection := func(name, targetID, goalID, taskID string) {
			currentDetectionKeys[detectionTrackerKey(name, targetID)] = struct{}{}
			if event, ok := t.publishDetection(now, name, targetID, project.ID, goalID, taskID); ok {
				events = append(events, event)
			}
		}
		for _, goal := range state.CompletedGoals {
			recordDetection(store.EventDetectionCompletionReportMissing, goal.ID, goal.ID, "")
		}
		for _, goal := range state.CommitlessGoals {
			recordDetection(store.EventDetectionCommitsMissing, goal.ID, goal.ID, "")
		}
		for _, goal := range state.UndeclaredGoals {
			recordDetection(store.EventDetectionUndeclaredGoal, goal.ID, goal.ID, "")
		}
		for _, goal := range state.DroppedGoals {
			recordDetection(store.EventDetectionAllTasksDropped, goal.ID, goal.ID, "")
		}
		for _, task := range state.UnclaimedDoingTasks {
			recordDetection(store.EventDetectionUnclaimedDoing, task.ID, "", task.ID)
		}
	}
	for key := range t.detectionActiveSince {
		if _, ok := currentDetectionKeys[key]; !ok {
			delete(t.detectionActiveSince, key)
			delete(t.detectionPublished, key)
		}
	}
	for key := range t.detectionPublished {
		if _, ok := currentDetectionKeys[key]; !ok {
			delete(t.detectionPublished, key)
		}
	}
	return events, nil
}

func (d *Daemon) runMaintenance(ctx context.Context, tracker *wakeupTracker, now time.Time) {
	_, _ = d.store.ApplyExpiredDefaults(ctx, now)
	d.store.PublishEvent(store.DecisionEvent{
		Name: store.EventKeepalive,
		Data: store.KeepaliveEvent{At: now},
	})

	events, err := tracker.evaluate(ctx, d.store, now)
	if err != nil {
		return
	}
	for _, event := range events {
		d.store.PublishEvent(event)
	}
}
