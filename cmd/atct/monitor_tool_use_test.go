package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/michiomochi/atct/internal/mcpshim"
)

type fakeMonitorCall struct {
	Method string
	Params map[string]any
}

// startMonitorFakeDaemon answers each method from responses. A method not
// listed gets an empty result.
func startMonitorFakeDaemon(t *testing.T, responses map[string]string) (*mcpshim.Client, func() []fakeMonitorCall) {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	var mu sync.Mutex
	var calls []fakeMonitorCall
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := json.Unmarshal(line, &req); err != nil {
					return
				}
				var params map[string]any
				_ = json.Unmarshal(req.Params, &params)
				mu.Lock()
				calls = append(calls, fakeMonitorCall{Method: req.Method, Params: params})
				mu.Unlock()
				response := responses[req.Method]
				if response == "" {
					response = `{"result":{}}`
				}
				_, _ = io.WriteString(conn, response+"\n")
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return mcpshim.NewClient(socketPath), func() []fakeMonitorCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]fakeMonitorCall(nil), calls...)
	}
}

func methodsOf(calls []fakeMonitorCall) []string {
	var methods []string
	for _, c := range calls {
		methods = append(methods, c.Method)
	}
	return methods
}

func bindCalls(calls []fakeMonitorCall) []fakeMonitorCall {
	var out []fakeMonitorCall
	for _, c := range calls {
		if c.Method == "session.tool_use.bind" {
			out = append(out, c)
		}
	}
	return out
}

const (
	bindOK        = `{"result":{"ok":true}}`
	bindFailed    = `{"error":"tool_use bind failed"}`
	monitorBlock  = `{"result":{"decision":"block","reason":"no live Monitor"}}`
	monitorIsLive = `{"result":{}}`
)

func TestMonitorCheckBindsAGatedToolBeforeTheDecision(t *testing.T) {
	client, calls := startMonitorFakeDaemon(t, map[string]string{
		"session.tool_use.bind": bindOK,
		"session.monitor_check": monitorBlock,
	})
	input := monitorHookInput{SessionID: "key-1", ToolName: "mcp__atct__atct_goal_claim", ToolUseID: "toolu_1"}

	out := monitorCheckFor(client, input)
	if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "no live Monitor") {
		t.Fatalf("output = %s; want the monitor block", out)
	}
	got := calls()
	if methods := methodsOf(got); len(methods) != 2 || methods[0] != "session.tool_use.bind" || methods[1] != "session.monitor_check" {
		t.Fatalf("calls = %v; want bind then monitor_check", methods)
	}
	bind := bindCalls(got)[0]
	if bind.Params["session_key"] != "key-1" || bind.Params["tool_use_id"] != "toolu_1" {
		t.Fatalf("bind params = %+v; want session_key key-1 and tool_use_id toolu_1", bind.Params)
	}
}

func TestMonitorCheckBindsAnExemptToolAndAllowsIt(t *testing.T) {
	client, calls := startMonitorFakeDaemon(t, map[string]string{
		"session.tool_use.bind": bindOK,
		"session.monitor_check": monitorBlock,
	})
	input := monitorHookInput{SessionID: "key-1", ToolName: "mcp__atct__atct_goal_handoff_review_request", ToolUseID: "toolu_2"}

	if out := monitorCheckFor(client, input); out != "" {
		t.Fatalf("exempt tool output = %s; want empty (allowed)", out)
	}
	got := calls()
	if len(bindCalls(got)) != 1 {
		t.Fatalf("calls = %v; want one bind for the exempt tool", methodsOf(got))
	}
	for _, c := range got {
		if c.Method == "session.monitor_check" {
			t.Fatalf("exempt tool asked the monitor decision: %v", methodsOf(got))
		}
	}
}

func TestMonitorCheckSendsNoBindWithoutToolUseID(t *testing.T) {
	client, calls := startMonitorFakeDaemon(t, map[string]string{"session.monitor_check": monitorIsLive})
	monitorCheckFor(client, monitorHookInput{SessionID: "key-1", ToolName: "mcp__atct__atct_goal_claim"})

	if got := bindCalls(calls()); len(got) != 0 {
		t.Fatalf("bind sent without tool_use_id: %+v", got)
	}
}

func TestMonitorCheckNeverBindsSessionIdentify(t *testing.T) {
	client, calls := startMonitorFakeDaemon(t, map[string]string{"session.tool_use.bind": bindOK})
	monitorCheckFor(client, monitorHookInput{SessionID: "key-1", ToolName: "mcp__atct__atct_session_identify", ToolUseID: "toolu_3"})

	if got := bindCalls(calls()); len(got) != 0 {
		t.Fatalf("bind sent for atct_session_identify: %+v", got)
	}
}

func TestMonitorCheckBindFailureDoesNotChangeAnExemptTool(t *testing.T) {
	client, calls := startMonitorFakeDaemon(t, map[string]string{"session.tool_use.bind": bindFailed})
	input := monitorHookInput{SessionID: "key-1", ToolName: "mcp__atct__atct_task_handoff_review_request", ToolUseID: "toolu_4"}

	if out := monitorCheckFor(client, input); out != "" {
		t.Fatalf("exempt tool with a failed bind = %s; want empty", out)
	}
	if got := methodsOf(calls()); len(got) != 1 {
		t.Fatalf("calls = %v; want only the bind", got)
	}
}

func TestMonitorCheckBindFailureFallsThroughToTheMonitorDecision(t *testing.T) {
	client, calls := startMonitorFakeDaemon(t, map[string]string{
		"session.tool_use.bind": bindFailed,
		"session.monitor_check": monitorBlock,
	})
	input := monitorHookInput{SessionID: "key-1", ToolName: "mcp__atct__atct_goal_claim", ToolUseID: "toolu_5"}

	out := monitorCheckFor(client, input)
	if !strings.Contains(out, "no live Monitor") {
		t.Fatalf("output = %s; want the monitor decision after a failed bind", out)
	}
	if got := methodsOf(calls()); len(got) != 2 || got[1] != "session.monitor_check" {
		t.Fatalf("calls = %v; want bind then monitor_check", got)
	}
}

func TestIsSessionIdentifyToolMatchesBothHarnessNames(t *testing.T) {
	for _, name := range []string{"mcp__atct__atct_session_identify", "atct__atct_session_identify"} {
		if !isSessionIdentifyTool(name) {
			t.Errorf("isSessionIdentifyTool(%q) = false, want true", name)
		}
	}
	if isSessionIdentifyTool("mcp__atct__atct_goal_claim") {
		t.Error("isSessionIdentifyTool(atct_goal_claim) = true, want false")
	}
}
