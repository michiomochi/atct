package main

import (
	"strings"
	"testing"
)

// An executor holding a rejected handoff is woken, but the wake-up has to say
// what is waiting. It used to read "recheck task 1307" forever: the task branch
// returned before anything looked for a rejection, so the one agent who could
// receive it was never told there was one.
func TestWatchLivenessNamesARejectionWaitingForAnExecutor(t *testing.T) {
	scope := watchScope{GoalID: "273", TaskID: "1307"}
	state := watchReconciliation{TaskHandoffs: []watchReconciliationHandoff{{
		ID:                "goal-273-task-1307-20260919",
		GoalID:            273,
		TaskID:            1307,
		ReceivedAt:        stringPtr("2026-09-19T10:00:00.000000000Z"),
		ReviewRequestedAt: stringPtr("2026-09-19T11:00:00.000000000Z"),
		ReviewRejectedAt:  stringPtr("2026-09-19T12:00:00.000000000Z"),
	}}}

	message := formatWatchLiveness(scope, state)
	if !strings.Contains(message, "goal-273-task-1307-20260919") {
		t.Fatalf("liveness message = %q, want the rejected handoff named", message)
	}
	if !strings.Contains(message, "rejected") {
		t.Fatalf("liveness message = %q, want it to say the handoff was rejected", message)
	}
}

// Once the rejection is received there is nothing waiting, so the message goes
// back to the ordinary recheck.
func TestWatchLivenessStopsNamingAReceivedRejection(t *testing.T) {
	scope := watchScope{GoalID: "273", TaskID: "1307"}
	state := watchReconciliation{TaskHandoffs: []watchReconciliationHandoff{{
		ID:                        "goal-273-task-1307-20260919",
		GoalID:                    273,
		TaskID:                    1307,
		ReceivedAt:                stringPtr("2026-09-19T10:00:00.000000000Z"),
		ReviewRequestedAt:         stringPtr("2026-09-19T11:00:00.000000000Z"),
		ReviewRejectedAt:          stringPtr("2026-09-19T12:00:00.000000000Z"),
		ReviewRejectionReceivedAt: stringPtr("2026-09-19T12:05:00.000000000Z"),
	}}}

	if message := formatWatchLiveness(scope, state); strings.Contains(message, "rejected") {
		t.Fatalf("liveness message = %q, want no rejection once it is received", message)
	}
}

// A task-scoped monitor speaks only for its own task, so a sibling task's
// rejection must not be put in front of it.
func TestWatchLivenessIgnoresASiblingTasksRejection(t *testing.T) {
	scope := watchScope{GoalID: "273", TaskID: "1307"}
	state := watchReconciliation{TaskHandoffs: []watchReconciliationHandoff{{
		ID:               "goal-273-task-1308-20260919",
		GoalID:           273,
		TaskID:           1308,
		ReceivedAt:       stringPtr("2026-09-19T10:00:00.000000000Z"),
		ReviewRejectedAt: stringPtr("2026-09-19T12:00:00.000000000Z"),
	}}}

	if message := formatWatchLiveness(scope, state); strings.Contains(message, "1308") {
		t.Fatalf("liveness message = %q, want no sibling task's rejection", message)
	}
}
