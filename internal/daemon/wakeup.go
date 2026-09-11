package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/domain"
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
	detectionMonitorLostAfter               = store.MonitorHealthLease
)

// wakeupTracker keeps the transition state that is intentionally not stored
// in SQLite. A condition must remain true for the grace period before it is
// published, and becoming false resets that period so a later occurrence gets
// a fresh wakeup ID.
type wakeupTracker struct {
	startedAt        time.Time
	conditions       map[string]wakeupConditionState
	discrepancySeen  map[int64]bool
	evaluateFailedID string
}

type wakeupConditionState struct {
	activeSince   time.Time
	lastPublished time.Time
	published     bool
}

func newWakeupTracker(startedAt time.Time) *wakeupTracker {
	return &wakeupTracker{
		startedAt:       startedAt,
		conditions:      make(map[string]wakeupConditionState),
		discrepancySeen: make(map[int64]bool),
	}
}

func wakeupConditionKey(name string, targetID any) string {
	return name + "\x00" + fmt.Sprint(targetID)
}

func (t *wakeupTracker) publishCondition(now time.Time, key string, startedAt time.Time, after, resendInterval time.Duration) bool {
	condition, ok := t.conditions[key]
	if !ok {
		if startedAt.IsZero() {
			startedAt = now
		}
		condition.activeSince = startedAt
	} else if !startedAt.IsZero() && !condition.activeSince.Equal(startedAt) {
		condition = wakeupConditionState{activeSince: startedAt}
	}
	if now.Before(condition.activeSince.Add(after)) {
		t.conditions[key] = condition
		return false
	}
	if !condition.published {
		condition.published = true
		condition.lastPublished = now
		t.conditions[key] = condition
		return true
	}
	if resendInterval <= 0 || now.Before(condition.lastPublished.Add(resendInterval)) {
		t.conditions[key] = condition
		return false
	}
	condition.lastPublished = now
	t.conditions[key] = condition
	return true
}

func (t *wakeupTracker) evaluate(ctx context.Context, s *store.Store, now time.Time) ([]store.DecisionEvent, error) {
	return t.evaluateWith(ctx, s, now, s.DetectWakeup)
}

func (t *wakeupTracker) evaluateWith(ctx context.Context, s *store.Store, now time.Time, detect func(context.Context, int64) (store.WakeupState, error)) ([]store.DecisionEvent, error) {
	var events []store.DecisionEvent
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return events, err
	}

	currentConditionKeys := make(map[string]struct{})
	var projectErrs []error
