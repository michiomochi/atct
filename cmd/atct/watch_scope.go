package main

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

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
	// A goal that is not active has nothing for anyone to do, whatever its
	// handoffs still look like. Withdrawing goal 294 left its goal handoff
	// open, so the scope stayed actionable and kept waking a space whose goal
	// had been dropped. A snapshot that does not carry the goal at all says
	// nothing either way, so it is left alone.
	if scope.GoalID != "" && watchReconciliationGoalStopped(state, scope.GoalID) {
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
	return len(watchCommanderLivenessTargets(state)) > 0
}

// watchCommanderTarget is one goal the commander is to act on. openDecisionIDs
// lists the open decisions that hold that goal back, and is filled only for the
// states that name them. decisionID is set only for an answered decision that
// is still unapplied.
type watchCommanderTarget struct {
	kind, handoffID, goalID string
	openDecisionIDs         []string
	decisionID              string
}

const (
	watchKindGoalReviewRejected        = "goal_review_rejected"
	watchKindGoalReviewReceived        = "goal_review_received"
	watchKindDecisionAnsweredUnapplied = "decision_answered_unapplied"
)

// watchLivenessNow is the clock the commander's unapplied-answer check reads;
// tests replace it.
var watchLivenessNow = time.Now

// watchCommanderLivenessTargets names every goal the commander is to act on, one
// entry per goal, in this order of priority: a goal whose human review was
// rejected and whose handoff the commander has yet to reject; a goal or plan
// handoff waiting for its review; a goal handoff whose review the commander has
// received but whose human review is not requested yet; a goal whose approved
// review is waiting to be completed. A goal with an open goal_review is the
// human's to look at now, so it is left out.
func watchCommanderLivenessTargets(state watchReconciliation) []watchCommanderTarget {
	reviewOpen := map[string]bool{}
	approved := map[string]bool{}
	for _, decision := range state.Decisions {
		if decision.Kind == "goal_review" && decision.Status == "open" {
			reviewOpen[decision.GoalID] = true
		}
		if decision.Kind == "goal_review" && decision.Status == "applied" && decision.AnswerLabel == "approve" {
			approved[decision.GoalID] = true
		}
	}
	var targets []watchCommanderTarget
	seen := map[string]bool{}
	add := func(target watchCommanderTarget) {
		if seen[target.goalID] || reviewOpen[target.goalID] {
			return
		}
		seen[target.goalID] = true
		targets = append(targets, target)
	}
	for _, target := range watchRejectedGoalReviewTargets(state) {
		add(target)
	}
	for _, group := range []struct {
		kind     string
		handoffs []watchReconciliationHandoff
	}{{"goal", state.GoalHandoffs}, {"plan", state.PlanHandoffs}} {
		for _, handoff := range group.handoffs {
			id := strconv.FormatInt(handoff.GoalID, 10)
			if watchHandoffOpen(handoff) && handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt == nil && handoff.ReviewRejectedAt == nil && !watchReconciliationGoalStopped(state, id) {
				add(watchCommanderTarget{kind: group.kind, handoffID: handoff.ID, goalID: id})
			}
		}
	}
	for _, handoff := range state.GoalHandoffs {
		id := strconv.FormatInt(handoff.GoalID, 10)
		if watchHandoffOpen(handoff) && handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt != nil && handoff.ReviewRejectedAt == nil && !approved[id] && watchReconciliationHasActiveGoal(state, id) {
			add(watchCommanderTarget{kind: watchKindGoalReviewReceived, handoffID: handoff.ID, goalID: id,
				openDecisionIDs: watchOpenDecisionIDs(state, id, "goal_review", "post_merge")})
		}
	}
	for _, decision := range state.Decisions {
		if decision.Kind == "goal_review" && decision.Status == "applied" && decision.AnswerLabel == "approve" && watchReconciliationHasActiveGoal(state, decision.GoalID) {
			add(watchCommanderTarget{kind: "goal", goalID: decision.GoalID,
				openDecisionIDs: watchOpenDecisionIDs(state, decision.GoalID, "goal_review")})
		}
	}
	return append(targets, watchUnappliedAnswerTargets(state)...)
}

