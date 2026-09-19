package main

import "strconv"

type watchScopeFilter struct {
	goalID      string
	taskID      string
	passThrough bool

	hasWakeupState      bool
	actionableGoalCount int
	unassignedGoalCount int
	unassignedGoalIDs   []int64
}

type watchScope struct{ ProjectID, GoalID, TaskID, Role, ScopeKey, MonitorToken string }

func watchLivenessEligible(scope watchScope) bool {
	if scope.Role == "commander" {
		return scope.ProjectID != "" && scope.GoalID == "" && scope.TaskID == ""
	}
	if scope.Role == "subcommander" {
		return scope.ProjectID != "" && scope.GoalID != "" && scope.TaskID == ""
	}
	if scope.Role == "executor" {
		return scope.ProjectID != "" && scope.GoalID != "" && scope.TaskID != ""
	}
	return false
}

func watchLivenessActionable(scope watchScope, state watchReconciliation) bool {
	if !watchLivenessEligible(scope) {
		return false
	}
	switch scope.Role {
	case "commander":
		return watchCommanderLivenessActionable(state)
	case "subcommander":
		return watchSubcommanderLivenessActionable(scope, state)
	case "executor":
		return watchExecutorLivenessActionable(scope, state)
	default:
		return false
	}
}

func watchCommanderLivenessActionable(state watchReconciliation) bool {
	for _, handoffs := range [][]watchReconciliationHandoff{state.GoalHandoffs, state.PlanHandoffs} {
		for _, handoff := range handoffs {
			if watchHandoffOpen(handoff) && handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil {
				return true
			}
		}
	}
	for _, decision := range state.Decisions {
		if decision.Kind == "goal_review" && decision.Status == "applied" && decision.AnswerLabel == "approve" && watchReconciliationHasActiveGoal(state, decision.GoalID) {
			return true
		}
	}
	return false
}

func watchSubcommanderLivenessActionable(scope watchScope, state watchReconciliation) bool {
	for _, handoff := range state.TaskCreateHandoffs {
		if watchTaskCreateHandoffMatchesGoal(scope, handoff) && handoff.CompletedAt == nil {
			return true
		}
	}
	for _, handoff := range state.TaskHandoffs {
		if !watchHandoffMatchesGoal(scope, handoff) || !watchHandoffOpen(handoff) {
			continue
		}
		if handoff.ReviewRequestedAt != nil || handoff.ReviewRejectedAt != nil {
			return true
		}
	}
	if watchGoalHasOpenTaskHandoff(scope, state) {
		return false
	}
	for _, handoffs := range [][]watchReconciliationHandoff{state.GoalHandoffs, state.PlanHandoffs} {
		for _, handoff := range handoffs {
			if !watchHandoffMatchesGoal(scope, handoff) || !watchHandoffOpen(handoff) {
				continue
			}
			if handoff.ReviewRejectedAt != nil {
				return true
			}
			if handoff.ReviewRequestedAt != nil {
				return false
			}
			if handoff.ReceivedAt != nil {
				return true
			}
		}
	}
	return false
}

func watchExecutorLivenessActionable(scope watchScope, state watchReconciliation) bool {
	for _, handoff := range state.TaskHandoffs {
		if !watchHandoffMatchesTask(scope, handoff) || !watchHandoffOpen(handoff) || handoff.ReceivedAt == nil {
			continue
		}
		if handoff.ReviewRequestedAt == nil || handoff.ReviewRejectedAt != nil {
			return true
		}
	}
	return false
}

// watchGoalHasOpenTaskHandoff answers whether someone else is working on this
// goal's tasks. A handoff whose monitor is gone is nobody working: the
// subcommander has to hear about that one rather than be told to stand down.
func watchGoalHasOpenTaskHandoff(scope watchScope, state watchReconciliation) bool {
	for _, handoff := range state.TaskHandoffs {
		if watchHandoffMatchesGoal(scope, handoff) && watchHandoffOpen(handoff) && !handoff.MonitorLost {
			return true
		}
	}
	return false
}

func watchHandoffOpen(handoff watchReconciliationHandoff) bool {
	return handoff.CompletedReportAt == nil
}

func watchHandoffMatchesGoal(scope watchScope, handoff watchReconciliationHandoff) bool {
	return scope.GoalID == strconv.FormatInt(handoff.GoalID, 10)
}

func watchHandoffMatchesTask(scope watchScope, handoff watchReconciliationHandoff) bool {
	return watchHandoffMatchesGoal(scope, handoff) && scope.TaskID == strconv.FormatInt(handoff.TaskID, 10)
}

func watchTaskCreateHandoffMatchesGoal(scope watchScope, handoff watchTaskCreateHandoff) bool {
	return scope.GoalID == strconv.FormatInt(handoff.GoalID, 10)
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
	if decision.TargetRole != "" && decision.TargetRole != scope.Role {
		return false
	}
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
	if eventName == "goal.review.complete" || eventName == "goal.review.reject" {
		return !f.passThrough && f.goalID == ""
	}
	if f.passThrough || f.goalID != "" {
		return true
	}

	switch eventName {
	case "decision.approved", "decision.rejected", "goal.created",
		"wakeup.discrepancy", "wakeup.evaluate_failed",
		"wakeup.completion_report_missing", "wakeup.commits_missing",
		"wakeup.undeclared_goal", "wakeup.all_tasks_dropped",
		"orchestration.recovery":
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
	case "wakeup.unclaimed_doing", "wakeup.handoff_unreceived",
		"wakeup.handoff_unreported", "wakeup.claim_undelegated",
		"wakeup.monitor_lost",
		"wakeup.claim_stale", "wakeup.decision_answered_unapplied",
		"wakeup.decision_default_unapplied":
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
