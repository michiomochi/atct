package main

import (
	"strings"
	"testing"
	"time"
)

func activeGoals(ids ...string) []watchReconciliationGoal {
	goals := make([]watchReconciliationGoal, 0, len(ids))
	for _, id := range ids {
		goals = append(goals, watchReconciliationGoal{ID: id, Status: "active"})
	}
	return goals
}

func TestCommanderLivenessNamesEveryStoppedGoalInOneLine(t *testing.T) {
	at := func(value string) *string { return &value }
	state := watchReconciliation{
		Goals: activeGoals("7", "8"),
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "h7", GoalID: 7, ReviewRequestedAt: at("2026-01-01T00:00:01Z")},
			{ID: "h8", GoalID: 8, ReviewRequestedAt: at("2026-01-01T00:00:02Z")},
		},
	}
	got := formatWatchLiveness(commanderScope(1), state)
	want := "atct monitor liveness: goal handoff h7 is waiting for your review (goal 7); goal handoff h8 is waiting for your review (goal 8)"
	if got != want {
		t.Fatalf("liveness line = %q, want %q", got, want)
	}
}

func TestCommanderLivenessReceivedReviewNamesOpenBlockingDecisions(t *testing.T) {
	at := func(value string) *string { return &value }
	state := watchReconciliation{
		Goals: activeGoals("7"),
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "h7", GoalID: 7, ReviewRequestedAt: at("2026-01-01T00:00:01Z"), ReviewReceivedAt: at("2026-01-01T00:00:02Z")},
		},
		Decisions: []watchDecision{
			{ID: "5", Kind: "decision", Status: "open", GoalID: "7"},
			{ID: "3", Kind: "decision", Status: "open", GoalID: "7"},
		},
	}
	got := formatWatchLiveness(commanderScope(1), state)
	want := "atct monitor liveness: goal 7 review received, human review not requested yet (blocked by open decisions: 3, 5)"
	if got != want {
		t.Fatalf("liveness line = %q, want %q", got, want)
	}
}

func TestCommanderLivenessLeavesOutGoalWithOpenGoalReviewAndKeepsOthers(t *testing.T) {
	at := func(value string) *string { return &value }
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state := watchReconciliation{
		Goals: activeGoals("7", "8"),
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "h7", GoalID: 7, ReviewRequestedAt: at("2026-01-01T00:00:01Z"), ReviewReceivedAt: at("2026-01-01T00:00:02Z")},
			{ID: "h8", GoalID: 8, ReviewRequestedAt: at("2026-01-01T00:00:03Z")},
		},
		Decisions: []watchDecision{{ID: "2", Kind: "goal_review", Status: "open", GoalID: "7"}},
	}
	targets := watchCommanderLivenessTargets(state)
	if len(targets) != 1 || targets[0].goalID != "8" {
		t.Fatalf("targets = %+v, want only goal 8", targets)
	}
	// An open decision on goal 7 must not silence goal 8's prompt.
	s := newWatchLivenessState(start)
	line, due := s.PromptDue(start.Add(2*time.Minute), commanderScope(1), state)
	if !due || !strings.Contains(line, "goal handoff h8") || strings.Contains(line, "goal 7") {
		t.Fatalf("PromptDue = %q, %v; want goal 8's line only", line, due)
	}
}

func TestCommanderLivenessPostMergeIsNotBlockingButStillBlocksCompletion(t *testing.T) {
	at := func(value string) *string { return &value }
	state := watchReconciliation{
		Goals: activeGoals("7", "9"),
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "h7", GoalID: 7, ReviewRequestedAt: at("2026-01-01T00:00:01Z"), ReviewReceivedAt: at("2026-01-01T00:00:02Z")},
			{ID: "h9", GoalID: 9, ReviewRequestedAt: at("2026-01-01T00:00:03Z"), ReviewReceivedAt: at("2026-01-01T00:00:04Z")},
		},
		Decisions: []watchDecision{
			{ID: "1", Kind: "goal_review", Status: "applied", AnswerLabel: "approve", GoalID: "9"},
			{ID: "4", Kind: "post_merge", Status: "open", GoalID: "7"},
			{ID: "6", Kind: "post_merge", Status: "open", GoalID: "9"},
		},
	}
	got := formatWatchLiveness(commanderScope(1), state)
	want := "atct monitor liveness: goal 7 review received, human review not requested yet; goal 9 review was approved and is waiting to be completed (open decisions still block completion: 6)"
	if got != want {
		t.Fatalf("liveness line = %q, want %q", got, want)
	}
}

func TestCommanderLivenessDoesNotConfuseReceivedWithApprovedReview(t *testing.T) {
	at := func(value string) *string { return &value }
	state := watchReconciliation{
		Goals: activeGoals("7", "9"),
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "h7", GoalID: 7, ReviewRequestedAt: at("2026-01-01T00:00:01Z"), ReviewReceivedAt: at("2026-01-01T00:00:02Z")},
			{ID: "h9", GoalID: 9, ReviewRequestedAt: at("2026-01-01T00:00:03Z"), ReviewReceivedAt: at("2026-01-01T00:00:04Z")},
		},
		Decisions: []watchDecision{{ID: "1", Kind: "goal_review", Status: "applied", AnswerLabel: "approve", GoalID: "9"}},
	}
	got := formatWatchLiveness(commanderScope(1), state)
	want := "atct monitor liveness: goal 7 review received, human review not requested yet; goal 9 review was approved and is waiting to be completed"
	if got != want {
		t.Fatalf("liveness line = %q, want %q", got, want)
	}
}
