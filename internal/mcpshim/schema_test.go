package mcpshim

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisterPublishesEightToolsWithFlexibleOutputSchema(t *testing.T) {
	ctx := context.Background()
	socketPath := startSchemaTestDaemon(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	Register(server, NewClient(socketPath), "run-1")

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer clientSession.Close()

	got, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	wantNames := map[string]bool{
		"atct_goal_list":         true,
		"atct_task_declare":      true,
		"atct_task_claim":        true,
		"atct_task_update":       true,
		"atct_decision_ask":      true,
		"atct_decision_poll":     true,
		"atct_decision_withdraw": true,
		"atct_goal_complete":     true,
	}
	if len(got.Tools) != len(wantNames) {
		t.Fatalf("tool count = %d, want %d", len(got.Tools), len(wantNames))
	}

	seen := make(map[string]bool, len(got.Tools))
	for _, tool := range got.Tools {
		if !wantNames[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
		seen[tool.Name] = true
		schema, ok := tool.OutputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s output schema = %T, want object schema", tool.Name, tool.OutputSchema)
		}
		if schema["type"] != "object" {
			t.Errorf("%s output schema type = %#v, want object", tool.Name, schema["type"])
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s output schema properties = %T, want object", tool.Name, schema["properties"])
		}
		switch data := properties["data"].(type) {
		case map[string]any:
			if _, constrained := data["type"]; constrained {
				t.Errorf("%s data schema imposes a JSON type: %#v", tool.Name, data)
			}
		default:
			t.Fatalf("%s data schema = %T, want unconstrained schema", tool.Name, properties["data"])
		}
	}
	for name := range wantNames {
		if !seen[name] {
			t.Errorf("missing tool %q", name)
		}
	}

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{name: "atct_goal_list", args: map[string]any{"cwd": "/tmp"}},
		{name: "atct_task_declare", args: map[string]any{
			"goal_id": "goal-1", "titles": []string{"task"}, "idempotency_key": "key-1", "agent": "agent-1",
		}},
		{name: "atct_task_claim", args: map[string]any{"task_id": "task-1"}},
		{name: "atct_task_update", args: map[string]any{"task_id": "task-1", "status": "doing"}},
		{name: "atct_decision_ask", args: map[string]any{
			"goal_id": "goal-1", "question": "question", "options": []any{}, "wait_ms": 0,
		}},
		{name: "atct_decision_poll", args: map[string]any{}},
		{name: "atct_decision_withdraw", args: map[string]any{"decision_id": "decision-1", "reason": "reason"}},
		{name: "atct_goal_complete", args: map[string]any{"goal_id": "goal-1", "result_summary": "done"}},
	} {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil {
			t.Errorf("CallTool(%s): %v", tc.name, err)
			continue
		}
		if result == nil || result.StructuredContent == nil {
			t.Errorf("CallTool(%s) returned no structured content", tc.name)
		}
	}
}

func startSchemaTestDaemon(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socketPath := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("Listen: %v", err)
	}
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
				if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
					return
				}
				_, _ = io.WriteString(conn, "{\"result\":{\"ok\":true}}\n")
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
		os.RemoveAll(dir)
	})
	return socketPath
}
