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
	wakeupInitialWait                    = 3 * time.Minute
	wakeupResendInterval                 = 3 * time.Minute
	wakeupHandoffUnreceivedAfter         = 30 * time.Minute
	wakeupHandoffUnreportedAfter         = 30 * time.Minute
	wakeupClaimUndelegatedAfter          = 30 * time.Minute
	wakeupAnsweredDecisionUnappliedAfter = 0
	wakeupDefaultDecisionUnappliedAfter  = 3 * time.Minute
	wakeupStaleClaimAfter                = 3 * time.Minute
	wakeupMonitorLostAfter               = store.MonitorHealthLease
)

// wakeupTracker keeps the transition state that is intentionally not stored
// in SQLite. A wakeup must remain true for the grace period before it is
// published, and becoming false resets that period so a later occurrence gets
// a fresh wakeup ID.
type wakeupTracker struct {
	startedAt        time.Time
	wakeups          map[string]wakeupState
	discrepancySeen  map[int64]bool
	evaluateFailedID string
}

type wakeupState struct {
	activeSince   time.Time
	lastPublished time.Time
	published     bool
}

func newWakeupTracker(startedAt time.Time) *wakeupTracker {
	return &wakeupTracker{
		startedAt:       startedAt,
		wakeups:         make(map[string]wakeupState),
		discrepancySeen: make(map[int64]bool),
	}
}

func wakeupKey(name string, targetID any) string {
	return name + "\x00" + fmt.Sprint(targetID)
}

func (t *wakeupTracker) publishWakeup(now time.Time, key string, startedAt time.Time, after, resendInterval time.Duration) bool {
	wakeup, ok := t.wakeups[key]
	if !ok {
		if startedAt.IsZero() {
			startedAt = now
		}
		wakeup.activeSince = startedAt
	} else if !startedAt.IsZero() && !wakeup.activeSince.Equal(startedAt) {
		wakeup = wakeupState{activeSince: startedAt}
	}
	if now.Before(wakeup.activeSince.Add(after)) {
		t.wakeups[key] = wakeup
		return false
	}
	if !wakeup.published {
		wakeup.published = true
		wakeup.lastPublished = now
		t.wakeups[key] = wakeup
		return true
	}
	if resendInterval <= 0 || now.Before(wakeup.lastPublished.Add(resendInterval)) {
		t.wakeups[key] = wakeup
		return false
	}
	wakeup.lastPublished = now
	t.wakeups[key] = wakeup
	return true
}

func (t *wakeupTracker) evaluate(ctx context.Context, s *store.Store, now time.Time) ([]store.DecisionEvent, error) {
	return t.evaluateWith(ctx, s, now, s.EvaluateWakeup)
}

func (t *wakeupTracker) evaluateWith(ctx context.Context, s *store.Store, now time.Time, evaluateWakeup func(context.Context, int64) (store.WakeupState, error)) ([]store.DecisionEvent, error) {
	var events []store.DecisionEvent
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return events, err
	}

	currentWakeupKeys := make(map[string]struct{})
	var projectErrs []error
