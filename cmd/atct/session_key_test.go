package main

import "testing"

func TestSessionKeyMessageUsesExactHarnessSessionID(t *testing.T) {
	got := sessionKeyMessage("claude-session-1")
	want := "ATCT session key: claude-session-1. Before any other ATCT operation, call atct_session_identify with this exact session_key.\n"
	if got != want {
		t.Fatalf("sessionKeyMessage = %q, want %q", got, want)
	}
}

func TestParseSessionKey(t *testing.T) {
	cfg, err := parseArgs([]string{"session-key", "--hook-input"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.sessionKeyHookInput {
		t.Fatal("sessionKeyHookInput = false, want true")
	}
}
