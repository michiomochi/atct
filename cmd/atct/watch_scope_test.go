package main

import "testing"

func TestWatchScopeSnapshotStopsOtherGoalDecision(t *testing.T) {
	filter := newWatchScopeFilter("goal-1")

	if got := filter.deliversSnapshotDecision(watchDecision{DecisionID: "other", GoalID: "goal-2"}); got {
		t.Fatal("goal scope delivered another goal's snapshot decision, want false")
	}
}

func TestWatchScopeSnapshotDeliversOwnGoalDecision(t *testing.T) {
	filter := newWatchScopeFilter("goal-1")

	if got := filter.deliversSnapshotDecision(watchDecision{DecisionID: "own", GoalID: "goal-1"}); !got {
		t.Fatal("goal scope suppressed its own snapshot decision, want true")
	}
}

func TestWatchScopeSnapshotStopsUnassignedDecision(t *testing.T) {
	filter := newWatchScopeFilter("goal-1")

	for _, goalID := range []string{"", "0"} {
		if got := filter.deliversSnapshotDecision(watchDecision{DecisionID: "unassigned", GoalID: goalID}); got {
			t.Fatalf("goal scope delivered snapshot decision with goal_id %q, want false", goalID)
		}
	}
}

func TestWatchScopeSnapshotWithoutGoalDeliversEveryGoal(t *testing.T) {
	filter := newWatchScopeFilter("")

	for _, goalID := range []string{"goal-1", "goal-2", "", "0"} {
		if got := filter.deliversSnapshotDecision(watchDecision{DecisionID: "decision", GoalID: goalID}); !got {
			t.Fatalf("unscoped watch suppressed snapshot decision with goal_id %q, want true", goalID)
		}
	}
}

func TestWatchScopeProjectStopsTaskHandoffReported(t *testing.T) {
	filter := newWatchScopeFilter("")

	if got := filter.delivers("handoff_reported", watchDecision{TaskID: "task-1"}); got {
		t.Fatal("project scope delivered task handoff_reported, want false")
	}
}

func TestWatchScopeProjectStopsHandoffYielded(t *testing.T) {
	filter := newWatchScopeFilter("")

	if got := filter.delivers("handoff_yielded", watchDecision{TaskID: "task-1"}); got {
		t.Fatal("project scope delivered handoff_yielded, want false")
	}
}

func TestWatchScopeProjectStopsUnappliedDecisionDetection(t *testing.T) {
	filter := newWatchScopeFilter("")

	for _, eventName := range []string{
		"detection.decision_answered_unapplied",
		"detection.decision_default_unapplied",
	} {
		if got := filter.delivers(eventName, watchDecision{DecisionID: "decision-1"}); got {
			t.Fatalf("project scope delivered %s, want false", eventName)
		}
	}
}

func TestWatchScopeProjectStopsDefaultDecisionAnswered(t *testing.T) {
	filter := newWatchScopeFilter("")
	defaultAppliedAt := "2026-08-28T00:00:00Z"
	cases := []watchDecision{
		{DecisionID: "decision-settled", SettledByDefault: true},
		{DecisionID: "decision-applied-at", DefaultAppliedAt: &defaultAppliedAt},
	}

	for _, decision := range cases {
		if got := filter.delivers("decision.answered", decision); got {
			t.Fatalf("project scope delivered default decision %s, want false", decision.DecisionID)
		}
	}
}

func TestWatchScopeProjectStopsTaskDetections(t *testing.T) {
	filter := newWatchScopeFilter("")
	events := []string{
		"detection.unclaimed_doing",
		"detection.handoff_unreceived",
		"detection.handoff_unreported",
		"detection.claim_undelegated",
		"detection.claim_stale",
	}

	for _, eventName := range events {
		if got := filter.delivers(eventName, watchDecision{TaskID: "task-1", HandoffID: "handoff-1"}); got {
			t.Fatalf("project scope delivered %s, want false", eventName)
		}
	}
}

func TestWatchScopeProjectSuppressesUnchangedWakeup(t *testing.T) {
	filter := newWatchScopeFilter("")
	first := watchDecision{
		ActionableGoalCount:    1,
		UnassignedGoalCount:    2,
		UnassignedGoalIDs:      []int64{3, 4},
		UntouchedTaskCount:     1,
		UnstartedTaskCount:     2,
		WaitingAnswerTaskCount: 3,
	}

	if got := filter.delivers("wakeup", first); !got {
		t.Fatal("first project wakeup was suppressed, want true")
	}

	taskOnlyChange := first
	taskOnlyChange.UntouchedTaskCount++
	if got := filter.delivers("wakeup", taskOnlyChange); got {
		t.Fatal("project wakeup with only task-level changes delivered, want false")
	}

	goalChange := taskOnlyChange
	goalChange.ActionableGoalCount++
	if got := filter.delivers("wakeup", goalChange); !got {
		t.Fatal("project wakeup with goal-level change suppressed, want true")
	}
}

