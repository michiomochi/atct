package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

func TestDecodePromptRecordInputReadsHookFields(t *testing.T) {
	in, ok := decodePromptRecordInput(strings.NewReader(`{"session_id":"s-1","transcript_path":"/p/s-1.jsonl","cwd":"/repo","prompt_id":"pid-1","prompt":"はい","hook_event_name":"UserPromptSubmit"}`))
	if !ok {
		t.Fatal("decodePromptRecordInput rejected a complete hook input")
	}
	if in.SessionID != "s-1" || in.TranscriptPath != "/p/s-1.jsonl" || in.CWD != "/repo" || in.PromptID != "pid-1" || in.Prompt != "はい" {
		t.Fatalf("decoded input = %+v", in)
	}
}

func TestDecodePromptRecordInputSkipsWhatCannotBeRecorded(t *testing.T) {
	for name, raw := range map[string]string{
		"malformed JSON": `{"session_id":`,
		"no session_id":  `{"prompt":"x","prompt_id":"p"}`,
		"empty prompt":   `{"session_id":"s-1","prompt":""}`,
		"empty input":    ``,
	} {
		if _, ok := decodePromptRecordInput(strings.NewReader(raw)); ok {
			t.Errorf("%s: decodePromptRecordInput accepted %q", name, raw)
		}
	}
}

// The hook must reach the daemon's session.human_prompt.record RPC and store
// the prompt under the session key, so a later chat answer can find it.
func TestSendPromptRecordStoresPromptThroughDaemon(t *testing.T) {
	ctx, cancel, db, _, client, _ := startWorkflowMonitorDaemon(t)
	defer cancel()

	input := promptRecordInput{
		SessionID: "cli-record-session", TranscriptPath: "/x/projects/p/cli-record-session.jsonl",
		PromptID: "pid-cli", Prompt: "Aにしてください",
	}
	if err := sendPromptRecord(client, input); err != nil {
		t.Fatalf("sendPromptRecord: %v", err)
	}
	got, err := db.HumanPromptsSince(ctx, input.SessionID, time.Time{})
	if err != nil {
		t.Fatalf("HumanPromptsSince: %v", err)
	}
	if len(got) != 1 || got[0].PromptID != "pid-cli" || got[0].Prompt != input.Prompt || got[0].TranscriptPath != input.TranscriptPath {
		t.Fatalf("stored prompts = %+v, want the one hook prompt", got)
	}
}

// The gate decides which project a prompt belongs to. A prompt from a directory
// that is not a registered project must not be recorded.
func TestPromptRecordGateFollowsRegisteredProjects(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "registered-repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := s.CreateProject(t.Context(), "atct", root); err != nil {
		s.Close()
		t.Fatalf("CreateProject: %v", err)
	}
	s.Close()

	registered, err := roleProjectRegistered(dir, root)
	if err != nil || !registered {
		t.Fatalf("roleProjectRegistered(registered root) = %v, %v; want true", registered, err)
	}
	other := filepath.Join(dir, "unregistered-repo")
	registered, err = roleProjectRegistered(dir, other)
	if err != nil || registered {
		t.Fatalf("roleProjectRegistered(other dir) = %v, %v; want false", registered, err)
	}
}

// The hook is in Claude Code's UserPromptSubmit only. Codex has no such hook here.
func TestPromptRecordHookIsRegisteredForClaudeOnly(t *testing.T) {
	claude := readHooksFile(t, filepath.Join("..", "..", "hooks", "claude-hooks.json"))
	entries, ok := claude["UserPromptSubmit"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("claude-hooks.json UserPromptSubmit = %v, want one entry", claude["UserPromptSubmit"])
	}
	entry := entries[0].(map[string]any)
	if _, hasMatcher := entry["matcher"]; hasMatcher {
		t.Errorf("UserPromptSubmit has a matcher; it should apply to every prompt")
	}
	hooks := entry["hooks"].([]any)
	command := hooks[0].(map[string]any)["command"].(string)
	if !strings.HasSuffix(command, "/hooks/prompt-record\"") {
		t.Errorf("UserPromptSubmit command = %s, want the prompt-record hook", command)
	}

	codex := readHooksFile(t, filepath.Join("..", "..", "hooks", "codex-hooks.json"))
	if _, ok := codex["UserPromptSubmit"]; ok {
		t.Errorf("codex-hooks.json registers UserPromptSubmit; the prompt record is Claude-only")
	}
}

func readHooksFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Hooks map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc.Hooks
}

// The prompt-record hook's stdout is injected into the agent's context, so it
// must stay empty on every path, including a CLI that prints garbage.
func TestPromptRecordHookWritesNothingToStdout(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "hooks", "prompt-record"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(script)
	if err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("prompt-record is not executable: %v", err)
	}
	input := `{"session_id":"s-1","transcript_path":"/p/s-1.jsonl","cwd":"/repo","prompt_id":"pid-1","prompt":"x"}`

	t.Run("atct missing", func(t *testing.T) {
		empty := t.TempDir()
		stdout := runPromptRecordHook(t, script, input, "PATH="+empty)
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})

	t.Run("atct prints garbage and a current version", func(t *testing.T) {
		fake := t.TempDir()
		writeFakeATCT(t, fake)
		stdout := runPromptRecordHook(t, script, input, "PATH="+fake+":/usr/bin:/bin")
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})

	t.Run("atct too old", func(t *testing.T) {
		fake := t.TempDir()
		writeFakeATCTWithVersion(t, fake, "0.1.0")
		stdout := runPromptRecordHook(t, script, input, "PATH="+fake+":/usr/bin:/bin")
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})
}

func runPromptRecordHook(t *testing.T, script, input, pathEnv string) string {
	t.Helper()
	cmd := exec.Command("/bin/bash", script)
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = []string{pathEnv}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("prompt-record hook exited with %v; it must exit 0", err)
	}
	return stdout.String()
}

func writeFakeATCT(t *testing.T, dir string) {
	t.Helper()
	writeFakeATCTWithVersion(t, dir, "99.0.0")
}

// writeFakeATCTWithVersion is an atct that reports the given version and prints
// garbage for every other command, to prove the hook keeps it off stdout.
func writeFakeATCTWithVersion(t *testing.T, dir, version string) {
	t.Helper()
	body := "#!/bin/sh\nif [ \"$1\" = \"version\" ]; then echo " + version + "; exit 0; fi\necho garbage\necho garbage >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "atct"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
