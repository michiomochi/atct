package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/mcpshim"
)

type monitorHookInput struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
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
	input, err := decodeMonitorHookInput(os.Stdin)
	if err != nil {
		_, writeErr := fmt.Fprint(os.Stdout, monitorCheckDeny(err.Error()))
		return writeErr
	}
	if !isATCTTool(input.ToolName) || isSessionIdentifyTool(input.ToolName) {
		return nil
	}
	_, err = fmt.Fprint(os.Stdout, monitorCheckResult(config, dir, exePath, input.SessionID))
	return err
}

// isATCTTool keeps the gate on ATCT's own tools. Both harnesses report the
// MCP tool name, so the hook needs no harness-specific matcher.
func isATCTTool(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasPrefix(name, "mcp__atct__") || strings.HasPrefix(name, "atct__")
}

// isSessionIdentifyTool exempts the one tool that registers the session. Gating
// it deadlocks a session whose key is not in the database yet: the monitor check
// fails to resolve the key, denies the call, and nothing can ever register it.
func isSessionIdentifyTool(name string) bool {
	return strings.HasSuffix(strings.TrimSpace(name), "atct_session_identify")
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
	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir:            dir,
		Version:        version,
		Executable:     exePath,
		ListenAddr:     config.listenAddr,
		ListenExplicit: config.listenExplicit,
	})
	if err != nil {
		return monitorCheckDeny("ATCT monitor-check failed: ensure daemon: " + err.Error())
	}

	client := mcpshim.NewClient(reg.SocketPath)
	var response monitorCheckResponse
	if err := client.Call(context.Background(), "session.monitor_check", map[string]string{"session_key": sessionKey}, &response); err != nil {
		return monitorCheckDeny("ATCT monitor-check failed: session.monitor_check: " + err.Error())
	}
	if response.Decision == "" {
		return ""
	}
	return monitorCheckDeny(response.Reason)
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
