package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

const (
	wakeupPublishAfter = 15 * time.Minute
	// Three minutes sits just under the p75 of observed gaps between agent
	// writes (0.6 / 3.7 / 13.8 minutes for p50 / p75 / p90 over 708 events), so
	// it fires during normal work too. That is the price of dropping the Stop
	// hook, which used to be the only thing that could not be ignored.
	wakeupInitialWait                       = 3 * time.Minute
	wakeupResendInterval                    = 3 * time.Minute
	detectionHandoffUnreceivedAfter         = 30 * time.Minute
	detectionHandoffUnreportedAfter         = 30 * time.Minute
	detectionClaimUndelegatedAfter          = 30 * time.Minute
	detectionAnsweredDecisionUnappliedAfter = 0
	detectionDefaultDecisionUnappliedAfter  = 3 * time.Minute
	detectionStaleClaimAfter                = 3 * time.Minute
)

// wakeupTracker keeps the transition state that is intentionally not stored
// in SQLite. A condition must remain true for the grace period before it is
// published, and becoming false resets that period so a later occurrence gets
// a fresh wakeup ID.
type wakeupTracker struct {
	startedAt            time.Time
	activeSince          map[int64]time.Time
	published            map[int64]time.Time
	discrepancySeen      map[int64]bool
	detectionActiveSince map[string]time.Time
	detectionPublished   map[string]bool
}

func newWakeupTracker(startedAt time.Time) *wakeupTracker {
	return &wakeupTracker{
		startedAt:            startedAt,
		activeSince:          make(map[int64]time.Time),
		published:            make(map[int64]time.Time),
		discrepancySeen:      make(map[int64]bool),
		detectionActiveSince: make(map[string]time.Time),
		detectionPublished:   make(map[string]bool),
	}
}

func detectionTrackerKey(name string, targetID any) string {
	return name + "\x00" + fmt.Sprint(targetID)
}

func (t *wakeupTracker) publishDetection(now, startedAt time.Time, after time.Duration, name string, targetID any, projectID, goalID, taskID int64, handoffID string) (store.DecisionEvent, bool) {
	return t.publishDetectionWithDecision(now, startedAt, after, name, targetID, projectID, goalID, taskID, handoffID, 0)
}

func (t *wakeupTracker) publishDetectionWithDecision(now, startedAt time.Time, after time.Duration, name string, targetID any, projectID, goalID, taskID int64, handoffID string, decisionID int64) (store.DecisionEvent, bool) {
	key := detectionTrackerKey(name, targetID)
	trackedAt, ok := t.detectionActiveSince[key]
	if !ok {
		if startedAt.IsZero() {
			startedAt = now
		}
		t.detectionActiveSince[key] = startedAt
		delete(t.detectionPublished, key)
		trackedAt = startedAt
	} else if !startedAt.IsZero() && !trackedAt.Equal(startedAt) {
		t.detectionActiveSince[key] = startedAt
		delete(t.detectionPublished, key)
		trackedAt = startedAt
	}
	if t.detectionPublished[key] || now.Before(trackedAt.Add(after)) {
		return store.DecisionEvent{}, false
	}
	t.detectionPublished[key] = true
	return store.DecisionEvent{
		Name: name,
		Data: store.DetectionEvent{
			DetectionID: store.NewDetectionID(),
			DecisionID:  decisionID,
			ProjectID:   projectID,
			GoalID:      goalID,
			TaskID:      taskID,
			HandoffID:   handoffID,
		},
	}, true
}

func (t *wakeupTracker) evaluate(ctx context.Context, s *store.Store, now time.Time) ([]store.DecisionEvent, error) {
	return t.evaluateWith(ctx, s, now, s.DetectWakeup)
}