// watchUnappliedAnswerTargets names each answered decision that has waited past
// store.AnsweredUnappliedAfter without being applied. One target per decision,
// in ascending decision id, and not deduplicated by goal: it sits beside the
// goal's review target.
func watchUnappliedAnswerTargets(state watchReconciliation) []watchCommanderTarget {
	type unapplied struct {
		id     int64
		target watchCommanderTarget
	}
	var found []unapplied
	for _, decision := range state.Decisions {
		if decision.Status != "answered" || decision.Kind == "goal_review" || decision.AnsweredAt == nil || !watchReconciliationHasActiveGoal(state, decision.GoalID) {
			continue
		}
		id, err := strconv.ParseInt(decision.ID, 10, 64)
		if err != nil {
			continue
		}
		answeredAt, err := time.Parse(time.RFC3339Nano, *decision.AnsweredAt)
		if err != nil {
			continue
		}
		ago := watchLivenessNow().Sub(answeredAt)
		if ago <= store.AnsweredUnappliedAfter {
			continue
		}
		found = append(found, unapplied{id: id, target: watchCommanderTarget{
			kind: watchKindDecisionAnsweredUnapplied, decisionID: decision.ID, goalID: decision.GoalID,
		}})
	}
	slices.SortFunc(found, func(a, b unapplied) int { return cmp.Compare(a.id, b.id) })
	targets := make([]watchCommanderTarget, 0, len(found))
	for _, f := range found {
		targets = append(targets, f.target)
	}
	return targets
}

