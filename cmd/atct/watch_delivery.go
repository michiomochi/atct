package main

import (
	"context"
	"fmt"
	"strings"
)

func newWatchCodexActionSink(bridge *codexMonitorBridge) watchAgentActionSink {
	if bridge == nil {
		return nil
	}
	return func(action watchAgentAction) error {
		return bridge.enqueueAction(context.Background(), codexMonitorAction{line: action.line, eventName: action.eventName, goalID: action.goalID, deliveryKey: action.deliveryKey})
	}
}

func watchActionDeliveryIdentity(eventName, line string, decision watchDecision) (string, string) {
	subject := line
	switch {
	case strings.HasPrefix(eventName, "decision."):
		subject = decision.DecisionID
	case strings.Contains(eventName, ".handoff."):
		subject = decision.HandoffID
	case strings.HasPrefix(eventName, "detection."):
		subject = decision.DetectionID
	case eventName == "goal.created", eventName == "monitor.liveness":
		subject = decision.GoalID
	case strings.HasPrefix(eventName, "wakeup"):
		subject = decision.wakeupID()
	}
	if subject == "" {
		subject = line
	}
	generation := decision.deliveryGeneration
	if generation == "" {
		generation = decision.Generation
	}
	if generation == "" {
		if strings.HasPrefix(eventName, "decision.") {
			generation = fmt.Sprintf("default:%t", decision.defaultApplied())
		} else {
			generation = "current"
		}
	}
	return strings.Join([]string{eventName, decision.TargetRole, subject}, "\x00"), generation
}
