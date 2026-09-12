package main

import (
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
	monitorToken := strings.TrimSpace(os.Getenv("ATCT_MONITOR_TOKEN"))
	_, err = io.WriteString(os.Stdout, sessionKeyMessageWithMonitorToken(input.SessionID, monitorToken))
	return err
}

func sessionKeyMessage(sessionID string) string {
	return sessionKeyMessageWithMonitorToken(sessionID, "")
}

func sessionKeyMessageWithMonitorToken(sessionID, monitorToken string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	monitorToken = strings.TrimSpace(monitorToken)
	if monitorToken != "" {
		return fmt.Sprintf("ATCT session key: %s. Before any other ATCT operation, call atct_session_identify with this exact session_key and monitor_token %s.\n", sessionID, monitorToken)
	}
	return fmt.Sprintf("ATCT session key: %s. Before any other ATCT operation, call atct_session_identify with this exact session_key.\n", sessionID)
}