// watchOpenDecisionIDs returns the ids of the goal's open decisions except the
// given kinds, in ascending order.
func watchOpenDecisionIDs(state watchReconciliation, goalID string, exceptKinds ...string) []string {
	var ids []int64
	for _, decision := range state.Decisions {
		if decision.GoalID != goalID || decision.Status != "open" || slices.Contains(exceptKinds, decision.Kind) {
			continue
		}
		if id, err := strconv.ParseInt(decision.ID, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out
}

// watchRejectedGoalReviewTargets finds every goal whose latest goal_review the
// human rejected while its goal handoff is still open with the review received.
// Each ends when atct_goal_handoff_review_reject stamps ReviewRejectedAt, and
// again once a resubmission opens a newer goal_review.
func watchRejectedGoalReviewTargets(state watchReconciliation) []watchCommanderTarget {
	latest := map[string]int64{}
	for _, decision := range state.Decisions {
		if id, err := strconv.ParseInt(decision.ID, 10, 64); err == nil && decision.Kind == "goal_review" && id > latest[decision.GoalID] {
			latest[decision.GoalID] = id
		}
	}
	var targets []watchCommanderTarget
	for _, decision := range state.Decisions {
		if decision.Kind != "goal_review" || decision.Status != "answered" || decision.AnswerLabel != "reject" || !watchReconciliationHasActiveGoal(state, decision.GoalID) {
			continue
		}
		if id, err := strconv.ParseInt(decision.ID, 10, 64); err != nil || id != latest[decision.GoalID] {
			continue
		}
		for _, handoff := range state.GoalHandoffs {
			// A resubmission clears ReviewRejectedAt on the same handoff while this
			// goal_review stays rejected, so a review requested after the answer is
			// the resubmission, not work still owed.
			if strconv.FormatInt(handoff.GoalID, 10) == decision.GoalID && watchHandoffOpen(handoff) &&
				handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt != nil && handoff.ReviewRejectedAt == nil &&
				!watchTimestampAfter(handoff.ReviewRequestedAt, decision.AnsweredAt) {
				targets = append(targets, watchCommanderTarget{kind: watchKindGoalReviewRejected, handoffID: handoff.ID, goalID: decision.GoalID})
				break
			}
		}
	}
	return targets
}

// watchTimestampAfter reports whether a is strictly later than b. A missing or
// unparsable timestamp is never later.
func watchTimestampAfter(a, b *string) bool {
	if a == nil || b == nil {
		return false
	}
	ta, errA := time.Parse(time.RFC3339Nano, *a)
	tb, errB := time.Parse(time.RFC3339Nano, *b)
	return errA == nil && errB == nil && ta.After(tb)
}

func watchSubcommanderLivenessActionable(scope watchScope, state watchReconciliation) bool {
	for _, handoff := range state.TaskCreateHandoffs {
		if watchTaskCreateHandoffMatchesGoal(scope, handoff) && watchTaskCreateHandoffOpen(handoff) {
			return true
		}
	}
	for _, handoff := range state.TaskHandoffs {
		if !watchHandoffMatchesGoal(scope, handoff) || !watchHandoffOpen(handoff) {
			continue
		}
		if handoff.RequestedAt != nil && handoff.ReceivedAt == nil {
			return true
		}
		if handoff.ReviewRequestedAt != nil || handoff.ReviewRejectedAt != nil {
			return true
		}
	}
	if watchGoalHasOpenTaskHandoff(scope, state) {
		return false
	}
	// A rejection is work wherever it sits, so look for one across every
	// handoff before anything else decides. Answering from the first open
	// handoff met let a goal handoff still under review say "nothing to do"
	// while a rejected plan handoff waited behind it, which is how goal 260
	// stood idle with a rejection to pick up.
	for _, handoffs := range [][]watchReconciliationHandoff{state.GoalHandoffs, state.PlanHandoffs} {
		for _, handoff := range handoffs {
			if !watchHandoffMatchesGoal(scope, handoff) || !watchHandoffOpen(handoff) {
				continue
			}
			if handoff.ReviewRejectedAt != nil {
				return true
			}
		}
	}
	for _, handoffs := range [][]watchReconciliationHandoff{state.GoalHandoffs, state.PlanHandoffs} {
		for _, handoff := range handoffs {
			if !watchHandoffMatchesGoal(scope, handoff) || !watchHandoffOpen(handoff) {
				continue
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
	return handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil
}

func watchTaskCreateHandoffOpen(handoff watchTaskCreateHandoff) bool {
	return handoff.CompletedAt == nil && handoff.RecoveredAt == nil
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

// watchCommanderObservedAnswer lets the project-wide commander watch hear the
// answer to a decision a subcommander asked, while the commander holds that
// goal's review. Such a decision targets the subcommander, so
// watchScopeMatchesDecision drops it for the commander, and the commander
// waiting on the review never learns it was answered. It returns a copy that
// targets the commander; the caller's decision is left as it was.
func watchCommanderObservedAnswer(scope watchScope, state watchReconciliation, decision watchDecision) (watchDecision, bool) {
	if scope.Role != "commander" || scope.GoalID != "" || scope.TaskID != "" {
		return watchDecision{}, false
	}
	if decision.Status != "answered" || decision.Kind == "goal_review" {
		return watchDecision{}, false
	}
	if decision.TargetRole == "" || decision.TargetRole == "commander" {
		return watchDecision{}, false
	}
	if decision.ProjectID != "" && decision.ProjectID != scope.ProjectID {
		return watchDecision{}, false
	}
	if !watchReconciliationHasActiveGoal(state, decision.GoalID) {
		return watchDecision{}, false
	}
	for _, handoff := range state.GoalHandoffs {
		if strconv.FormatInt(handoff.GoalID, 10) != decision.GoalID || !watchHandoffOpen(handoff) {
			continue
		}
		if handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt != nil && handoff.ReviewRejectedAt == nil {
			decision.TargetRole = "commander"
			return decision, true
		}
	}
	return watchDecision{}, false
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

// watchHandoffEntryIsLifecycleCopy reports a handoff entry that only repeats a
// lifecycle event the watcher already gets. Only request, received and
// completed entries are written by lifecycle code alone: the entry kinds that
// exist are the six in the schema CHECK, and validateHandoffEntryRequest lets a
// participant append only review_requested, review_received and review_rejected.
// The review receive ack is a review_received entry with the fixed body "received".
func watchHandoffEntryIsLifecycleCopy(decision watchDecision) bool {
	switch decision.Kind {
	case "request", "received", "completed":
		return true
	case "review_received":
		return decision.BodyPreview == "received"
	}
	return false
}

func (f *watchScopeFilter) delivers(eventName string, decision watchDecision) bool {
	if eventName == "handoff_entry_added" && watchHandoffEntryIsLifecycleCopy(decision) {
		return false
	}
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
	case "decision.withdrawn":
		// Scoped like an answer, because it ends the same wait.
		return true
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

// watchReconciliationGoalStopped reports that the snapshot carries this goal
// and it is not active. An absent goal is unknown, not stopped.
func watchReconciliationGoalStopped(state watchReconciliation, goalID string) bool {
	for _, goal := range state.Goals {
		if goal.ID == goalID {
			return goal.Status != "active"
		}
	}
	return false
}
