package mcpshim

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestIdentifiedSessionStaysTheTransportsResolution(t *testing.T) {
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

	type roleCall struct {
		AgentSessionID    int64 `json:"agent_session_id"`
		RequireIdentified bool  `json:"require_identified"`
	}
	var mu sync.Mutex
	var roleCalls []roleCall
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
				response := `{"result":{}}`
				switch req.Method {
				case "session.identify":
					response = `{"result":{"agent_session_id":77,"reattached":true,"assignment":{"role":"subcommander"}}}`
				case "session.role":
					var call roleCall
					_ = json.Unmarshal(req.Params, &call)
					mu.Lock()
					roleCalls = append(roleCalls, call)
					mu.Unlock()
					if call.AgentSessionID == 5 {
						response = `{"error":"agent session is not identified: call atct_session_identify"}`
					} else {
						response = `{"result":{"role":"subcommander","goal_id":297}}`
					}
				}
				_, _ = io.WriteString(conn, response+"\n")
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})

	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	Register(server, NewClient(socketPath), 5)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "identity-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("CallTool %s: %v", name, err)
		}
		return result
	}
	roleArgs := map[string]any{"expected_role": "subcommander"}

	// (a) before identify the transport row is refused with the hint.
	result := call("atct_role", roleArgs)
	if !result.IsError {
		t.Fatalf("atct_role before identify succeeded: %+v", result)
	}
	text, _ := json.Marshal(result.Content)
	if !strings.Contains(string(text), "atct_session_identify") {
		t.Fatalf("atct_role error = %s; want it to name atct_session_identify", text)
	}

	// (b) after identify every later call resolves to the canonical row.
	if result := call("atct_session_identify", map[string]any{"session_key": "key-297"}); result.IsError {
		t.Fatalf("atct_session_identify failed: %+v", result)
	}
	for i := 0; i < 3; i++ {
		if result := call("atct_role", roleArgs); result.IsError {
			t.Fatalf("atct_role #%d after identify failed: %+v", i+1, result)
		}
	}

	// (c) the daemon saw 5 once, then 77 three times, always asking for identified.
	mu.Lock()
	defer mu.Unlock()
	want := []int64{5, 77, 77, 77}
	if len(roleCalls) != len(want) {
		t.Fatalf("session.role calls = %+v; want %d", roleCalls, len(want))
	}
	for i, c := range roleCalls {
		if c.AgentSessionID != want[i] || !c.RequireIdentified {
			t.Fatalf("session.role call %d = %+v; want id %d with require_identified", i, c, want[i])
		}
	}
}
