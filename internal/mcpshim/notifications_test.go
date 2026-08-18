package mcpshim

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGoalListResponseIncludesUnappliedDecisions(t *testing.T) {
	result := callNotificationTestTool(t, "atct_goal_list", map[string]any{"cwd": "/tmp"},
		`{"data":{"goals":[]},"unapplied_decisions":[{"decision_id":"decision-1","question":"Choose the deployment strategy"}]}`)

	var notices []struct {
		DecisionID string `json:"decision_id"`
		Question   string `json:"question"`
	}
	if err := json.Unmarshal(result["unapplied_decisions"], &notices); err != nil {
		t.Fatalf("unmarshal unapplied_decisions: %v", err)
	}
	if len(notices) != 1 || notices[0].DecisionID != "decision-1" || notices[0].Question != "Choose the deployment strategy" {
		t.Fatalf("unapplied_decisions = %s, want decision-1 with question", result["unapplied_decisions"])
	}
	if got := string(result["data"]); got != `{"goals":[]}` {
		t.Fatalf("data = %s, want existing payload unchanged", got)
	}
}

func TestTaskClaimResponseIncludesUnappliedDecisions(t *testing.T) {
	result := callNotificationTestTool(t, "atct_task_claim", map[string]any{"task_id": "task-1"},
		`{"data":{"id":"task-1","status":"doing"},"unapplied_decisions":[{"decision_id":"decision-2","question":"Which owner should take this task?"}]}`)

	var notices []struct {
		DecisionID string `json:"decision_id"`
		Question   string `json:"question"`
	}
	if err := json.Unmarshal(result["unapplied_decisions"], &notices); err != nil {
		t.Fatalf("unmarshal unapplied_decisions: %v", err)
	}
	if len(notices) != 1 || notices[0].DecisionID != "decision-2" || notices[0].Question != "Which owner should take this task?" {
		t.Fatalf("unapplied_decisions = %s, want decision-2 with question", result["unapplied_decisions"])
	}
	if got := string(result["data"]); got != `{"id":"task-1","status":"doing"}` {
		t.Fatalf("data = %s, want existing payload unchanged", got)
	}
}

func TestAdditionalToolResponsesIncludeUnappliedDecisions(t *testing.T) {
	for _, name := range []string{
		"atct_task_declare",
		"atct_task_update",
		"atct_decision_ask",
		"atct_decision_poll",
		"atct_decision_withdraw",
		"atct_goal_complete",
	} {
		result := callNotificationTestTool(t, name, notificationTestArgs(name),
			`{"data":{"ok":true},"unapplied_decisions":[{"decision_id":"decision-3","question":"Which option should be used?"}]}`)

		var notices []struct {
			DecisionID string `json:"decision_id"`
			Question   string `json:"question"`
		}
		if err := json.Unmarshal(result["unapplied_decisions"], &notices); err != nil {
			t.Fatalf("%s: unmarshal unapplied_decisions: %v", name, err)
		}
		if len(notices) != 1 || notices[0].DecisionID != "decision-3" || notices[0].Question != "Which option should be used?" {
			t.Fatalf("%s: unapplied_decisions = %s, want decision-3 with question", name, result["unapplied_decisions"])
		}
		if got := string(result["data"]); got != `{"ok":true}` {
			t.Fatalf("%s: data = %s, want existing payload unchanged", name, got)
		}
	}
}

func TestParkedResponsePreservesClaimableTasks(t *testing.T) {
	result := callNotificationTestTool(t, "atct_decision_ask",
		map[string]any{"goal_id": "goal-1", "question": "question", "options": []any{}, "wait_ms": 0},
		`{"data":{"parked":true,"decision_id":"decision-4"},"unapplied_decisions":[],"claimable_tasks":[{"id":"task-2","title":"next task"}]}`)

	if got := string(result["claimable_tasks"]); got != `[{"id":"task-2","title":"next task"}]` {
		t.Fatalf("claimable_tasks = %s, want parked task candidates", got)
	}
}

func TestResponsesOmitUnappliedDecisionsWhenEmpty(t *testing.T) {
	result := callNotificationTestTool(t, "atct_goal_list", map[string]any{"cwd": "/tmp"},
		`{"data":{"goals":[]},"unapplied_decisions":[]}`)

	if _, ok := result["unapplied_decisions"]; ok {
		t.Fatalf("unapplied_decisions = %s, want field omitted", result["unapplied_decisions"])
	}
	if got := string(result["data"]); got != `{"goals":[]}` {
		t.Fatalf("data = %s, want existing payload unchanged", got)
	}
}

func TestOtherToolResponsesRemainUnchanged(t *testing.T) {
	want := `{"ok":true}`
	for _, name := range []string{
		"atct_task_declare",
		"atct_task_update",
		"atct_decision_ask",
		"atct_decision_poll",
		"atct_decision_withdraw",
		"atct_goal_complete",
	} {
		result := callNotificationTestTool(t, name, notificationTestArgs(name), want)
		if got := string(result["data"]); got != want {
			t.Errorf("%s data = %s, want %s", name, got, want)
		}
		if _, ok := result["unapplied_decisions"]; ok {
			t.Errorf("%s unexpectedly has unapplied_decisions: %s", name, result["unapplied_decisions"])
		}
	}
}

func callNotificationTestTool(t *testing.T, name string, args map[string]any, daemonResult string) map[string]json.RawMessage {
	t.Helper()
	socketPath := startNotificationTestDaemon(t, daemonResult)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	Register(server, NewClient(socketPath), "run-1")

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "notification-test", Version: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer clientSession.Close()

	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal structured content %s: %v", encoded, err)
	}
	return got
}

func notificationTestArgs(name string) map[string]any {
	switch name {
	case "atct_task_declare":
		return map[string]any{"goal_id": "goal-1", "titles": []string{"task"}, "idempotency_key": "key-1", "agent": "agent-1"}
	case "atct_task_update":
		return map[string]any{"task_id": "task-1", "status": "doing"}
	case "atct_decision_ask":
		return map[string]any{"goal_id": "goal-1", "question": "question", "options": []any{}, "wait_ms": 0}
	case "atct_decision_poll":
		return map[string]any{}
	case "atct_decision_withdraw":
		return map[string]any{"decision_id": "decision-1", "reason": "reason"}
	case "atct_goal_complete":
		return map[string]any{
			"goal_id": "goal-1", "work_done": "done", "now_possible": "ready",
			"how_to_verify": "check the goal", "surprises": "なし",
			"needs_review": "なし", "next_steps": "なし",
		}
	default:
		return nil
	}
}

func startNotificationTestDaemon(t *testing.T, result string) string {
	t.Helper()
	// t.TempDir() embeds the test name, which pushes the socket path past the
	// 104-byte limit macOS enforces on Unix sockets.
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
				_, _ = io.WriteString(conn, `{"result":`+result+"}\n")
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		_ = os.RemoveAll(dir)
	})
	return socketPath
}