func (t *wakeupTracker) evaluateWith(ctx context.Context, s *store.Store, now time.Time, detect func(context.Context, int64) (store.WakeupState, error)) ([]store.DecisionEvent, error) {
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

		active := len(state.Tasks) > 0
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
							WakeupID:               store.NewWakeupID(),
							ProjectID:              project.ID,
							ActionableGoalCount:    state.ActionableGoalCount,
							UnstartedTaskCount:     state.UnstartedTaskCount,
							WaitingAnswerTaskCount: state.WaitingAnswerTaskCount,
							UntouchedTaskCount:     state.UntouchedTaskCount,
							DelegatedTaskCount:     state.DelegatedTaskCount,
							WaitingAnswerCount:     state.WaitingAnswerCount,
						},
					})
					t.published[project.ID] = now
				}
			}
		}

		recordDetection := func(name string, targetID any, startedAt time.Time, after time.Duration, goalID, taskID int64, handoffID string, decisionID int64) {
			currentDetectionKeys[detectionTrackerKey(name, targetID)] = struct{}{}
			if event, ok := t.publishDetectionWithDecision(now, startedAt, after, name, targetID, project.ID, goalID, taskID, handoffID, decisionID); ok {
				events = append(events, event)
			}
		}
		openTaskHandoffs := make(map[int64]*store.TaskHandoff)
		goalIDs := make(map[int64]struct{})
		for _, task := range state.UndelegatedClaims {
			if task.GoalID != 0 {
				goalIDs[task.GoalID] = struct{}{}
			}
		}
		for _, task := range state.StaleClaims {
			if task.GoalID != 0 {
				goalIDs[task.GoalID] = struct{}{}
			}
		}
		for goalID := range goalIDs {
			handoffs, err := s.ListOpenTaskHandoffsForGoal(ctx, goalID)
			if err != nil {
				return nil, err
			}
			for taskID, handoff := range handoffs {
				openTaskHandoffs[taskID] = handoff
			}
		}
		for _, goal := range state.CompletedGoals {
			recordDetection(store.EventDetectionCompletionReportMissing, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.CommitlessGoals {
			recordDetection(store.EventDetectionCommitsMissing, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.UndeclaredGoals {
			recordDetection(store.EventDetectionUndeclaredGoal, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.DroppedGoals {
			recordDetection(store.EventDetectionAllTasksDropped, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, task := range state.UnclaimedDoingTasks {
			recordDetection(store.EventDetectionUnclaimedDoing, task.ID, time.Time{}, wakeupPublishAfter, task.GoalID, task.ID, "", 0)
		}
		for _, handoff := range state.HandoffsAwaitingReceipt {
			if handoff.RequestedAt == nil {
				continue
			}
			goalID, err := s.GetTaskGoalID(ctx, handoff.TaskID)
			if err != nil {
				return nil, err
			}
			recordDetection(store.EventDetectionHandoffUnreceived, handoff.ID, *handoff.RequestedAt, detectionHandoffUnreceivedAfter, goalID, handoff.TaskID, handoff.ID, 0)
		}
		for _, handoff := range state.HandoffsAwaitingReport {
			if handoff.ReceivedAt == nil {
				continue
			}
			goalID, err := s.GetTaskGoalID(ctx, handoff.TaskID)
			if err != nil {
				return nil, err
			}
			recordDetection(store.EventDetectionHandoffUnreported, handoff.ID, *handoff.ReceivedAt, detectionHandoffUnreportedAfter, goalID, handoff.TaskID, handoff.ID, 0)
		}
		for _, handoff := range state.HandoffsReported {
			key := detectionTrackerKey(store.EventHandoffReported, handoff.ID)
			currentDetectionKeys[key] = struct{}{}
			if handoff.CompletedReportAt != nil && handoff.CompletedReportAt.Before(t.startedAt) {
				t.detectionActiveSince[key] = t.startedAt
				t.detectionPublished[key] = true
				continue
			}
			event, ok := t.publishDetectionWithDecision(now, time.Time{}, 0, store.EventHandoffReported, handoff.ID, project.ID, handoff.GoalID, handoff.TaskID, handoff.ID, 0)
			if !ok {
				continue
			}
			detection := event.Data.(store.DetectionEvent)
			detection.CompleteReport = handoff.CompleteReport
			event.Data = detection
			events = append(events, event)
		}
		for _, task := range state.UndelegatedClaims {
			claimedAt := taskHandoffClaimedAt(openTaskHandoffs[task.ID])
			if claimedAt == nil {
				continue
			}
			recordDetection(store.EventDetectionClaimUndelegated, task.ID, *claimedAt, detectionClaimUndelegatedAfter, task.GoalID, task.ID, "", 0)
		}
		for _, decision := range state.AnsweredUnappliedDecisions {
			recordDetection(store.EventDetectionDecisionAnsweredUnapplied, decision.ID, time.Time{}, detectionAnsweredDecisionUnappliedAfter, decision.GoalID, decision.TaskID, "", decision.ID)
		}
		for _, decision := range state.DefaultUnappliedDecisions {
			startedAt := time.Time{}
			if decision.DefaultAppliedAt != nil {
				startedAt = *decision.DefaultAppliedAt
			}
			recordDetection(store.EventDetectionDecisionDefaultUnapplied, decision.ID, startedAt, detectionDefaultDecisionUnappliedAfter, decision.GoalID, decision.TaskID, "", decision.ID)
		}
		for _, task := range state.StaleClaims {
			claimedAt := taskHandoffClaimedAt(openTaskHandoffs[task.ID])
			if claimedAt == nil {
				continue
			}
			recordDetection(store.EventDetectionClaimStale, task.ID, *claimedAt, detectionStaleClaimAfter, task.GoalID, task.ID, "", 0)
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
