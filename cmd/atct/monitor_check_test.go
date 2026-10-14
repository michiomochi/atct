package main

import (
	"errors"
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

func TestIsMonitorFormATCTWatch(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{"atct watch --monitor --token t1", true},
		{"/usr/local/bin/atct watch --monitor --token t1", true},
		{"atct watch --monitor --token t1 --once", false},
		{"atct watch --token t1", false},
		{"sleep 600", false},
		{"", false},
	} {
		if got := isMonitorFormATCTWatch(tc.command); got != tc.want {
			t.Errorf("isMonitorFormATCTWatch(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestMonitorFormDecisionFailsOpenAndDeniesOnlyABlock(t *testing.T) {
	if got := monitorFormDecision(monitorCheckResponse{}, errors.New("daemon unreachable")); got != "" {
		t.Fatalf("monitorFormDecision(error) = %s, want no output", got)
	}
	if got := monitorFormDecision(monitorCheckResponse{}, nil); got != "" {
		t.Fatalf("monitorFormDecision(allow) = %s, want no output", got)
	}
	got := monitorFormDecision(monitorCheckResponse{Decision: "block", Reason: "use --once"}, nil)
	want := monitorCheckDeny("use --once")
	if got != want {
		t.Fatalf("monitorFormDecision(block) = %s, want %s", got, want)
	}
}

// Any Monitor call this hook cannot judge is let through, and so is every
// Monitor call that is not a watch in the expiring form.
func TestMonitorCheckOutputLetsUnjudgedMonitorCallsThrough(t *testing.T) {
	for name, body := range map[string]string{
		"no session_id":     `{"tool_name":"Monitor","tool_input":{"command":"atct watch --monitor --token t1"}}`,
		"non-watch command": `{"session_id":"s1","tool_name":"Monitor","tool_input":{"command":"tail -f app.log"}}`,
		"once watch":        `{"session_id":"s1","tool_name":"Monitor","tool_input":{"command":"atct watch --monitor --token t1 --once"}}`,
		"ws source":         `{"session_id":"s1","tool_name":"Monitor","tool_input":{"ws":{"url":"wss://example.com"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := monitorCheckOutput([]byte(body), nil, cliConfig{}, "", ""); got != "" {
				t.Fatalf("monitorCheckOutput = %s, want no output", got)
			}
		})
	}
}

// A body that does not parse as JSON cannot be attributed to a tool, so the
// gate treats it as it always has: deny. Claude Code sends valid JSON, so this
// is not reached for a Monitor call in practice.
func TestMonitorCheckOutputDeniesUnparseableBody(t *testing.T) {
	got := monitorCheckOutput([]byte(`{"tool_name":"Monitor"`), nil, cliConfig{}, "", "")
	if !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("monitorCheckOutput = %s, want deny", got)
	}
}

// A watch in the expiring form asks the daemon; with no daemon to ask, it is allowed.
func TestMonitorCheckOutputAllowsWatchFormWhenDaemonIsUnreachable(t *testing.T) {
	body := `{"session_id":"s1","tool_name":"Monitor","tool_input":{"command":"atct watch --monitor --token t1"}}`
	if got := monitorCheckOutput([]byte(body), nil, cliConfig{}, "", ""); got != "" {
		t.Fatalf("monitorCheckOutput = %s, want fail-open", got)
	}
}

// The ATCT gate keeps denying when it cannot reach the daemon.
func TestMonitorCheckOutputStillDeniesATCTToolWhenDaemonIsUnreachable(t *testing.T) {
	body := `{"session_id":"s1","tool_name":"mcp__atct__atct_goal_get"}`
	got := monitorCheckOutput([]byte(body), nil, cliConfig{}, "", "")
	if !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, "ATCT monitor-check failed") {
		t.Fatalf("monitorCheckOutput = %s, want a deny naming the failed check", got)
	}
}
