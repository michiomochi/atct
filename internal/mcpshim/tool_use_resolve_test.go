package mcpshim

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeDaemonCall struct {
	Method string
	Params map[string]any
}

// startFakeDaemon serves the daemon socket. responses maps a method to its raw
// reply; a method not listed gets an empty result. It returns the socket path
// and a function that reports the calls seen so far.
func startFakeDaemon(t *testing.T, responses map[string]string) (string, func() []fakeDaemonCall) {
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
	var calls []fakeDaemonCall
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
				var req rpc.Request
				if err := json.Unmarshal(line, &req); err != nil {
					return
				}
				var params map[string]any
				_ = json.Unmarshal(req.Params, &params)
				mu.Lock()
				calls = append(calls, fakeDaemonCall{Method: req.Method, Params: params})
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
	return socketPath, func() []fakeDaemonCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]fakeDaemonCall(nil), calls...)
	}
}

// newToolUseTestSession registers the shim as transport row 5 and connects a client.
func newToolUseTestSession(t *testing.T, responses map[string]string) (*mcp.ClientSession, func() []fakeDaemonCall) {
	t.Helper()
	socketPath, calls := startFakeDaemon(t, responses)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	Register(server, NewClient(socketPath), 5)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "tool-use-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session, calls
}

func callAtctRole(t *testing.T, session *mcp.ClientSession, toolUseID string) *mcp.CallToolResult {
	t.Helper()
	params := &mcp.CallToolParams{Name: "atct_role", Arguments: map[string]any{"expected_role": "executor"}}
	if toolUseID != "" {
		params.Meta = mcp.Meta{"claudecode/toolUseId": toolUseID}
	}
	result, err := session.CallTool(context.Background(), params)
	if err != nil {
		t.Fatalf("CallTool atct_role: %v", err)
	}
	return result
}

// roleAgentIDs returns the agent_session_id of every session.role call, in order.
func roleAgentIDs(calls []fakeDaemonCall) []float64 {
	var ids []float64
	for _, c := range calls {
		if c.Method == "session.role" {
			id, _ := c.Params["agent_session_id"].(float64)
			ids = append(ids, id)
		}
	}
	return ids
}

func resolveCalls(calls []fakeDaemonCall) []fakeDaemonCall {
	var out []fakeDaemonCall
	for _, c := range calls {
		if c.Method == "session.tool_use.resolve" {
			out = append(out, c)
		}
	}
	return out
}

func TestToolUseMetaAttachesTheTransportBeforeTheCall(t *testing.T) {
	session, calls := newToolUseTestSession(t, map[string]string{
		"session.tool_use.resolve": `{"result":{"agent_session_id":77}}`,
		"session.role":             `{"result":{"role":"executor"}}`,
	})

	if result := callAtctRole(t, session, "toolu_1"); result.IsError {
		t.Fatalf("atct_role with a binding failed: %+v", result)
	}
	got := calls()
	resolves := resolveCalls(got)
	if len(resolves) != 1 {
		t.Fatalf("resolve calls = %+v; want 1", resolves)
	}
	if id, _ := resolves[0].Params["agent_session_id"].(float64); id != 5 || resolves[0].Params["tool_use_id"] != "toolu_1" {
		t.Fatalf("resolve params = %+v; want agent 5 and toolu_1", resolves[0].Params)
	}
	if ids := roleAgentIDs(got); len(ids) != 1 || ids[0] != 77 {
		t.Fatalf("session.role ids = %v; want [77] after the attach", ids)
	}

	// The holder now holds the canonical id, so a later call without _meta uses it.
	callAtctRole(t, session, "")
	if ids := roleAgentIDs(calls()); len(ids) != 2 || ids[1] != 77 {
		t.Fatalf("session.role ids = %v; want the second call on 77", ids)
	}
}

func TestToolUseWithoutMetaSkipsResolve(t *testing.T) {
	session, calls := newToolUseTestSession(t, map[string]string{
		"session.tool_use.resolve": `{"result":{"agent_session_id":77}}`,
	})

	callAtctRole(t, session, "")
	got := calls()
	if resolves := resolveCalls(got); len(resolves) != 0 {
		t.Fatalf("resolve calls without _meta = %+v; want none", resolves)
	}
	if ids := roleAgentIDs(got); len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("session.role ids = %v; want [5]", ids)
	}
}

func TestToolUseResolveErrorStillRunsTheTool(t *testing.T) {
	session, calls := newToolUseTestSession(t, map[string]string{
		"session.tool_use.resolve": `{"error":"agent session 5 is discarded"}`,
		"session.role":             `{"result":{"role":"executor"}}`,
	})

	if result := callAtctRole(t, session, "toolu_err"); result.IsError {
		t.Fatalf("atct_role after a failed resolve = error %+v; want the tool to run", result)
	}
	if ids := roleAgentIDs(calls()); len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("session.role ids = %v; want [5] (holder unchanged)", ids)
	}
}

func TestToolUseZeroResolveKeepsTheHolder(t *testing.T) {
	session, calls := newToolUseTestSession(t, map[string]string{
		"session.tool_use.resolve": `{"result":{"agent_session_id":0}}`,
	})

	callAtctRole(t, session, "toolu_none")
	if ids := roleAgentIDs(calls()); len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("session.role ids = %v; want [5] (holder unchanged)", ids)
	}
}
