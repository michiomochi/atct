package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWatchTaskScopeDeliversOnlyItsTaskHandoffAndDetection(t *testing.T) {
	filter := newWatchTaskScopeFilter("846")
	for _, eventName := range []string{"handoff_reported", "detection.claim_stale"} {
		if !filter.delivers(eventName, watchDecision{TaskID: "846"}) {
			t.Fatalf("task scope suppressed %s for its task", eventName)
		}
		if filter.delivers(eventName, watchDecision{TaskID: "847"}) {
			t.Fatalf("task scope delivered %s for another task", eventName)
		}
	}
	if filter.delivers("detection.claim_stale", watchDecision{}) {
		t.Fatal("task scope delivered a task-less detection")
	}
}

func TestWatchTaskScopeSuppressesTasklessWakeupEvaluateFailed(t *testing.T) {
	filter := newWatchTaskScopeFilter("846")

	if filter.delivers("wakeup.evaluate_failed", watchDecision{WakeupID: "wakeup-1", Reason: "database unavailable"}) {
		t.Fatal("task scope delivered a taskless wakeup.evaluate_failed event")
	}
}

func TestWatchScopeGoalReviewCompletionIsProjectOnly(t *testing.T) {
	decision := watchDecision{GoalID: "42"}
	if !newWatchScopeFilter("").delivers("goal.review.complete", decision) {
		t.Fatal("project scope suppressed goal.review.complete, want true")
	}
	if newWatchScopeFilter("42").delivers("goal.review.complete", decision) {
		t.Fatal("goal scope delivered goal.review.complete, want false")
	}
	if newWatchTaskScopeFilter("9").delivers("goal.review.complete", decision) {
		t.Fatal("task scope delivered goal.review.complete, want false")
	}
}

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
		"detection.monitor_lost",
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

func TestWatchScopeMatchesDecisionOwnerRole(t *testing.T) {
	if watchScopeMatchesDecision(watchScope{Role: "commander"}, watchDecision{TargetRole: "subcommander"}) {
		t.Fatal("commander matched a subcommander-owned decision")
	}
	if !watchScopeMatchesDecision(watchScope{Role: "subcommander", GoalID: "7"}, watchDecision{TargetRole: "subcommander", GoalID: "7"}) {
		t.Fatal("matching subcommander scope did not receive its decision")
	}
}

func TestWatchScopeGoalDeliversTaskScopedEvents(t *testing.T) {
	filter := newWatchScopeFilter("goal-1")
	cases := []struct {
		eventName string
		decision  watchDecision
	}{
		{"handoff_reported", watchDecision{TaskID: "task-1"}},
		{"detection.decision_answered_unapplied", watchDecision{DecisionID: "decision-1"}},
		{"detection.decision_default_unapplied", watchDecision{DecisionID: "decision-2"}},
		{"decision.answered", watchDecision{SettledByDefault: true}},
		{"detection.unclaimed_doing", watchDecision{TaskID: "task-1"}},
		{"detection.handoff_unreceived", watchDecision{HandoffID: "handoff-1"}},
		{"detection.handoff_unreported", watchDecision{HandoffID: "handoff-1"}},
		{"detection.monitor_lost", watchDecision{HandoffID: "handoff-1"}},
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

func TestWatchFormatsHandoffReviewEvents(t *testing.T) {
	cases := []struct {
		name      string
		eventName string
		decision  watchDecision
		want      string
	}{
		{"task request", "task.handoff.review.request", watchDecision{TaskID: "12", HandoffID: "task-handoff"}, "atct task handoff review requested (task_id: 12, handoff_id: task-handoff)"},
		{"plan request", "plan.handoff.review.request", watchDecision{GoalID: "7", HandoffID: "plan-handoff"}, "atct plan handoff review requested (goal_id: 7, handoff_id: plan-handoff)"},
		{"goal reject", "goal.handoff.review.reject", watchDecision{GoalID: "7", HandoffID: "goal-handoff"}, "atct goal handoff review rejected (goal_id: 7, handoff_id: goal-handoff)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := formatWatchDecision(tc.eventName, tc.decision)
			if !ok || got != tc.want {
				t.Fatalf("formatWatchDecision(%q) = %q, %v; want %q, true", tc.eventName, got, ok, tc.want)
			}
		})
	}
}

func TestHandoffOnlyProjectionRoutesRightRoleAndStops(t *testing.T) {
	at := func(value string) *string { return &value }
	cases := []struct {
		name      string
		kind      string
		handoff   watchReconciliationHandoff
		wantEvent string
		wantRole  string
	}{
		{name: "unreceived goal handoff wakes commander", kind: "goal", handoff: watchReconciliationHandoff{ID: "goal-request", GoalID: 7, RequestedAt: at("request")}, wantEvent: "goal.handoff.request", wantRole: "commander"},
		{name: "unreceived task handoff wakes subcommander", kind: "task", handoff: watchReconciliationHandoff{ID: "task-request", GoalID: 7, TaskID: 8, RequestedAt: at("request")}, wantEvent: "task.handoff.request", wantRole: "subcommander"},
		{name: "task review wakes reviewer", kind: "task", handoff: watchReconciliationHandoff{ID: "task-review", GoalID: 7, TaskID: 8, ReviewRequestedAt: at("review-request")}, wantEvent: "task.handoff.review.request", wantRole: "subcommander"},
		{name: "rejection receipt wakes original task submitter", kind: "task", handoff: watchReconciliationHandoff{ID: "task-reject-received", GoalID: 7, TaskID: 8, ReviewRejectedAt: at("reject"), ReviewRejectionReceivedAt: at("receipt")}, wantEvent: "task.handoff.review.reject.receive", wantRole: "executor"},
		{name: "plan rejection wakes plan submitter", kind: "plan", handoff: watchReconciliationHandoff{ID: "plan-reject", GoalID: 7, ReviewRejectedAt: at("reject")}, wantEvent: "plan.handoff.review.reject", wantRole: "subcommander"},
		{name: "goal review request wakes commander", kind: "goal", handoff: watchReconciliationHandoff{ID: "goal-review", GoalID: 7, ReviewRequestedAt: at("review-request")}, wantEvent: "goal.handoff.review.request", wantRole: "commander"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, decision, ok := watchReconciliationHandoffEvent(tc.kind, tc.handoff)
			if !ok || event != tc.wantEvent || decision.TargetRole != tc.wantRole {
				t.Fatalf("handoff projection = (%q, %+v, %v), want (%q, role=%q, true)", event, decision, ok, tc.wantEvent, tc.wantRole)
			}
			completed := tc.handoff
			completed.CompletedReportAt = at("complete")
			if event, _, ok := watchReconciliationHandoffEvent(tc.kind, completed); ok {
				t.Fatalf("completed handoff projected %q, want no action", event)
			}
		})
	}
}

