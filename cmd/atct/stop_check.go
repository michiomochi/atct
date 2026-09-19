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

type stopHookInput struct {
	SessionID      string `json:"session_id"`
	StopHookActive bool   `json:"stop_hook_active"`
}

type stopCheckResponse struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func runStopCheck(config cliConfig, dir, exePath string) error {
	input, err := decodeStopHookInput(os.Stdin)
	if err != nil {
		_, writeErr := fmt.Fprint(os.Stdout, stopCheckFailure(err))
		return writeErr
	}
	if input.StopHookActive {
		return nil
	}
	_, err = fmt.Fprint(os.Stdout, stopCheckResult(config, dir, exePath, input.SessionID))
	return err
}

func decodeStopHookInput(r io.Reader) (stopHookInput, error) {
	var input stopHookInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return stopHookInput{}, fmt.Errorf("decode hook input: %w", err)
	}
	if strings.TrimSpace(input.SessionID) == "" {
		return stopHookInput{}, fmt.Errorf("hook input has no session_id")
	}
	return input, nil
}

func stopCheckResult(config cliConfig, dir, exePath, sessionKey string) string {
	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir:            dir,
		Version:        version,
		Executable:     exePath,
		ListenAddr:     config.listenAddr,
		ListenExplicit: config.listenExplicit,
	})
	if err != nil {
		return stopCheckFailure(fmt.Errorf("ensure daemon: %w", err))
	}

	client := mcpshim.NewClient(reg.SocketPath)
	var response stopCheckResponse
	if err := client.Call(context.Background(), "session.stop_check", map[string]string{"session_key": sessionKey}, &response); err != nil {
		return stopCheckFailure(fmt.Errorf("session.stop_check: %w", err))
	}
	if response.Decision == "" {
		return ""
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return stopCheckFailure(fmt.Errorf("encode response: %w", err))
	}
	return string(encoded)
}

func stopCheckFailure(err error) string {
	encoded, marshalErr := json.Marshal(stopCheckResponse{Decision: "block", Reason: "ATCT stop-check failed: " + err.Error()})
	if marshalErr != nil {
		return `{"decision":"block","reason":"ATCT stop-check failed"}`
	}
	return string(encoded)
}
