package main

import (
	"strings"
	"testing"
)

func TestEmitWatchDecisionSuppressesUnchangedWakeupAndEmitsChangedContent(t *testing.T) {
	first := watchDecision{
		WakeupID:               "wakeup-1",
		ActionableGoalCount:    4,
		UnstartedTaskCount:     4,
		WaitingAnswerTaskCount: 0,
		UntouchedTaskCount:     4,
		WaitingAnswerCount:     24,
	}
	sameContent := first
	sameContent.WakeupID = "wakeup-2"
	changedContent := sameContent
	changedContent.WakeupID = "wakeup-3"
	changedContent.UntouchedTaskCount = 3
	changedContent.UnstartedTaskCount = 3

	var out strings.Builder
	delivered := make(map[watchDeliveryKey]struct{})
	var lastWakeupContent string
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	for _, decision := range []watchDecision{first, sameContent, changedContent} {
		if err := emitWatchDecisionWithState(&out, "wakeup", decision, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}

	got := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	want := []string{
		"atct wakeup: actionable_goals=4 unstarted_tasks=4 waiting_answer_tasks=0 untouched_tasks=4 delegated_tasks=0 waiting_answers=24",
		"atct wakeup: actionable_goals=4 unstarted_tasks=3 waiting_answer_tasks=0 untouched_tasks=3 delegated_tasks=0 waiting_answers=24",
	}
	if len(got) != len(want) {
		t.Fatalf("emitted lines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("emitted line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEmitWatchDecisionKeepsWakeupDeliveryPerWatch(t *testing.T) {
	decision := watchDecision{
		WakeupID:            "wakeup-1",
		ActionableGoalCount: 1,
		UnstartedTaskCount:  1,
		UntouchedTaskCount:  1,
	}

	var firstOut, secondOut strings.Builder
	firstDelivered := make(map[watchDeliveryKey]struct{})
	var firstLastWakeupContent string
	firstWakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	firstDetectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	secondDelivered := make(map[watchDeliveryKey]struct{})
	var secondLastWakeupContent string
	secondWakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	secondDetectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	if err := emitWatchDecisionWithState(&firstOut, "wakeup", decision, firstDelivered, &firstLastWakeupContent, firstWakeupDiscrepancyDelivered, firstDetectionDelivered); err != nil {
		t.Fatalf("first watch emitWatchDecision: %v", err)
	}
	if err := emitWatchDecisionWithState(&secondOut, "wakeup", decision, secondDelivered, &secondLastWakeupContent, secondWakeupDiscrepancyDelivered, secondDetectionDelivered); err != nil {
		t.Fatalf("second watch emitWatchDecision: %v", err)
	}

	want := "atct wakeup: actionable_goals=1 unstarted_tasks=1 waiting_answer_tasks=0 untouched_tasks=1 delegated_tasks=0 waiting_answers=0\n"
	if firstOut.String() != want {
		t.Fatalf("first watch output = %q, want %q", firstOut.String(), want)
	}
	if secondOut.String() != want {
		t.Fatalf("second watch output = %q, want %q", secondOut.String(), want)
	}
}

func TestEmitWatchDecisionEmitsWakeupWhenContentReturns(t *testing.T) {
	first := watchDecision{
		WakeupID:            "wakeup-1",
		ActionableGoalCount: 4,
		UnstartedTaskCount:  4,
		UntouchedTaskCount:  4,
	}
	changed := first
	changed.WakeupID = "wakeup-2"
	changed.UnstartedTaskCount = 3
	changed.UntouchedTaskCount = 3
	returned := first
	returned.WakeupID = "wakeup-3"

	var out strings.Builder
	delivered := make(map[watchDeliveryKey]struct{})
	var lastWakeupContent string
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	for _, decision := range []watchDecision{first, changed, returned} {
		if err := emitWatchDecisionWithState(&out, "wakeup", decision, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}

	want := strings.Join([]string{
		"atct wakeup: actionable_goals=4 unstarted_tasks=4 waiting_answer_tasks=0 untouched_tasks=4 delegated_tasks=0 waiting_answers=0",
		"atct wakeup: actionable_goals=4 unstarted_tasks=3 waiting_answer_tasks=0 untouched_tasks=3 delegated_tasks=0 waiting_answers=0",
		"atct wakeup: actionable_goals=4 unstarted_tasks=4 waiting_answer_tasks=0 untouched_tasks=4 delegated_tasks=0 waiting_answers=0",
	}, "\n") + "\n"
	if out.String() != want {
		t.Fatalf("emitted output = %q, want %q", out.String(), want)
	}
}