func TestHandoffOnlyReconciliationProjectsTaskCreateUntilComplete(t *testing.T) {
	states := []string{
		`{"task_create_handoffs":[{"ID":"create-1","GoalID":7,"RequestedAt":"request"}]}`,
		`{"task_create_handoffs":[{"ID":"create-1","GoalID":7,"ReceivedAt":"receipt"}]}`,
		`{"task_create_handoffs":[{"ID":"create-1","GoalID":7,"CompletedAt":"complete"}]}`,
	}
	var calls int
	client := &http.Client{Transport: watchRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/events/reconcile" || calls >= len(states) {
			return nil, io.ErrUnexpectedEOF
		}
		body := states[calls]
		calls++
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	lastWakeupContent := ""
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	for range states {
		if err := reconcileWatchScope(context.Background(), client, "http://daemon", watchScope{ProjectID: "1", GoalID: "7", Role: "subcommander"}, &output, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, newWatchScopeFilter("7"), nil); err != nil {
			t.Fatalf("reconcileWatchScope: %v", err)
		}
	}
	want := "atct task-create handoff requested (goal_id: 7, handoff_id: create-1)\n" +
		"atct task-create handoff received (goal_id: 7, handoff_id: create-1)\n"
	if got := output.String(); got != want {
		t.Fatalf("task-create projection = %q, want %q", got, want)
	}
}

func TestWatchPlanReviewDeliveryUsesLifecycleGeneration(t *testing.T) {
	states := []string{
		`{"plan_handoffs":[{"ID":"plan-1","GoalID":7,"ReviewRequestedAt":"2026-09-06T00:00:00Z"}]}`,
		`{"plan_handoffs":[{"ID":"plan-1","GoalID":7,"ReviewRequestedAt":"2026-09-06T00:00:00Z"}]}`,
		`{"plan_handoffs":[{"ID":"plan-1","GoalID":7,"ReviewReceivedAt":"2026-09-06T00:01:00Z"}]}`,
		`{"plan_handoffs":[{"ID":"plan-1","GoalID":7,"ReviewReceivedAt":"2026-09-06T00:01:00Z"}]}`,
		`{"plan_handoffs":[{"ID":"plan-1","GoalID":7,"ReviewRejectedAt":"2026-09-06T00:02:00Z"}]}`,
		`{"plan_handoffs":[{"ID":"plan-1","GoalID":7,"ReviewRejectedAt":"2026-09-06T00:02:00Z"}]}`,
		`{"plan_handoffs":[{"ID":"plan-1","GoalID":7,"ReviewRequestedAt":"2026-09-06T00:03:00Z"}]}`,
		`{"plan_handoffs":[{"ID":"plan-1","GoalID":7,"ReviewRequestedAt":"2026-09-06T00:03:00Z"}]}`,
	}
	var calls int
	client := &http.Client{Transport: watchRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/events/reconcile" {
			return nil, io.ErrUnexpectedEOF
		}
		if calls >= len(states) {
			return nil, io.ErrUnexpectedEOF
		}
		body := states[calls]
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	lastWakeupContent := ""
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	for range states {
		if err := reconcileWatchScope(
			context.Background(), client, "http://daemon", watchScope{ProjectID: "project-1"}, &output,
			delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered,
			newWatchScopeFilter(""), nil,
		); err != nil {
			t.Fatalf("reconcileWatchScope: %v", err)
		}
	}

	want := strings.Join([]string{
		"atct plan handoff review requested (goal_id: 7, handoff_id: plan-1)",
		"atct plan handoff review received (goal_id: 7, handoff_id: plan-1)",
		"atct plan handoff review rejected (goal_id: 7, handoff_id: plan-1)",
		"atct plan handoff review requested (goal_id: 7, handoff_id: plan-1)",
	}, "\n") + "\n"
	if got := output.String(); got != want {
		t.Fatalf("plan review lifecycle output = %q, want %q", got, want)
	}
}

func TestWatchEventsURLOmitsDurableWatcherKey(t *testing.T) {
	got, err := watchEventsURLWithScope("http://daemon", watchScope{ProjectID: "1", GoalID: "2"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("watcher_key") != "" {
		t.Fatalf("watcher_key = %q, want it omitted", parsed.Query().Get("watcher_key"))
	}
}
