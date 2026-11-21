package main

import (
	"strings"
	"testing"
)

// answeredSubcommanderDecisionState is a commander holding goal 7's review:
// the subcommander asked a decision on goal 7, and the human has answered it.
func answeredSubcommanderDecisionState() (watchScope, watchReconciliation, watchDecision) {
	at := func(value string) *string { return &value }
	scope := commanderScope(1)
	state := watchReconciliation{
		Goals: activeGoals("7"),
		GoalHandoffs: []watchReconciliationHandoff{
			{ID: "h7", GoalID: 7, ReviewRequestedAt: at("2026-01-01T00:00:01Z"), ReviewReceivedAt: at("2026-01-01T00:00:02Z")},
		},
	}
	decision := watchDecision{
		ID: "5", DecisionID: "5", ProjectID: "1", Kind: "decision", Status: "answered",
		GoalID: "7", TargetRole: "subcommander",
	}
	return scope, state, decision
}

func TestWatchCommanderObservedAnswerReturnsCommanderCopy(t *testing.T) {
	scope, state, decision := answeredSubcommanderDecisionState()
	got, ok := watchCommanderObservedAnswer(scope, state, decision)
	if !ok {
		t.Fatal("watchCommanderObservedAnswer = false, want true for an answered subcommander decision on a goal under review")
	}
	if got.TargetRole != "commander" {
		t.Fatalf("copy TargetRole = %q, want commander", got.TargetRole)
	}
	if got.ID != decision.ID || got.Status != "answered" {
		t.Fatalf("copy = %+v, want the same decision answered", got)
	}
	if decision.TargetRole != "subcommander" {
		t.Fatalf("input decision TargetRole = %q, want subcommander (the copy must not change it)", decision.TargetRole)
	}
}

func TestWatchCommanderObservedAnswerRefusesWhenOneConditionFails(t *testing.T) {
	at := func(value string) *string { return &value }
	for name, flip := range map[string]func(*watchScope, *watchReconciliation, *watchDecision){
		"status open": func(_ *watchScope, _ *watchReconciliation, d *watchDecision) {
			d.Status = "open"
		},
		"handoff review not yet received": func(_ *watchScope, s *watchReconciliation, _ *watchDecision) {
			s.GoalHandoffs[0].ReviewReceivedAt = nil
		},
		"handoff review rejected": func(_ *watchScope, s *watchReconciliation, _ *watchDecision) {
			s.GoalHandoffs[0].ReviewRejectedAt = at("2026-01-01T00:00:03Z")
		},
		"goal not active": func(_ *watchScope, s *watchReconciliation, _ *watchDecision) {
			s.Goals = []watchReconciliationGoal{{ID: "7", Status: "done"}}
		},
		"other project": func(_ *watchScope, _ *watchReconciliation, d *watchDecision) {
			d.ProjectID = "2"
		},
		"kind goal_review": func(_ *watchScope, _ *watchReconciliation, d *watchDecision) {
			d.Kind = "goal_review"
		},
		"target role already commander": func(_ *watchScope, _ *watchReconciliation, d *watchDecision) {
			d.TargetRole = "commander"
		},
		"scope has a goal": func(s *watchScope, _ *watchReconciliation, _ *watchDecision) {
			s.GoalID = "7"
		},
		"subcommander scope": func(s *watchScope, _ *watchReconciliation, _ *watchDecision) {
			*s = watchScope{ProjectID: "1", GoalID: "7", Role: "subcommander"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			scope, state, decision := answeredSubcommanderDecisionState()
			flip(&scope, &state, &decision)
			if got, ok := watchCommanderObservedAnswer(scope, state, decision); ok {
				t.Fatalf("watchCommanderObservedAnswer = %+v, true; want false", got)
			}
		})
	}
}

func TestWatchCommanderWatchEmitsAnsweredSubcommanderDecision(t *testing.T) {
	body := `{"goals":[{"id":"7","status":"active"}],` +
		`"goal_handoffs":[{"ID":"h7","GoalID":7,"ReviewRequestedAt":"q","ReviewReceivedAt":"r"}],` +
		`"decisions":[{"id":"5","decision_id":"5","project_id":"1","kind":"decision","status":"answered","goal_id":"7","target_role":"subcommander"}]}`
	if got := reconcileOnce(t, commanderScope(1), body); !strings.Contains(got, "atct decision answered (decision_id: 5)") {
		t.Fatalf("commander watch output = %q, want the answered decision 5", got)
	}
	// The subcommander's own watch still gets the decision through the existing path.
	if got := reconcileOnce(t, watchScope{ProjectID: "1", GoalID: "7", Role: "subcommander"}, body); !strings.Contains(got, "atct decision answered (decision_id: 5)") {
		t.Fatalf("subcommander watch output = %q, want the answered decision 5", got)
	}
}
