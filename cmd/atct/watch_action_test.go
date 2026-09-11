package main

import (
	"context"
	"testing"
)

type watchAgentActionCase struct {
	name      string
	eventName string
	line      string
	decision  watchDecision
	want      bool
}

func frozenWatchAgentActionCases() []watchAgentActionCase {
	return []watchAgentActionCase{
		{name: "decision answered", eventName: "decision.answered", line: "atct decision answered (decision_id: 1)", decision: watchDecision{DecisionID: "1"}, want: true},
		{name: "decision approved", eventName: "decision.approved", line: "atct decision approved (decision_id: 1)", decision: watchDecision{DecisionID: "1"}, want: true},
		{name: "decision rejected", eventName: "decision.rejected", line: "atct decision rejected (decision_id: 1)", decision: watchDecision{DecisionID: "1"}, want: true},
		{name: "decision default applied", eventName: "decision.answered", line: "atct decision default applied (decision_id: 1)", decision: watchDecision{DecisionID: "1", DefaultAppliedAt: stringPtr("2026-09-07T00:00:00Z")}, want: false},
		{name: "decision pending", eventName: "decision.pending", line: "atct decision pending (decision_id: 1)", decision: watchDecision{DecisionID: "1"}, want: false},
		{name: "decision opened", eventName: "decision.opened", line: "atct decision opened (decision_id: 1)", decision: watchDecision{DecisionID: "1"}, want: false},
		{name: "goal created", eventName: "goal.created", line: "atct goal created (goal_id: 1)", decision: watchDecision{GoalID: "1"}, want: true},
		{name: "wakeup", eventName: "wakeup", line: "atct wakeup: actionable_goals=1", want: true},
		{name: "liveness", eventName: "monitor.liveness", line: "atct monitor liveness: recheck task 1", decision: watchDecision{GoalID: "249", TaskID: "1"}, want: true},
		{name: "goal detection", eventName: "detection.completion_report_missing", line: "atct detection: goal 1 has all tasks done but no completion report", decision: watchDecision{GoalID: "1"}, want: true},
		{name: "task handoff requested", eventName: "task.handoff.request", line: "atct task handoff requested (task_id: 1, handoff_id: h1)", decision: watchDecision{TaskID: "1", HandoffID: "h1"}, want: true},
		{name: "task handoff received", eventName: "task.handoff.receive", line: "atct task handoff received (task_id: 1, handoff_id: h1)", decision: watchDecision{TaskID: "1", HandoffID: "h1"}, want: true},
		{name: "task handoff completed", eventName: "task.handoff.complete", line: "atct task handoff completed (task_id: 1, handoff_id: h1)", decision: watchDecision{TaskID: "1", HandoffID: "h1"}, want: true},
		{name: "goal handoff requested", eventName: "goal.handoff.request", line: "atct goal handoff requested (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "goal handoff received", eventName: "goal.handoff.receive", line: "atct goal handoff received (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "goal handoff completed", eventName: "goal.handoff.complete", line: "atct goal handoff completed (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "task review requested", eventName: "task.handoff.review.request", line: "atct task handoff review requested (task_id: 1, handoff_id: h1)", decision: watchDecision{TaskID: "1", HandoffID: "h1"}, want: true},
		{name: "task review received", eventName: "task.handoff.review.receive", line: "atct task handoff review received (task_id: 1, handoff_id: h1)", decision: watchDecision{TaskID: "1", HandoffID: "h1"}, want: true},
		{name: "task review rejected", eventName: "task.handoff.review.reject", line: "atct task handoff review rejected (task_id: 1, handoff_id: h1)", decision: watchDecision{TaskID: "1", HandoffID: "h1"}, want: true},
		{name: "goal review requested", eventName: "goal.handoff.review.request", line: "atct goal handoff review requested (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "goal review received", eventName: "goal.handoff.review.receive", line: "atct goal handoff review received (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "goal review rejected", eventName: "goal.handoff.review.reject", line: "atct goal handoff review rejected (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "plan review requested", eventName: "plan.handoff.review.request", line: "atct plan handoff review requested (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "plan review received", eventName: "plan.handoff.review.receive", line: "atct plan handoff review received (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "plan review rejected", eventName: "plan.handoff.review.reject", line: "atct plan handoff review rejected (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: true},
		{name: "ordinary plan requested", eventName: "plan.handoff.request", line: "atct plan handoff requested (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: false},
		{name: "ordinary plan received", eventName: "plan.handoff.receive", line: "atct plan handoff received (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: false},
		{name: "ordinary plan completed", eventName: "plan.handoff.complete", line: "atct plan handoff completed (goal_id: 1, handoff_id: h1)", decision: watchDecision{GoalID: "1", HandoffID: "h1"}, want: false},
		{name: "reported", eventName: "handoff_reported", line: "atct handoff reported: task 1 (handoff h1): verified", decision: watchDecision{TaskID: "1", HandoffID: "h1"}, want: true},
		{name: "yielded", eventName: "handoff_yielded", line: "atct handoff yielded: task 1", decision: watchDecision{TaskID: "1"}, want: true},
		{name: "accepted task detection", eventName: "detection.unclaimed_doing", line: "atct detection: task 1 is doing without a work lock", decision: watchDecision{TaskID: "1"}, want: true},
		{name: "accepted task detection without handoff", eventName: "detection.claim_undelegated", line: "atct detection: task 1 has no handoff request", decision: watchDecision{TaskID: "1"}, want: true},
		{name: "accepted stale claim", eventName: "detection.claim_stale", line: "atct detection: task 1 has a stale claim", decision: watchDecision{TaskID: "1"}, want: true},
		{name: "lost monitor detection", eventName: "detection.monitor_lost", line: "atct detection: monitor for handoff h1 is lost; recover or replace its worker", decision: watchDecision{GoalID: "1", TaskID: "2", HandoffID: "h1"}, want: true},
		{name: "other task detection", eventName: "detection.handoff_unreceived", line: "atct detection: handoff h1 has no receipt", decision: watchDecision{HandoffID: "h1"}, want: false},
		{name: "wakeup discrepancy", eventName: "wakeup.discrepancy", line: "atct wakeup discrepancy: detector_unstarted_tasks=1 counted_unstarted_tasks=0", want: true},
		{name: "wakeup evaluation failure", eventName: "wakeup.evaluate_failed", line: "atct wakeup evaluate failed: timeout", want: true},
		{name: "keepalive", eventName: "keepalive", line: "atct watch: keepalive", want: false},
		{name: "reconnect diagnostic", eventName: "", line: "atct watch: connection unavailable; reconnecting in 5s", want: false},
		{name: "unknown raw", eventName: "", line: "unknown raw text", want: false},
	}
}

func stringPtr(value string) *string { return &value }

func TestWatchAgentActionSelectorMembership(t *testing.T) {
	for _, tc := range frozenWatchAgentActionCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := selectWatchAgentAction(tc.line, tc.eventName, tc.decision)
			if ok != tc.want {
				t.Fatalf("selectWatchAgentAction() ok = %v, want %v", ok, tc.want)
			}
			if !tc.want {
				return
			}
			if got.line != tc.line || got.eventName != tc.eventName || got.goalID != tc.decision.GoalID {
				t.Fatalf("action = %#v, want line=%q event=%q goal=%q", got, tc.line, tc.eventName, tc.decision.GoalID)
			}
		})
	}
}

func TestClaudeAndCodexAgentActionParity(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	bridge.SetActive(true)
	var claude []watchAgentAction
	claudeSink := watchAgentActionSink(func(action watchAgentAction) error {
		claude = append(claude, action)
		return nil
	})
	codexSink := bridge.ActionSinkWithContext(context.Background())

	for _, tc := range frozenWatchAgentActionCases() {
		action, ok := selectWatchAgentAction(tc.line, tc.eventName, tc.decision)
		if ok {
			if err := claudeSink(action); err != nil {
				t.Fatalf("Claude sink(%s): %v", tc.name, err)
			}
			if err := codexSink(action); err != nil {
				t.Fatalf("Codex sink(%s): %v", tc.name, err)
			}
		}
	}

	if len(claude) != bridge.QueueLen() {
		t.Fatalf("Claude actions = %d, Codex queue = %d", len(claude), bridge.QueueLen())
	}
	for i, action := range claude {
		queued := bridge.queue[i]
		if queued.line != action.line || queued.eventName != action.eventName || queued.goalID != action.goalID {
			t.Fatalf("Codex action %d = %#v, Claude action = %#v", i, queued, action)
		}
	}
}
