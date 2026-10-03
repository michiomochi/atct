package main

import (
	"testing"
	"time"
)

func livenessSnapshotWithRejection(goalStatus string) watchReconciliation {
	return watchReconciliation{
		Goals: []watchReconciliationGoal{{ID: "273", Status: goalStatus}},
		TaskHandoffs: []watchReconciliationHandoff{{
			ID:                "goal-273-task-1307-20260919",
			GoalID:            273,
			TaskID:            1307,
			ReceivedAt:        stringPtr("2026-09-19T10:00:00.000000000Z"),
			ReviewRequestedAt: stringPtr("2026-09-19T11:00:00.000000000Z"),
			ReviewRejectedAt:  stringPtr("2026-09-19T12:00:00.000000000Z"),
		}},
	}
}

// An unchanged situation used to be re-announced every minute forever. Each
// prompt costs the woken agent a whole turn, so a goal nobody could advance
// spent its context repeating the same recheck.
func TestWatchLivenessDoesNotRepeatAnUnchangedPrompt(t *testing.T) {
	scope := watchScope{ProjectID: "1", Role: "executor", GoalID: "273", TaskID: "1307"}
	snapshot := livenessSnapshotWithRejection("active")
	start := time.Now()
	state := newWatchLivenessState(start)

	if _, ok := state.PromptDue(start.Add(2*time.Minute), scope, snapshot); !ok {
		t.Fatal("the first prompt for an actionable scope must be sent")
	}
	for minute := 3; minute <= 20; minute++ {
		if line, ok := state.PromptDue(start.Add(time.Duration(minute)*time.Minute), scope, snapshot); ok {
			t.Fatalf("minute %d repeated an unchanged prompt: %q", minute, line)
		}
	}
}

// A situation that changes is news, so it is sent even though the last prompt
// was recent.
func TestWatchLivenessSendsAChangedPrompt(t *testing.T) {
	scope := watchScope{ProjectID: "1", Role: "executor", GoalID: "273", TaskID: "1307"}
	start := time.Now()
	state := newWatchLivenessState(start)
	if _, ok := state.PromptDue(start.Add(2*time.Minute), scope, livenessSnapshotWithRejection("active")); !ok {
		t.Fatal("the first prompt must be sent")
	}

	changed := livenessSnapshotWithRejection("active")
	changed.TaskHandoffs[0].ReviewRejectionReceivedAt = stringPtr("2026-09-19T12:05:00.000000000Z")
	if _, ok := state.PromptDue(start.Add(4*time.Minute), scope, changed); !ok {
		t.Fatal("a changed situation must be sent")
	}
}

// A withdrawn goal has nothing for anyone to do. Its handoff stays open, so
// the scope read as actionable and kept waking a space whose goal was dropped.
func TestWatchLivenessStopsForAGoalThatIsNoLongerActive(t *testing.T) {
	for _, status := range []string{"dropped", "done", "proposed"} {
		t.Run(status, func(t *testing.T) {
			scope := watchScope{ProjectID: "1", Role: "executor", GoalID: "273", TaskID: "1307"}
			snapshot := livenessSnapshotWithRejection(status)
			state := newWatchLivenessState(time.Now())
			if line, ok := state.PromptDue(time.Now().Add(2*time.Minute), scope, snapshot); ok {
				t.Fatalf("a %s goal still prompted: %q", status, line)
			}
		})
	}
}
