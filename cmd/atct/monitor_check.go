package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/mcpshim"
)

// monitorToolName is the Claude Code tool a watch is started with.
const monitorToolName = "Monitor"

type monitorHookInput struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type monitorCheckResponse struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// preToolUseOutput is the deny wire both Claude Code and Codex accept.
type preToolUseOutput struct {
	HookSpecificOutput preToolUseHookSpecificOutput `json:"hookSpecificOutput"`
}

type preToolUseHookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

func runMonitorCheck(config cliConfig, dir, exePath string) error {
	body, readErr := io.ReadAll(os.Stdin)
	_, err := fmt.Fprint(os.Stdout, monitorCheckOutput(body, readErr, config, dir, exePath))
	return err
}

// monitorCheckOutput is what the hook prints for one PreToolUse body. A Monitor
// call is refused only for the watch form that expires, and any failure allows
// it: this gate must not stop a watch it cannot judge.
func monitorCheckOutput(body []byte, readErr error, config cliConfig, dir, exePath string) string {
	input, err := decodeMonitorHookInput(bytes.NewReader(body))
	if readErr != nil {
		err = fmt.Errorf("ATCT monitor-check failed: read hook input: %v", readErr)
	}
	if err != nil {
		if monitorHookToolName(body) == monitorToolName {
			return ""
		}
		return monitorCheckDeny(err.Error())
	}
	if strings.TrimSpace(input.ToolName) == monitorToolName {
		if !isMonitorFormATCTWatch(input.ToolInput.Command) {
			return ""
		}
		return monitorFormCheckResult(config, dir, exePath, input.SessionID)
	}
	if !isATCTTool(input.ToolName) || isMonitorExemptTool(input.ToolName) {
		return ""
	}
	return monitorCheckResult(config, dir, exePath, input.SessionID)
}

// monitorHookToolName reads tool_name from a body that did not decode as a
// whole. It returns "" when even that cannot be read.
func monitorHookToolName(body []byte) string {
	var named struct {
		ToolName string `json:"tool_name"`
	}
	if json.Unmarshal(body, &named) != nil {
		return ""
	}
	return strings.TrimSpace(named.ToolName)
}

// isMonitorFormATCTWatch reports whether a Monitor command is an ATCT watch in
// the form that expires every 30 minutes. A --once watch ends at its first
// event, so it is never refused here.
func isMonitorFormATCTWatch(command string) bool {
	return strings.Contains(command, "atct watch") &&
		strings.Contains(command, "--monitor") &&
		!strings.Contains(command, "--once")
}

// isATCTTool keeps the gate on ATCT's own tools. Both harnesses report the
// MCP tool name, so the hook needs no harness-specific matcher.
func isATCTTool(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasPrefix(name, "mcp__atct__") || strings.HasPrefix(name, "atct__")
}

// monitorExemptTools are the calls the gate must not refuse, whatever the
// state of the session's Monitor.
//
// The gate exists to stop a session taking on work whose wakeups would never
// reach it. These take on nothing.
//
// atct_session_identify registers the session. Gating it deadlocks one whose
// key is not in the database yet: the check cannot resolve the key, denies the
// call, and nothing is left that could register it.
//
// The review requests are how a worker reports that the work it already holds
// is finished. A report travels one way and waits for no wakeup, so refusing
// it protects nothing and throws away the work instead: the session cannot
// restore its own Monitor, and what it has done dies with the pane.
var monitorExemptTools = []string{
	"atct_session_identify",
	"atct_task_handoff_review_request",
	"atct_goal_handoff_review_request",
	"atct_plan_handoff_review_request",
}

func isMonitorExemptTool(name string) bool {
	name = strings.TrimSpace(name)
	for _, exempt := range monitorExemptTools {
		if strings.HasSuffix(name, exempt) {
			return true
		}
	}
	return false
}

func decodeMonitorHookInput(r io.Reader) (monitorHookInput, error) {
	var input monitorHookInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return monitorHookInput{}, fmt.Errorf("ATCT monitor-check failed: decode hook input: %v", err)
	}
	if strings.TrimSpace(input.SessionID) == "" {
		return monitorHookInput{}, fmt.Errorf("ATCT monitor-check failed: hook input has no session_id")
	}
	return input, nil
}

func monitorCheckResult(config cliConfig, dir, exePath, sessionKey string) string {
	response, err := callMonitorRPC(config, dir, exePath, "session.monitor_check", sessionKey)
	if err != nil {
		return monitorCheckDeny("ATCT monitor-check failed: " + err.Error())
	}
	if response.Decision == "" {
		return ""
	}
	return monitorCheckDeny(response.Reason)
}

func monitorFormCheckResult(config cliConfig, dir, exePath, sessionKey string) string {
	response, err := callMonitorRPC(config, dir, exePath, "session.monitor_form_check", sessionKey)
	return monitorFormDecision(response, err)
}

// monitorFormDecision turns the form check's answer into the hook output. An
// error or an allow prints nothing.
func monitorFormDecision(response monitorCheckResponse, err error) string {
	if err != nil || response.Decision == "" {
		return ""
	}
	return monitorCheckDeny(response.Reason)
}

func callMonitorRPC(config cliConfig, dir, exePath, method, sessionKey string) (monitorCheckResponse, error) {
	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir:            dir,
		Version:        version,
		Executable:     exePath,
		ListenAddr:     config.listenAddr,
		ListenExplicit: config.listenExplicit,
	})
	if err != nil {
		return monitorCheckResponse{}, fmt.Errorf("ensure daemon: %w", err)
	}

	client := mcpshim.NewClient(reg.SocketPath)
	var response monitorCheckResponse
	if err := client.Call(context.Background(), method, map[string]string{"session_key": sessionKey}, &response); err != nil {
		return monitorCheckResponse{}, fmt.Errorf("%s: %w", method, err)
	}
	return response, nil
}

func monitorCheckDeny(reason string) string {
	encoded, err := json.Marshal(preToolUseOutput{HookSpecificOutput: preToolUseHookSpecificOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}})
	if err != nil {
		return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"ATCT monitor-check failed"}}`
	}
	return string(encoded)
}
