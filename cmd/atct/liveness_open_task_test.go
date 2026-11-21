package main

import (
	"strings"
	"testing"
)

// A subcommander whose executor is still holding its task handoff has nothing
// to do: the executor reports when it is done. Prompting it anyway wakes it
// every minute to look at work that is already assigned.
func TestSubcommanderIsNotPromptedWhileItsExecutorHoldsTheTask(t *testing.T) {
	received := "2026-09-19T00:00:00Z"
	scope := watchScope{Role: "subcommander", ProjectID: "1", GoalID: "272"}
	state := watchReconciliation{
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "goal-272-handoff", GoalID: 272, RequestedAt: &received, ReceivedAt: &received},
		},
		TaskHandoffs: []watchReconciliationHandoff{
			{ID: "goal-272-task-1303", GoalID: 272, TaskID: 1303, RequestedAt: &received, ReceivedAt: &received},
		},
	}

	if watchLivenessActionable(scope, state) {
		t.Fatal("the subcommander was told to act while its executor still held the task handoff")
	}
}

// The same snapshot with the goal missing from the task handoff is what the
// daemon actually sent: the payload carried no goal for task handoffs, so the
// open handoff matched nothing and the subcommander was prompted forever.
func TestSubcommanderPromptNeedsTheGoalOnTaskHandoffs(t *testing.T) {
	received := "2026-09-19T00:00:00Z"
	scope := watchScope{Role: "subcommander", ProjectID: "1", GoalID: "272"}
	state := watchReconciliation{
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "goal-272-handoff", GoalID: 272, RequestedAt: &received, ReceivedAt: &received},
		},
		TaskHandoffs: []watchReconciliationHandoff{
			{ID: "goal-272-task-1303", TaskID: 1303, RequestedAt: &received, ReceivedAt: &received},
		},
	}

	if !watchLivenessActionable(scope, state) {
		t.Skip("a task handoff without its goal no longer looks like someone else's work")
	}
	t.Log("reproduced: without GoalID the open task handoff is invisible and the subcommander is prompted")
}

// An open task handoff means "somebody else is on it" only while that somebody
// is still there. Goal 272 sat for two hours with an open handoff whose
// executor had been dead the whole time, and suppressing on the handoff alone
// would have left its subcommander with nothing to tell it so.
func TestSubcommanderIsPromptedWhenItsExecutorIsGone(t *testing.T) {
	received := "2026-09-19T00:00:00Z"
	scope := watchScope{Role: "subcommander", ProjectID: "1", GoalID: "272"}
	state := watchReconciliation{
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "goal-272-handoff", GoalID: 272, RequestedAt: &received, ReceivedAt: &received},
		},
		TaskHandoffs: []watchReconciliationHandoff{
			{ID: "goal-272-task-1303", GoalID: 272, TaskID: 1303, RequestedAt: &received, ReceivedAt: &received, MonitorLost: true},
		},
	}

	if !watchLivenessActionable(scope, state) {
		t.Fatal("the subcommander was left silent while its executor's monitor was gone")
	}
}

// Goal 260 had a goal handoff waiting on review and a plan handoff that had
// been rejected. The scan returned at the first open handoff it matched, so
// the pending review answered "nothing to do" and the rejection behind it was
// never read. Its subcommander sat idle with a rejection to pick up.
func TestSubcommanderSeesARejectionBehindAPendingReview(t *testing.T) {
	at := "2026-09-19T00:00:00Z"
	scope := watchScope{Role: "subcommander", ProjectID: "1", GoalID: "260"}
	state := watchReconciliation{
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "goal-260-flow-monitor-e2e", GoalID: 260, RequestedAt: &at, ReceivedAt: &at, ReviewRequestedAt: &at},
		},
		PlanHandoffs: []watchReconciliationHandoff{
			{ID: "plan-260-executor-monitor-rebinding", GoalID: 260, RequestedAt: &at, ReceivedAt: &at, ReviewRequestedAt: &at, ReviewRejectedAt: &at},
		},
	}

	if !watchLivenessActionable(scope, state) {
		t.Fatal("a rejected plan handoff was missed because a pending review came first")
	}
}

// The pending review on its own still means wait.
func TestSubcommanderWaitsOnAPendingReviewWithNoRejection(t *testing.T) {
	at := "2026-09-19T00:00:00Z"
	scope := watchScope{Role: "subcommander", ProjectID: "1", GoalID: "260"}
	state := watchReconciliation{
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "goal-260-flow-monitor-e2e", GoalID: 260, RequestedAt: &at, ReceivedAt: &at, ReviewRequestedAt: &at},
		},
	}

	if watchLivenessActionable(scope, state) {
		t.Fatal("a review that is still being read was reported as work to do")
	}
}

// Goal 287 had a rejected plan handoff from the previous subcommander and an
// open goal handoff of its own. It reads as work to do, and the nudge it gets
// has to name the rejection: "recheck goal 287" told its subcommander nothing,
// and the subcommander concluded no transition was available.
func TestSubcommanderSeesAnInheritedRejection(t *testing.T) {
	at := "2026-09-19T00:00:00Z"
	scope := watchScope{Role: "subcommander", ProjectID: "1", GoalID: "287"}
	state := watchReconciliation{
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "goal-287-review-lifecycle", GoalID: 287, RequestedAt: &at, ReceivedAt: &at},
		},
		PlanHandoffs: []watchReconciliationHandoff{
			{ID: "plan-287-review-lifecycle-alignment", GoalID: 287, RequestedAt: &at, ReceivedAt: &at, ReviewRequestedAt: &at, ReviewRejectedAt: &at},
		},
	}

	if !watchLivenessActionable(scope, state) {
		t.Fatal("an inherited rejection is not reported as work to do")
	}
}

// The nudge has to say what to act on. "recheck goal 287" named nothing, and
// its subcommander rechecked, read the open goal handoff, and reported that no
// transition was available while the rejection waited.
func TestLivenessNudgeNamesTheRejectedHandoff(t *testing.T) {
	at := "2026-09-19T00:00:00Z"
	scope := watchScope{Role: "subcommander", ProjectID: "1", GoalID: "287"}
	state := watchReconciliation{
		PlanHandoffs: []watchReconciliationHandoff{
			{ID: "plan-287-review-lifecycle-alignment", GoalID: 287, RequestedAt: &at, ReceivedAt: &at, ReviewRequestedAt: &at, ReviewRejectedAt: &at},
		},
	}

	line := formatWatchLiveness(scope, state)
	for _, want := range []string{"plan-287-review-lifecycle-alignment", "rejected", "plan"} {
		if !strings.Contains(line, want) {
			t.Errorf("liveness line %q does not mention %q", line, want)
		}
	}

	// With nothing rejected it stays the plain nudge.
	plain := formatWatchLiveness(scope, watchReconciliation{})
	if strings.Contains(plain, "rejected") {
		t.Errorf("liveness line %q claims a rejection that is not there", plain)
	}
}
