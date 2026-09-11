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
	_, err = io.WriteString(os.Stdout, sessionKeyMessage(input.SessionID))
	return err
}

func sessionKeyMessage(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return fmt.Sprintf("ATCT session key: %s. Before any other ATCT operation, call atct_session_identify with this exact session_key.\n", sessionID)
}
