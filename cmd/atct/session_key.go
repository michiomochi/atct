package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func runSessionKey(config cliConfig, dir string) error {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	registered, err := roleProjectRegistered(dir, cwd)
	if err != nil || !registered {
		return err
	}
	monitorToken := sessionStartMonitorToken(input.SessionID, os.Getenv("ATCT_MONITOR_TOKEN"))
	_, err = io.WriteString(os.Stdout, sessionKeyMessageWithMonitorToken(input.SessionID, monitorToken))
	return err
}

func sessionStartMonitorToken(sessionID, token string) string {
	token = strings.TrimSpace(token)
	if token != "" {
		return token
	}
	// SessionStart also runs after compact; the same session must keep its bind token.
	sum := sha256.Sum256([]byte("atct-monitor:" + strings.TrimSpace(sessionID)))
	return fmt.Sprintf("%x", sum)
}

func sessionKeyMessageWithMonitorToken(sessionID, monitorToken string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	monitorToken = strings.TrimSpace(monitorToken)
	if monitorToken != "" {
		return fmt.Sprintf("ATCT session key: %s. When receiving a task or goal handoff, pass this exact session_key and monitor_token %s to its receive tool. Otherwise, call atct_session_identify with them before other ATCT operations.\n", sessionID, monitorToken)
	}
	return fmt.Sprintf("ATCT session key: %s. When receiving a task or goal handoff, pass this exact session_key to its receive tool. Otherwise, call atct_session_identify with it before other ATCT operations.\n", sessionID)
}
