package main

import (
	"io"
	"strings"
)

// watchAgentAction is the canonical, already-formatted notification delivered
// to an agent-facing monitor. Transport adapters must not classify raw lines.
type watchAgentAction struct {
	line        string
	eventName   string
	goalID      string
	deliveryKey string
	generation  string
	targetRole  string
	scopeKey    string
}

type watchRawLineSink func(string) error
type watchAgentActionSink func(watchAgentAction) error

type monitorActionWriter struct {
	writer io.Writer
}

func (w monitorActionWriter) WriteAction(action watchAgentAction) error {
	if w.writer == nil {
		return nil
	}
	_, err := io.WriteString(w.writer, action.line+"\n")
	return err
}

func (w monitorActionWriter) Sink(action watchAgentAction) error {
	return w.WriteAction(action)
}

// selectWatchAgentAction owns the notification membership contract. Callers
// invoke it only after formatWatchDecision and delivery-state deduplication.
func selectWatchAgentAction(line, eventName string, decision watchDecision) (watchAgentAction, bool) {
	if strings.TrimSpace(line) == "" {
		return watchAgentAction{}, false
	}
	selected := false
	switch eventName {
	case "decision.approved", "decision.rejected":
		selected = true
	case "decision.answered":
		selected = !decision.defaultApplied()
	case "goal.created", "wakeup", "monitor.liveness", "handoff_reported", "handoff_yielded":
		selected = true
	case "detection.completion_report_missing", "detection.commits_missing", "detection.undeclared_goal", "detection.all_tasks_dropped":
		selected = true
	case "detection.unclaimed_doing", "detection.claim_undelegated", "detection.claim_stale":
		selected = true
	case "task.handoff.request", "task.handoff.receive", "task.handoff.complete",
		"task.handoff.review.request", "task.handoff.review.receive", "task.handoff.review.reject", "task.handoff.review.reject.receive",
		"goal.handoff.request", "goal.handoff.receive", "goal.handoff.complete",
		"goal.handoff.review.request", "goal.handoff.review.receive", "goal.handoff.review.reject", "goal.handoff.review.reject.receive",
		"plan.handoff.review.request", "plan.handoff.review.receive", "plan.handoff.review.reject", "plan.handoff.review.reject.receive",
		"task.create_handoff.request", "task.create_handoff.receive",
		"wakeup.discrepancy", "wakeup.evaluate_failed":
		selected = true
	}
	if !selected {
		return watchAgentAction{}, false
	}
	deliveryKey, generation := watchActionDeliveryIdentity(eventName, line, decision)
	return watchAgentAction{
		line:        line,
		eventName:   eventName,
		goalID:      decision.GoalID,
		deliveryKey: deliveryKey,
		generation:  generation,
		targetRole:  decision.TargetRole,
		scopeKey:    decision.ScopeKey,
	}, true
}
