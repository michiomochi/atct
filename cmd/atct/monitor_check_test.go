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
		if !isMonitorExemptTool(name) {
			t.Errorf("isMonitorExemptTool(%q) = false; gating it deadlocks an unregistered session", name)
		}
	}
	if isMonitorExemptTool("mcp__atct__atct_goal_claim") {
		t.Error("isMonitorExemptTool(atct_goal_claim) = true, want false")
	}
}

// A worker finishes its task and its Monitor is gone. Refusing the report
// protects nothing: a report waits for no wakeup, and the session cannot
// restore its own Monitor, so the work dies with the pane instead.
func TestMonitorCheckLetsAWorkerReportWorkItAlreadyHolds(t *testing.T) {
	for _, name := range []string{
		"mcp__atct__atct_task_handoff_review_request",
		"mcp__atct__atct_goal_handoff_review_request",
		"mcp__atct__atct_plan_handoff_review_request",
	} {
		if !isMonitorExemptTool(name) {
			t.Errorf("isMonitorExemptTool(%q) = false; a finished worker cannot hand its work back", name)
		}
	}
	// Taking on new work still needs a Monitor: those wakeups have to land.
	for _, name := range []string{
		"mcp__atct__atct_task_handoff_request",
		"mcp__atct__atct_decision_ask",
		"mcp__atct__atct_goal_claim",
	} {
		if isMonitorExemptTool(name) {
			t.Errorf("isMonitorExemptTool(%q) = true; that call takes on work whose wakeups would not arrive", name)
		}
	}
}
