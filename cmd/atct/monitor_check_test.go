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

func TestMonitorCheckExemptsSessionIdentify(t *testing.T) {
	for _, name := range []string{"mcp__atct__atct_session_identify", "atct__atct_session_identify"} {
		if !isATCTTool(name) {
			t.Fatalf("isATCTTool(%q) = false, want true", name)
		}
		if !isSessionIdentifyTool(name) {
			t.Errorf("isSessionIdentifyTool(%q) = false; gating it deadlocks an unregistered session", name)
		}
	}
	if isSessionIdentifyTool("mcp__atct__atct_goal_claim") {
		t.Error("isSessionIdentifyTool(atct_goal_claim) = true, want false")
	}
}