func TestWatchScopeProjectDeliversGoalHandoffReported(t *testing.T) {
	filter := newWatchScopeFilter("")

	if got := filter.delivers("handoff_reported", watchDecision{GoalID: "goal-1"}); !got {
		t.Fatal("project scope suppressed goal handoff_reported, want true")
	}
}

func TestWatchScopeProjectDeliversDecisionApprovedAndRejected(t *testing.T) {
	filter := newWatchScopeFilter("")

	for _, eventName := range []string{"decision.approved", "decision.rejected"} {
		if got := filter.delivers(eventName, watchDecision{DecisionID: "decision-1"}); !got {
			t.Fatalf("project scope suppressed %s, want true", eventName)
		}
	}
}

func TestWatchScopeProjectDeliversHumanDecisionAnswered(t *testing.T) {
	filter := newWatchScopeFilter("")

	if got := filter.delivers("decision.answered", watchDecision{DecisionID: "decision-1"}); !got {
		t.Fatal("project scope suppressed human decision.answered, want true")
	}
}

func TestWatchScopeGoalDeliversTaskScopedEvents(t *testing.T) {
	filter := newWatchScopeFilter("goal-1")
	cases := []struct {
		eventName string
		decision  watchDecision
	}{
		{"handoff_reported", watchDecision{TaskID: "task-1"}},
		{"handoff_yielded", watchDecision{TaskID: "task-1"}},
		{"detection.decision_answered_unapplied", watchDecision{DecisionID: "decision-1"}},
		{"detection.decision_default_unapplied", watchDecision{DecisionID: "decision-2"}},
		{"decision.answered", watchDecision{SettledByDefault: true}},
		{"detection.unclaimed_doing", watchDecision{TaskID: "task-1"}},
		{"detection.handoff_unreceived", watchDecision{HandoffID: "handoff-1"}},
		{"detection.handoff_unreported", watchDecision{HandoffID: "handoff-1"}},
		{"detection.claim_undelegated", watchDecision{TaskID: "task-1"}},
		{"detection.claim_stale", watchDecision{TaskID: "task-1"}},
		{"wakeup", watchDecision{}},
	}

	for _, tc := range cases {
		if got := filter.delivers(tc.eventName, tc.decision); !got {
			t.Fatalf("goal scope suppressed %s, want true", tc.eventName)
		}
	}
}

func TestWatchScopeProjectDeliversGoalDetectionsAndCreated(t *testing.T) {
	filter := newWatchScopeFilter("")
	events := []string{
		"detection.completion_report_missing",
		"detection.commits_missing",
		"detection.undeclared_goal",
		"detection.all_tasks_dropped",
		"goal.created",
		"wakeup.discrepancy",
		"wakeup.evaluate_failed",
	}

	for _, eventName := range events {
		if got := filter.delivers(eventName, watchDecision{GoalID: "goal-1", WakeupID: "wakeup-1"}); !got {
			t.Fatalf("project scope suppressed %s, want true", eventName)
		}
	}
}

func TestWatchScopeProjectDeliversUnknownEvents(t *testing.T) {
	filter := newWatchScopeFilter("")

	if got := filter.delivers("detection.some_future_condition", watchDecision{}); !got {
		t.Fatal("project scope suppressed unknown event, want true")
	}
}

func TestWatchScopeFormatsDefaultAppliedDecisionLine(t *testing.T) {
	got, ok := formatWatchDecision("decision.answered", watchDecision{ID: "d1", SettledByDefault: true})
	if !ok {
		t.Fatal("formatWatchDecision() returned false, want true")
	}

	want := "atct decision default applied (decision_id: d1)"
	if got != want {
		t.Fatalf("formatWatchDecision() = %q, want %q", got, want)
	}
}

func TestWatchPassThroughFilterDeliversEverything(t *testing.T) {
	filter := newWatchPassThroughFilter()
	cases := []struct {
		eventName string
		decision  watchDecision
	}{
		{"handoff_reported", watchDecision{TaskID: "task-1"}},
		{"handoff_yielded", watchDecision{TaskID: "task-1"}},
		{"detection.decision_answered_unapplied", watchDecision{DecisionID: "decision-1"}},
		{"detection.decision_default_unapplied", watchDecision{DecisionID: "decision-2"}},
		{"detection.unclaimed_doing", watchDecision{TaskID: "task-1"}},
		{"detection.handoff_unreceived", watchDecision{HandoffID: "handoff-1"}},
		{"detection.claim_undelegated", watchDecision{TaskID: "task-1"}},
		{"detection.claim_stale", watchDecision{TaskID: "task-1"}},
	}

	for _, tc := range cases {
		if got := filter.delivers(tc.eventName, tc.decision); !got {
			t.Fatalf("pass-through filter suppressed %s, want true", tc.eventName)
		}
	}

	wakeup := watchDecision{
		ActionableGoalCount: 1,
		UnassignedGoalCount: 2,
		UnassignedGoalIDs:   []int64{3, 4},
	}
	for range 2 {
		if got := filter.delivers("wakeup", wakeup); !got {
			t.Fatal("pass-through filter suppressed wakeup, want true")
		}
	}
}
