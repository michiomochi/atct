package main

import "testing"

func TestSessionKeyMessageIncludesMonitorToken(t *testing.T) {
	got := sessionKeyMessageWithMonitorToken("codex-session-1", "token-1")
	want := "ATCT session key: codex-session-1. When receiving a task or goal handoff, pass this exact session_key and monitor_token token-1 to its receive tool. Otherwise, call atct_session_identify with them before other ATCT operations.\n"
	if got != want {
		t.Fatalf("sessionKeyMessageWithMonitorToken = %q, want %q", got, want)
	}
}

func TestSessionKeyMessageDirectsReceiveWithoutMonitorToken(t *testing.T) {
	got := sessionKeyMessageWithMonitorToken("codex-session-1", "")
	want := "ATCT session key: codex-session-1. When receiving a task or goal handoff, pass this exact session_key to its receive tool. Otherwise, call atct_session_identify with it before other ATCT operations.\n"
	if got != want {
		t.Fatalf("sessionKeyMessageWithMonitorToken = %q, want %q", got, want)
	}
}

func TestSessionStartMonitorTokenUsesInjectedToken(t *testing.T) {
	got := sessionStartMonitorToken("codex-session-1", "token-1")
	if got != "token-1" {
		t.Fatalf("sessionStartMonitorToken = %q, want token-1", got)
	}
}

func TestSessionStartMonitorTokenIsStableForSameSession(t *testing.T) {
	first := sessionStartMonitorToken("claude-session-1", "")
	second := sessionStartMonitorToken("claude-session-1", "")
	if first != second || len(first) != 64 {
		t.Fatalf("session token = %q then %q, want stable 64-character token", first, second)
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