projectLoop:
	for _, project := range projects {
		state, err := detect(ctx, project.ID)
		if err != nil {
			projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
			continue projectLoop
		}
		counted, err := s.CountUnstartedTasksForWakeup(ctx, project.ID)
		if err != nil {
			projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
			continue projectLoop
		}

		mismatch := state.UnstartedTaskCount == 0 && counted > 0
		if mismatch {
			state, err = detect(ctx, project.ID)
			if err != nil {
				projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
				continue projectLoop
			}
			counted, err = s.CountUnstartedTasksForWakeup(ctx, project.ID)
			if err != nil {
				projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
				continue projectLoop
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

		if len(state.Tasks) > 0 {
			conditionKey := wakeupConditionKey("actionable", project.ID)
			currentConditionKeys[conditionKey] = struct{}{}
			if t.publishCondition(now, conditionKey, time.Time{}, wakeupInitialWait, wakeupResendInterval) {
				events = append(events, store.DecisionEvent{
					Name: store.EventWakeup,
					Data: store.WakeupEvent{
						WakeupID:               store.NewWakeupID(),
						ProjectID:              project.ID,
						ActionableGoalCount:    state.ActionableGoalCount,
						UnassignedGoalCount:    state.UnassignedGoalCount,
						UnassignedGoalIDs:      state.UnassignedGoalIDs,
						UnstartedTaskCount:     state.UnstartedTaskCount,
						WaitingAnswerTaskCount: state.WaitingAnswerTaskCount,
						UntouchedTaskCount:     state.UntouchedTaskCount,
						DelegatedTaskCount:     state.DelegatedTaskCount,
						WaitingAnswerCount:     state.WaitingAnswerCount,
					},
				})
			}
		}

		recordConditionEvent := func(name string, targetID any, startedAt time.Time, after time.Duration, goalID, taskID int64, handoffID string, decisionID int64) {
			conditionKey := wakeupConditionKey(name, targetID)
			currentConditionKeys[conditionKey] = struct{}{}
			if t.publishCondition(now, conditionKey, startedAt, after, 0) {
				event := store.DecisionEvent{Name: name, Data: store.DetectionEvent{
					DetectionID: store.NewDetectionID(), DecisionID: decisionID, ProjectID: project.ID,
					GoalID: goalID, TaskID: taskID, HandoffID: handoffID,
				}}
				if name == store.EventDetectionHandoffUnreported {
					if data, ok := event.Data.(store.DetectionEvent); ok {
						data.WorktreeActivity = handoffWorktreeActivity(ctx, project.RootPath, goalID, startedAt)
						event.Data = data
					}
				}
				events = append(events, event)
			}
		}
		healthHistory, err := s.ListMonitorHealthHistory(ctx, project.ID)
		if err != nil {
			projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
			continue projectLoop
		}
		goals, err := s.ListGoals(ctx, project.ID)
		if err != nil {
			projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
			continue projectLoop
		}
		for _, goal := range goals {
			if goal.Status != domain.GoalActive {
				continue
			}
			goalHandoffs, err := s.ListGoalHandoffs(ctx, goal.ID)
			if err != nil {
				projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
				continue projectLoop
			}
			var goalReceivedAt *time.Time
			for _, handoff := range goalHandoffs {
				if handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
					goalReceivedAt = handoff.ReceivedAt
					break
				}
			}
			if lostAt, ok := lostMonitorAt(healthHistory, now, "subcommander", goal.ID, 0, goalReceivedAt); ok {
				recordConditionEvent(store.EventDetectionMonitorLost, "goal:"+strconv.FormatInt(goal.ID, 10), lostAt, detectionMonitorLostAfter, goal.ID, 0, "", 0)
			}
			handoffs, err := s.ListOpenTaskHandoffsForGoal(ctx, goal.ID)
			if err != nil {
				projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
				continue projectLoop
			}
			for _, handoff := range handoffs {
				if lostAt, ok := lostMonitorAt(healthHistory, now, "executor", goal.ID, handoff.TaskID, handoff.ReceivedAt); ok {
					recordConditionEvent(store.EventDetectionMonitorLost, "task:"+strconv.FormatInt(handoff.TaskID, 10), lostAt, detectionMonitorLostAfter, goal.ID, handoff.TaskID, handoff.ID, 0)
				}
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
				projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
				continue projectLoop
			}
			for taskID, handoff := range handoffs {
				openTaskHandoffs[taskID] = handoff
			}
		}
		for _, goal := range state.CompletedGoals {
			recordConditionEvent(store.EventDetectionCompletionReportMissing, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.CommitlessGoals {
			recordConditionEvent(store.EventDetectionCommitsMissing, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.UndeclaredGoals {
			recordConditionEvent(store.EventDetectionUndeclaredGoal, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.DroppedGoals {
			recordConditionEvent(store.EventDetectionAllTasksDropped, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, task := range state.UnclaimedDoingTasks {
			recordConditionEvent(store.EventDetectionUnclaimedDoing, task.ID, time.Time{}, wakeupPublishAfter, task.GoalID, task.ID, "", 0)
		}
		for _, handoff := range state.HandoffsAwaitingReceipt {
			if handoff.RequestedAt == nil {
				continue
			}
			goalID, err := s.GetTaskGoalID(ctx, handoff.TaskID)
			if err != nil {
				projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
				continue projectLoop
			}
			recordConditionEvent(store.EventDetectionHandoffUnreceived, handoff.ID, *handoff.RequestedAt, detectionHandoffUnreceivedAfter, goalID, handoff.TaskID, handoff.ID, 0)
		}
		for _, handoff := range state.HandoffsAwaitingReport {
			if handoff.ReceivedAt == nil {
				continue
			}
			goalID, err := s.GetTaskGoalID(ctx, handoff.TaskID)
			if err != nil {
				projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
				continue projectLoop
			}
			recordConditionEvent(store.EventDetectionHandoffUnreported, handoff.ID, *handoff.ReceivedAt, detectionHandoffUnreportedAfter, goalID, handoff.TaskID, handoff.ID, 0)
		}
		for _, task := range state.UndelegatedClaims {
			claimedAt := taskHandoffClaimedAt(openTaskHandoffs[task.ID])
			if claimedAt == nil {
				continue
			}
			recordConditionEvent(store.EventDetectionClaimUndelegated, task.ID, *claimedAt, detectionClaimUndelegatedAfter, task.GoalID, task.ID, "", 0)
		}
		for _, decision := range state.AnsweredUnappliedDecisions {
			recordConditionEvent(store.EventDetectionDecisionAnsweredUnapplied, decision.ID, time.Time{}, detectionAnsweredDecisionUnappliedAfter, decision.GoalID, decision.TaskID, "", decision.ID)
		}
		for _, decision := range state.DefaultUnappliedDecisions {
			startedAt := time.Time{}
			if decision.DefaultAppliedAt != nil {
				startedAt = *decision.DefaultAppliedAt
			}
			recordConditionEvent(store.EventDetectionDecisionDefaultUnapplied, decision.ID, startedAt, detectionDefaultDecisionUnappliedAfter, decision.GoalID, decision.TaskID, "", decision.ID)
		}
		for _, task := range state.StaleClaims {
			claimedAt := taskHandoffClaimedAt(openTaskHandoffs[task.ID])
			if claimedAt == nil {
				continue
			}
			recordConditionEvent(store.EventDetectionClaimStale, task.ID, *claimedAt, detectionStaleClaimAfter, task.GoalID, task.ID, "", 0)
		}
	}
	if len(projectErrs) > 0 {
		return events, errors.Join(projectErrs...)
	}
	for key := range t.conditions {
		if _, ok := currentConditionKeys[key]; !ok {
			delete(t.conditions, key)
		}
	}
	return events, nil
}

func lostMonitorAt(history []store.MonitorHealth, now time.Time, role string, goalID, taskID int64, receivedAt *time.Time) (time.Time, bool) {
	if receivedAt == nil {
		return time.Time{}, false
	}
	var lastSeen time.Time
	for _, health := range history {
		if health.Role != role || health.GoalID == nil || *health.GoalID != goalID || health.LastSeenAt.Before(receivedAt.Add(-store.MonitorHealthLease)) {
			continue
		}
		if taskID == 0 {
			if health.TaskID != nil {
				continue
			}
		} else if health.TaskID == nil || *health.TaskID != taskID {
			continue
		}
		if health.StoppedAt == nil && !health.LastSeenAt.Before(now.Add(-store.MonitorHealthLease)) {
			return time.Time{}, false
		}
		if health.LastSeenAt.After(lastSeen) {
			lastSeen = health.LastSeenAt
		}
	}
	return lastSeen, !lastSeen.IsZero()
}

// handoffWorktreeActivity uses the same goal-derived worktree path and branch
// as script/worktree-setup.sh. An empty result means the activity is unknown.
func handoffWorktreeActivity(ctx context.Context, projectRoot string, goalID int64, receivedAt time.Time) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	goal8 := strconv.FormatInt(goalID, 10)
	if len(goal8) > 8 {
		goal8 = goal8[:8]
	}
	worktree := filepath.Join(projectRoot, ".worktrees", goal8)
	info, err := os.Stat(worktree)
	if err != nil || !info.IsDir() {
		return ""
	}

	status, err := runWakeupGit(ctx, worktree, "status", "--porcelain", "-z")
	if err != nil {
		return ""
	}
	for _, path := range porcelainPaths(string(status)) {
		if ctx.Err() != nil {
			return ""
		}
		info, err := os.Stat(filepath.Join(worktree, path))
		if err == nil && info.ModTime().After(receivedAt) {
			return "changed"
		}
	}

	commits, err := runWakeupGit(ctx, worktree, "log", "--format=%ct", "--after="+receivedAt.Format(time.RFC3339Nano), "wt/goal-"+goal8)
	if err != nil {
		return ""
	}
	for _, value := range strings.Fields(string(commits)) {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		if time.Unix(seconds, 0).After(receivedAt) {
			return "changed"
		}
	}
	return "unchanged"
}

func runWakeupGit(ctx context.Context, worktree string, args ...string) ([]byte, error) {
	gitArgs := append([]string{"-C", worktree}, args...)
	return exec.CommandContext(ctx, "git", gitArgs...).Output()
}

func porcelainPaths(output string) []string {
	entries := strings.Split(output, "\x00")
	paths := make([]string, 0, len(entries))
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}
		paths = append(paths, entry[3:])
		if entry[0] == 'R' || entry[0] == 'C' || entry[1] == 'R' || entry[1] == 'C' {
			index++
			if index < len(entries) && entries[index] != "" {
				paths = append(paths, entries[index])
			}
		}
	}
	return paths
}

func (d *Daemon) runMaintenance(ctx context.Context, tracker *wakeupTracker, now time.Time) {
	d.runMaintenanceWith(ctx, tracker, now, d.store.DetectWakeup)
}

func (d *Daemon) runMaintenanceWith(ctx context.Context, tracker *wakeupTracker, now time.Time, detect func(context.Context, int64) (store.WakeupState, error)) {
	_, _ = d.store.ApplyExpiredDefaults(ctx, now)
	d.store.PublishEvent(store.DecisionEvent{
		Name: store.EventKeepalive,
		Data: store.KeepaliveEvent{At: now},
	})

	events, err := tracker.evaluateWith(ctx, d.store, now, detect)
	for _, event := range events {
		d.store.PublishEvent(event)
	}
	if err != nil {
		if tracker.evaluateFailedID == "" {
			tracker.evaluateFailedID = store.NewWakeupID()
		}
		d.store.PublishEvent(store.DecisionEvent{
			Name: store.EventWakeupEvaluateFailed,
			Data: store.WakeupEvaluateFailedEvent{
				WakeupID: tracker.evaluateFailedID,
				Reason:   err.Error(),
			},
		})
		return
	}
	tracker.evaluateFailedID = ""
}
