package main

import (
	"strings"
	"testing"
)

func TestDecodeStopHookInput(t *testing.T) {
	input, err := decodeStopHookInput(strings.NewReader(`{"session_id":"session-123","stop_hook_active":true}`))
	if err != nil {
		t.Fatalf("decodeStopHookInput: %v", err)
	}
	if input.SessionID != "session-123" || !input.StopHookActive {
		t.Fatalf("input = %#v", input)
	}
}

func TestDecodeStopHookInputRejectsMissingSessionID(t *testing.T) {
	if _, err := decodeStopHookInput(strings.NewReader(`{"stop_hook_active":false}`)); err == nil {
		t.Fatal("decodeStopHookInput accepted missing session_id")
	}
}

func TestParseStopCheck(t *testing.T) {
	cfg, err := parseArgs([]string{"stop-check", "--hook-input"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.stopCheckHookInput {
		t.Fatal("stopCheckHookInput = false, want true")
	}
}
