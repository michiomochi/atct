package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/rpc"
)

func TestParseHandoffEntryActions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		want  string
		scope string
	}{
		{name: "append task", args: []string{"handoff", "append", "handoff-1", "task-1", "review_requested", "review body"}, want: "append", scope: "task"},
		{name: "history task", args: []string{"handoff", "history", "handoff-1", "task-1"}, want: "history", scope: "task"},
		{name: "append goal", args: []string{"handoff", "goal", "append", "handoff-1", "goal-1", "review_rejected", "rejection body"}, want: "append", scope: "goal"},
		{name: "history goal", args: []string{"handoff", "goal", "history", "handoff-1", "goal-1"}, want: "history", scope: "goal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseArgs(tc.args)
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			if cfg.subcommand != "handoff" || cfg.handoffAction != tc.want || cfg.handoffScope != tc.scope {
				t.Fatalf("handoff config = %#v, want action %q", cfg, tc.want)
			}
		})
	}
}

func TestParseHandoffEntryOptions(t *testing.T) {
	cfg, err := parseArgs([]string{
		"handoff", "append", "handoff-1", "task-1", "review_received", "body",
		"--in-reply-to-id", "1", "--capability=opaque", "--agent-session-id", "12", "--listen", "127.0.0.1:18787",
	})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.handoffInReplyToID != 1 || cfg.handoffCapability != "opaque" || cfg.handoffAgentSessionID != "12" {
		t.Fatalf("handoff options = %#v, want reply id, capability, and session", cfg)
	}
	if cfg.listenAddr != "127.0.0.1:18787" || !cfg.listenExplicit {
		t.Fatalf("handoff listen = %q explicit=%t, want explicit custom address", cfg.listenAddr, cfg.listenExplicit)
	}

	cfg, err = parseArgs([]string{"handoff", "goal", "entry", "append", "handoff-2", "goal-2", "--kind", "review_requested", "--body", "body"})
	if err != nil {
		t.Fatalf("parseArgs goal entry flags: %v", err)
	}
	if cfg.handoffScope != "goal" || cfg.handoffGoalID != "goal-2" || cfg.handoffEntryKind != "review_requested" || cfg.handoffEntryBody != "body" {
		t.Fatalf("goal handoff options = %#v, want parsed entry flags", cfg)
	}

	cfg, err = parseArgs([]string{"handoff", "history", "handoff-1", "task-1", "--after-id", "4", "--limit=20", "--report", "ignored"})
	if err == nil {
		t.Fatalf("parseArgs with report on history succeeded: %#v", cfg)
	}
}

func TestParseHandoffRejectsLegacyEntryOptions(t *testing.T) {
	for _, args := range [][]string{
		{"handoff", "append", "handoff-1", "task-1", "review_requested", "body", "--relates-to", "1"},
		{"handoff", "history", "handoff-1", "task-1", "--cursor", "1"},
	} {
		if cfg, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) = %#v, want removed option error", args, cfg)
		}
	}
}

func TestRunHandoffRejectsUnmonitoredCaller(t *testing.T) {
	t.Setenv("ATCT_MONITOR_CAPABILITY", "")
	t.Setenv("ATCT_MONITORED_CALLER", "")
	err := runHandoff(cliConfig{
		handoffAction: "append",
		handoffID:     "handoff-1",
		handoffTaskID: "task-1",
	}, t.TempDir(), t.TempDir()+"/atct-not-started")
	if err == nil || !strings.Contains(err.Error(), "monitored caller") {
		t.Fatalf("runHandoff error = %v, want explicit monitored caller error", err)
	}
}

