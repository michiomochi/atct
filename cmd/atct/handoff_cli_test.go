package main

import (
	"bufio"
	"encoding/json"
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
		{name: "append task", args: []string{"handoff", "append", "handoff-1", "task-1", "progress", "progress body"}, want: "append", scope: "task"},
		{name: "history task", args: []string{"handoff", "history", "handoff-1", "task-1"}, want: "history", scope: "task"},
		{name: "append goal", args: []string{"handoff", "goal", "append", "handoff-1", "goal-1", "question", "question body"}, want: "append", scope: "goal"},
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
		"handoff", "append", "handoff-1", "task-1", "answer", "body",
		"--relates-to", "entry-1", "--capability=opaque", "--agent-session-id", "12", "--listen", "127.0.0.1:18787",
	})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.handoffRelatesTo != "entry-1" || cfg.handoffCapability != "opaque" || cfg.handoffAgentSessionID != "12" {
		t.Fatalf("handoff options = %#v, want relation, capability, and session", cfg)
	}
	if cfg.listenAddr != "127.0.0.1:18787" || !cfg.listenExplicit {
		t.Fatalf("handoff listen = %q explicit=%t, want explicit custom address", cfg.listenAddr, cfg.listenExplicit)
	}

	cfg, err = parseArgs([]string{"handoff", "goal", "entry", "append", "handoff-2", "goal-2", "--kind", "question", "--body", "body"})
	if err != nil {
		t.Fatalf("parseArgs goal entry flags: %v", err)
	}
	if cfg.handoffScope != "goal" || cfg.handoffGoalID != "goal-2" || cfg.handoffEntryKind != "question" || cfg.handoffEntryBody != "body" {
		t.Fatalf("goal handoff options = %#v, want parsed entry flags", cfg)
	}

	cfg, err = parseArgs([]string{"handoff", "history", "handoff-1", "task-1", "--cursor", "4", "--limit=20", "--report", "ignored"})
	if err == nil {
		t.Fatalf("parseArgs with report on history succeeded: %#v", cfg)
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
					"entry_id": "entry-1", "handoff_id": "handoff-1", "sequence": 3,
					"kind": "progress", "body": "progress body", "author_session_id": 12,
				})
				response, _ := json.Marshal(rpc.Response{Result: result})
				_, _ = conn.Write(append(response, '\n'))
			}()
		}
	}()

	if err := runHandoff(cliConfig{
		handoffAction:     "append",
		handoffScope:      "task",
		handoffID:         "handoff-1",
		handoffTaskID:     "task-1",
		handoffEntryKind:  "progress",
		handoffEntryBody:  "progress body",
		handoffCapability: "opaque-capability",
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
			"handoff_id": "handoff-1", "task_id": "task-1", "kind": "progress", "body": "progress body", "capability": "opaque-capability",
		} {
			if params[key] != want {
				t.Fatalf("params[%q] = %#v, want %q", key, params[key], want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handoff.entry.append RPC")
	}
	listener.Close()
	<-serveDone
}
