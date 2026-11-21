package main

import "testing"

// The session that asked a decision stops and waits for the answer. When the
// decision is withdrawn instead, that answer is never coming, and nothing told
// it: the store publishes decision.withdrawn and the watch neither delivers
// nor renders it. Goal 260 waited on decision 801 that way, its five tasks
// done and its pane idle.
func TestWatchDeliversAWithdrawnDecision(t *testing.T) {
	filter := newWatchPassThroughFilter()
	decision := watchDecision{ID: "801", GoalID: "260", Status: "withdrawn"}

	if !filter.delivers("decision.withdrawn", decision) {
		t.Error("a withdrawn decision is not delivered; its asker waits for an answer that will never come")
	}
	line, ok := formatWatchDecision("decision.withdrawn", decision)
	if !ok {
		t.Fatal("a withdrawn decision has no line to show")
	}
	if line == "" {
		t.Error("the withdrawn line is empty")
	}
}

// A subcommander watches one goal, so the withdrawal has to reach that scope
// too: goal 260's asker was watching goal 260.
func TestWatchDeliversAWithdrawnDecisionToItsGoalScope(t *testing.T) {
	filter := newWatchScopeFilter("260")
	if !filter.delivers("decision.withdrawn", watchDecision{ID: "801", GoalID: "260", Status: "withdrawn"}) {
		t.Error("a goal-scoped watch never sees its own decision being withdrawn")
	}
	other := newWatchTaskScopeFilter("999")
	if other.delivers("decision.withdrawn", watchDecision{ID: "801", GoalID: "260", Status: "withdrawn"}) {
		t.Error("a task-scoped watch was given another scope's withdrawal")
	}
}
