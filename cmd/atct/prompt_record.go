package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/mcpshim"
)

// promptRecordInput is the part of the UserPromptSubmit hook input that the
// human-prompt record needs.
type promptRecordInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	PromptID       string `json:"prompt_id"`
	Prompt         string `json:"prompt"`
}

// decodePromptRecordInput reports false when there is nothing to record. A
// malformed hook input is not an error here: the hook must never block the
// prompt, and nothing is written to stdout.
func decodePromptRecordInput(r io.Reader) (promptRecordInput, bool) {
	var input promptRecordInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return promptRecordInput{}, false
	}
	if input.SessionID == "" || input.Prompt == "" {
		return promptRecordInput{}, false
	}
	return input, true
}

// runPromptRecord sends the human's prompt to the daemon so a later chat answer
// can be checked against it. It writes nothing to stdout, because a
// UserPromptSubmit hook's stdout goes into the agent's context.
func runPromptRecord(config cliConfig, dir, exePath string) error {
	input, ok := decodePromptRecordInput(os.Stdin)
	if !ok {
		return nil
	}
	cwd := input.CWD
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		cwd = wd
	}
	registered, err := roleProjectRegistered(dir, cwd)
	if err != nil || !registered {
		return err
	}
	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir:            dir,
		Version:        version,
		Executable:     exePath,
		ListenAddr:     config.listenAddr,
		ListenExplicit: config.listenExplicit,
	})
	if err != nil {
		return fmt.Errorf("ensure daemon: %w", err)
	}
	return sendPromptRecord(mcpshim.NewClient(reg.SocketPath), input)
}

func sendPromptRecord(client *mcpshim.Client, input promptRecordInput) error {
	var out map[string]any
	return client.Call(context.Background(), "session.human_prompt.record", map[string]string{
		"session_key":     input.SessionID,
		"prompt_id":       input.PromptID,
		"transcript_path": input.TranscriptPath,
		"prompt":          input.Prompt,
	}, &out)
}