projectLoop:
	for _, project := range projects {
		state, err := evaluateWakeup(ctx, project.ID)
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
			state, err = evaluateWakeup(ctx, project.ID)
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
						WakeupID:                    id,
						ProjectID:                   project.ID,
						EvaluatedUnstartedTaskCount: state.UnstartedTaskCount,
						CountedUnstartedTaskCount:   counted,
					},
				})
				t.discrepancySeen[project.ID] = true
			}
		} else {
			delete(t.discrepancySeen, project.ID)
		}

		if len(state.Tasks) > 0 {
			key := wakeupKey("actionable", project.ID)
			currentWakeupKeys[key] = struct{}{}
			if t.publishWakeup(now, key, time.Time{}, wakeupInitialWait, wakeupResendInterval) {
				events = append(events, store.DecisionEvent{
					Name: store.EventWakeup,
					Data: store.ActionableWakeupEvent{
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

		recordWakeupEvent := func(name string, targetID any, startedAt time.Time, after time.Duration, goalID, taskID int64, handoffID string, decisionID int64) {
			key := wakeupKey(name, targetID)
			currentWakeupKeys[key] = struct{}{}
			if t.publishWakeup(now, key, startedAt, after, 0) {
				event := store.DecisionEvent{Name: name, Data: store.WakeupEvent{
					WakeupID: store.NewWakeupID(), DecisionID: decisionID, ProjectID: project.ID,
					GoalID: goalID, TaskID: taskID, HandoffID: handoffID,
				}}
				if name == store.EventWakeupHandoffUnreported {
					if data, ok := event.Data.(store.WakeupEvent); ok {
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
				recordWakeupEvent(store.EventWakeupMonitorLost, "goal:"+strconv.FormatInt(goal.ID, 10), lostAt, wakeupMonitorLostAfter, goal.ID, 0, "", 0)
			}
			handoffs, err := s.ListOpenTaskHandoffsForGoal(ctx, goal.ID)
			if err != nil {
				projectErrs = append(projectErrs, fmt.Errorf("project %d: %w", project.ID, err))
				continue projectLoop
			}
			for _, handoff := range handoffs {
				if lostAt, ok := lostMonitorAt(healthHistory, now, "executor", goal.ID, handoff.TaskID, handoff.ReceivedAt); ok {
					recordWakeupEvent(store.EventWakeupMonitorLost, "task:"+strconv.FormatInt(handoff.TaskID, 10), lostAt, wakeupMonitorLostAfter, goal.ID, handoff.TaskID, handoff.ID, 0)
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
			recordWakeupEvent(store.EventWakeupCompletionReportMissing, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.CommitlessGoals {
			recordWakeupEvent(store.EventWakeupCommitsMissing, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.UndeclaredGoals {
			recordWakeupEvent(store.EventWakeupUndeclaredGoal, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, goal := range state.DroppedGoals {
			recordWakeupEvent(store.EventWakeupAllTasksDropped, goal.ID, time.Time{}, wakeupPublishAfter, goal.ID, 0, "", 0)
		}
		for _, task := range state.UnclaimedDoingTasks {
			recordWakeupEvent(store.EventWakeupUnclaimedDoing, task.ID, time.Time{}, wakeupPublishAfter, task.GoalID, task.ID, "", 0)
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
			recordWakeupEvent(store.EventWakeupHandoffUnreceived, handoff.ID, *handoff.RequestedAt, wakeupHandoffUnreceivedAfter, goalID, handoff.TaskID, handoff.ID, 0)
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
			recordWakeupEvent(store.EventWakeupHandoffUnreported, handoff.ID, *handoff.ReceivedAt, wakeupHandoffUnreportedAfter, goalID, handoff.TaskID, handoff.ID, 0)
		}
		for _, task := range state.UndelegatedClaims {
			claimedAt := taskHandoffClaimedAt(openTaskHandoffs[task.ID])
			if claimedAt == nil {
				continue
			}
			recordWakeupEvent(store.EventWakeupClaimUndelegated, task.ID, *claimedAt, wakeupClaimUndelegatedAfter, task.GoalID, task.ID, "", 0)
		}
		for _, decision := range state.AnsweredUnappliedDecisions {
			recordWakeupEvent(store.EventWakeupDecisionAnsweredUnapplied, decision.ID, time.Time{}, wakeupAnsweredDecisionUnappliedAfter, decision.GoalID, decision.TaskID, "", decision.ID)
		}
		for _, decision := range state.DefaultUnappliedDecisions {
			startedAt := time.Time{}
			if decision.DefaultAppliedAt != nil {
				startedAt = *decision.DefaultAppliedAt
			}
			recordWakeupEvent(store.EventWakeupDecisionDefaultUnapplied, decision.ID, startedAt, wakeupDefaultDecisionUnappliedAfter, decision.GoalID, decision.TaskID, "", decision.ID)
		}
		for _, task := range state.StaleClaims {
			claimedAt := taskHandoffClaimedAt(openTaskHandoffs[task.ID])
			if claimedAt == nil {
				continue
			}
			recordWakeupEvent(store.EventWakeupClaimStale, task.ID, *claimedAt, wakeupStaleClaimAfter, task.GoalID, task.ID, "", 0)
		}
	}
	if len(projectErrs) > 0 {
		return events, errors.Join(projectErrs...)
	}
	for key := range t.wakeups {
		if _, ok := currentWakeupKeys[key]; !ok {
			delete(t.wakeups, key)
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
	d.runMaintenanceWith(ctx, tracker, now, d.store.EvaluateWakeup)
}

func (d *Daemon) runMaintenanceWith(ctx context.Context, tracker *wakeupTracker, now time.Time, evaluateWakeup func(context.Context, int64) (store.WakeupState, error)) {
	_, _ = d.store.ApplyExpiredDefaults(ctx, now)
	d.store.PublishEvent(store.DecisionEvent{
		Name: store.EventKeepalive,
		Data: store.KeepaliveEvent{At: now},
	})

	events, err := tracker.evaluateWith(ctx, d.store, now, evaluateWakeup)
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
