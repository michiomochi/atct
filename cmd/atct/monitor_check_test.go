package main

import (
	"strings"
	"testing"
)

func TestMonitorCheckDenyUsesPreToolUseWire(t *testing.T) {
	got := monitorCheckDeny("no live Monitor")
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no live Monitor"}}`
	if got != want {
		t.Fatalf("monitorCheckDeny() = %s, want %s", got, want)
	}
}

func TestMonitorCheckGatesOnlyATCTTools(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"mcp__atct__atct_goal_claim", true},
		{"atct__atct_goal_claim", true},
		{"Bash", false},
		{"mcp__other__thing", false},
		{"", false},
	} {
		if got := isATCTTool(tc.name); got != tc.want {
			t.Errorf("isATCTTool(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMonitorCheckRejectsHookInputWithoutSession(t *testing.T) {
	if _, err := decodeMonitorHookInput(strings.NewReader(`{"tool_name":"mcp__atct__atct_role"}`)); err == nil {
		t.Fatal("decodeMonitorHookInput accepted input without session_id")
	}
}
