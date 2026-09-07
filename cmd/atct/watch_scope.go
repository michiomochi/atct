package main

type watchScopeFilter struct {
	goalID      string
	taskID      string
	passThrough bool

	hasWakeupState      bool
	actionableGoalCount int
	unassignedGoalCount int
	unassignedGoalIDs   []int64
}

type watchScope struct{ ProjectID, GoalID, TaskID, Role, ScopeKey string }

func watchLivenessEligible(scope watchScope) bool {
	if scope.Role == "subcommander" {
		return scope.ProjectID != "" && scope.GoalID != "" && scope.TaskID == ""
	}
	if scope.Role == "executor" {
		return scope.ProjectID != "" && scope.GoalID != "" && scope.TaskID != ""
	}
	return false
}

func scopedOpenDecision(scope watchScope, state watchReconciliation) bool {
	for _, decision := range state.Decisions {
		if decision.Status == "open" && watchScopeMatchesDecision(scope, decision) {
			return true
		}
	}
	return false
}

func watchScopeMatchesDecision(scope watchScope, decision watchDecision) bool {
	if scope.ProjectID != "" && decision.ProjectID != "" && scope.ProjectID != decision.ProjectID {
		return false
	}
	if scope.TaskID != "" {
		return decision.TaskID == scope.TaskID
	}
	if scope.GoalID != "" {
		return decision.GoalID == scope.GoalID
	}
	return true
}

func newWatchScopeFilter(goalID string) *watchScopeFilter {
	return &watchScopeFilter{goalID: goalID}
}

func newWatchTaskScopeFilter(taskID string) *watchScopeFilter {
	return &watchScopeFilter{taskID: taskID}
}

func newWatchPassThroughFilter() *watchScopeFilter {
	return &watchScopeFilter{passThrough: true}
}

// Snapshot decisions come from /api/inbox, which the daemon does not scope by
// goal. Keep this separate from delivers because SSE keepalives and
// wakeup.evaluate_failed events must pass through without a goal ID.
func (f *watchScopeFilter) deliversSnapshotDecision(decision watchDecision) bool {
	if f.taskID != "" && decision.TaskID != f.taskID {
		return false
	}
	if f.goalID != "" && decision.GoalID != f.goalID {
		return false
	}
	return f.delivers("decision.answered", decision)
}

func (f *watchScopeFilter) delivers(eventName string, decision watchDecision) bool {
	if f.taskID != "" {
		return decision.TaskID == f.taskID
	}
	if f.passThrough || f.goalID != "" {
		return true
	}

	switch eventName {
	case "decision.approved", "decision.rejected", "goal.created",
		"wakeup.discrepancy", "wakeup.evaluate_failed",
		"detection.completion_report_missing", "detection.commits_missing",
		"detection.undeclared_goal", "detection.all_tasks_dropped":
		return true
	case "task.handoff.request", "task.handoff.receive",
		"task.handoff.review.request", "task.handoff.review.receive",
		"task.handoff.review.reject", "task.handoff.complete":
		return false
	case "goal.handoff.request", "goal.handoff.receive",
		"goal.handoff.review.request", "goal.handoff.review.receive",
		"goal.handoff.review.reject", "goal.handoff.complete",
		"plan.handoff.request", "plan.handoff.receive",
		"plan.handoff.review.request", "plan.handoff.review.receive",
		"plan.handoff.review.reject", "plan.handoff.complete":
		return true
	case "decision.answered":
		return !decision.defaultApplied()
	case "wakeup":
		if f.hasWakeupState && f.actionableGoalCount == decision.ActionableGoalCount &&
			f.unassignedGoalCount == decision.UnassignedGoalCount &&
			watchScopeGoalIDsEqual(f.unassignedGoalIDs, decision.UnassignedGoalIDs) {
			return false
		}
		f.hasWakeupState = true
		f.actionableGoalCount = decision.ActionableGoalCount
		f.unassignedGoalCount = decision.UnassignedGoalCount
		f.unassignedGoalIDs = append(f.unassignedGoalIDs[:0], decision.UnassignedGoalIDs...)
		return true
	case "handoff_reported":
		return decision.TaskID == ""
	case "handoff_yielded",
		"detection.unclaimed_doing", "detection.handoff_unreceived",
		"detection.handoff_unreported", "detection.claim_undelegated",
		"detection.claim_stale", "detection.decision_answered_unapplied",
		"detection.decision_default_unapplied":
		return false
	default:
		return true
	}
}

func watchScopeGoalIDsEqual(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
