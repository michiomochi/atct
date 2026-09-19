package main

import "testing"

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
