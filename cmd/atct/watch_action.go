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
	controlOnly bool
}

type watchRawLineSink func(string) error
type watchAgentActionSink func(watchAgentAction) error

type monitorActionWriter struct {
	writer io.Writer
}

func (w monitorActionWriter) WriteAction(action watchAgentAction) error {
	if w.writer == nil || action.controlOnly {
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
	if strings.TrimSpace(line) == "" || eventName == "" {
		return watchAgentAction{}, false
	}
	if eventName == "decision.answered" && decision.defaultApplied() {
		return watchAgentAction{}, false
	}
	switch eventName {
	case "decision.pending", "decision.opened",
		"plan.handoff.request", "plan.handoff.receive", "plan.handoff.complete",
		"wakeup.handoff_unreceived", "wakeup.handoff_unreported", "keepalive":
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
		controlOnly: strings.HasSuffix(eventName, ".handoff.review.receive"),
	}, true
}