func TestRunHandoffAppendForwardsMonitoredCapability(t *testing.T) {
	dir := shortDaemonTestDir(t)
	socketPath := filepath.Join(dir, "handoff.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	if err := daemonctl.WriteRegistry(dir, daemonctl.Registry{
		PID:        os.Getpid(),
		SocketPath: socketPath,
		Version:    version,
	}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	requests := make(chan rpc.Request, 2)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var req rpc.Request
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					return
				}
				select {
				case requests <- req:
				default:
				}
				result, _ := json.Marshal(map[string]any{
					"id": 3, "handoff_id": "handoff-1", "kind": "review_requested", "body": "review body", "author_session_id": 12,
					"in_reply_to_id": 1,
				})
				response, _ := json.Marshal(rpc.Response{Result: result})
				_, _ = conn.Write(append(response, '\n'))
			}()
		}
	}()

	if err := runHandoff(cliConfig{
		handoffAction:      "append",
		handoffScope:       "task",
		handoffID:          "handoff-1",
		handoffTaskID:      "task-1",
		handoffEntryKind:   "review_requested",
		handoffEntryBody:   "review body",
		handoffInReplyToID: 1,
		handoffCapability:  "opaque-capability",
	}, dir, filepath.Join(dir, "atct-not-started")); err != nil {
		t.Fatalf("runHandoff: %v", err)
	}
	select {
	case req := <-requests:
		if req.Method != "handoff.entry.append" {
			t.Fatalf("RPC method = %q, want handoff.entry.append", req.Method)
		}
		var params map[string]any
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("RPC params: %v", err)
		}
		for key, want := range map[string]string{
			"handoff_id": "handoff-1", "task_id": "task-1", "kind": "review_requested", "body": "review body", "capability": "opaque-capability",
		} {
			if params[key] != want {
				t.Fatalf("params[%q] = %#v, want %q", key, params[key], want)
			}
		}
		if params["in_reply_to_id"] != float64(1) {
			t.Fatalf("params[in_reply_to_id] = %#v, want 1", params["in_reply_to_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handoff.entry.append RPC")
	}
	listener.Close()
	<-serveDone
}

func TestRunHandoffHistoryForwardsAfterIDAndPrintsCanonicalPage(t *testing.T) {
	dir := shortDaemonTestDir(t)
	socketPath := filepath.Join(dir, "handoff.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := daemonctl.WriteRegistry(dir, daemonctl.Registry{
		PID:        os.Getpid(),
		SocketPath: socketPath,
		Version:    version,
	}); err != nil {
		listener.Close()
		t.Fatalf("WriteRegistry: %v", err)
	}

	requests := make(chan rpc.Request, 1)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var req rpc.Request
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					return
				}
				select {
				case requests <- req:
				default:
				}
				result, _ := json.Marshal(map[string]any{
					"entries": []map[string]any{{
						"id": 3, "handoff_id": "handoff-1", "kind": "review_received", "body": "review reply", "author_session_id": 12,
					}},
					"has_more": false, "next_after_id": 3,
				})
				response, _ := json.Marshal(rpc.Response{Result: result})
				_, _ = conn.Write(append(response, '\n'))
			}()
		}
	}()

	oldStdout := os.Stdout
	readOutput, writeOutput, err := os.Pipe()
	if err != nil {
		listener.Close()
		<-serveDone
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writeOutput
	runErr := runHandoff(cliConfig{
		handoffAction:     "history",
		handoffScope:      "task",
		handoffID:         "handoff-1",
		handoffTaskID:     "task-1",
		handoffAfterID:    2,
		handoffLimit:      20,
		handoffCapability: "opaque-capability",
	}, dir, filepath.Join(dir, "atct-not-started"))
	writeOutput.Close()
	os.Stdout = oldStdout
	if runErr != nil {
		readOutput.Close()
		listener.Close()
		<-serveDone
		t.Fatalf("runHandoff: %v", runErr)
	}
	output, err := io.ReadAll(readOutput)
	readOutput.Close()
	if err != nil {
		listener.Close()
		<-serveDone
		t.Fatalf("read output: %v", err)
	}

	select {
	case req := <-requests:
		if req.Method != "handoff.entry.history" {
			t.Fatalf("RPC method = %q, want handoff.entry.history", req.Method)
		}
		var params map[string]any
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("RPC params: %v", err)
		}
		if params["after_id"] != float64(2) || params["limit"] != float64(20) {
			t.Fatalf("RPC params = %#v, want after_id=2 limit=20", params)
		}
		if _, ok := params["cursor"]; ok {
			t.Fatalf("RPC params expose removed cursor: %#v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handoff.entry.history RPC")
	}

	var page map[string]any
	if err := json.Unmarshal(output, &page); err != nil {
		t.Fatalf("decode CLI output: %v (%s)", err, output)
	}
	if page["next_after_id"] != float64(3) || page["next_cursor"] != nil {
		t.Fatalf("CLI history output = %#v, want next_after_id and no next_cursor", page)
	}
	entries, ok := page["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("CLI history entries = %#v, want one entry", page["entries"])
	}
	entry := entries[0].(map[string]any)
	if entry["id"] != float64(3) || entry["in_reply_to_id"] != nil {
		t.Fatalf("CLI history entry = %#v, want canonical id", entry)
	}
	if _, ok := entry["entry_id"]; ok {
		t.Fatalf("CLI history entry exposes removed entry_id: %#v", entry)
	}

	listener.Close()
	<-serveDone
}
