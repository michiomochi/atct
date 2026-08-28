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
		`{"data":{"goals":[]},"unapplied_decisions":[{"decision_id":1,"question":"Choose the deployment strategy"}]}`)

	var notices []struct {
		DecisionID int64  `json:"decision_id"`
		Question   string `json:"question"`
	}
	if err := json.Unmarshal(result["unapplied_decisions"], &notices); err != nil {
		t.Fatalf("unmarshal unapplied_decisions: %v", err)
	}
	if len(notices) != 1 || notices[0].DecisionID != 1 || notices[0].Question != "Choose the deployment strategy" {
		t.Fatalf("unapplied_decisions = %s, want decision 1 with question", result["unapplied_decisions"])
	}
	if got := string(result["data"]); got != `{"goals":[]}` {
		t.Fatalf("data = %s, want existing payload unchanged", got)
	}
}

func TestTaskClaimResponseIncludesUnappliedDecisions(t *testing.T) {
	result := callNotificationTestTool(t, "atct_task_claim", map[string]any{"task_id": "task-1"},
		`{"data":{"id":"task-1","status":"doing"},"unapplied_decisions":[{"decision_id":2,"question":"Which owner should take this task?"}]}`)

	var notices []struct {
		DecisionID int64  `json:"decision_id"`
		Question   string `json:"question"`
	}
	if err := json.Unmarshal(result["unapplied_decisions"], &notices); err != nil {
		t.Fatalf("unmarshal unapplied_decisions: %v", err)
	}
	if len(notices) != 1 || notices[0].DecisionID != 2 || notices[0].Question != "Which owner should take this task?" {
		t.Fatalf("unapplied_decisions = %s, want decision 2 with question", result["unapplied_decisions"])
	}
	if got := string(result["data"]); got != `{"id":"task-1","status":"doing"}` {
		t.Fatalf("data = %s, want existing payload unchanged", got)
	}
}

func TestAdditionalToolResponsesIncludeUnappliedDecisions(t *testing.T) {
	for _, name := range []string{
		"atct_task_create",
		"atct_task_declare",
		"atct_task_update",
		"atct_decision_ask",
		"atct_decision_poll",
		"atct_decision_withdraw",
		"atct_goal_complete",
		"atct_goal_set_derived_from",
	} {
		result := callNotificationTestTool(t, name, notificationTestArgs(name),
			`{"data":{"ok":true},"unapplied_decisions":[{"decision_id":3,"question":"Which option should be used?"}]}`)

		var notices []struct {
			DecisionID int64  `json:"decision_id"`
			Question   string `json:"question"`
		}
		if err := json.Unmarshal(result["unapplied_decisions"], &notices); err != nil {
			t.Fatalf("%s: unmarshal unapplied_decisions: %v", name, err)
		}
		if len(notices) != 1 || notices[0].DecisionID != 3 || notices[0].Question != "Which option should be used?" {
			t.Fatalf("%s: unapplied_decisions = %s, want decision 3 with question", name, result["unapplied_decisions"])
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

func TestProjectClaimResponseIncludesRole(t *testing.T) {
	result := callNotificationTestTool(t, "atct_project_claim", map[string]any{"project_id": "project-1"},
		`{"data":{"id":"project-1","name":"atct"},"role":"commander"}`)

	var role string
	if err := json.Unmarshal(result["role"], &role); err != nil {
		t.Fatalf("unmarshal role: %v", err)
	}
	if role != "commander" {
		t.Fatalf("role = %q, want commander", role)
	}
}

func TestGoalClaimResponseIncludesRole(t *testing.T) {
	result := callNotificationTestTool(t, "atct_goal_claim", map[string]any{"goal_id": "goal-1"},
		`{"data":{"id":"goal-1","status":"active"},"role":"subcommander"}`)

	var role string
	if err := json.Unmarshal(result["role"], &role); err != nil {
		t.Fatalf("unmarshal role: %v", err)
	}
	if role != "subcommander" {
		t.Fatalf("role = %q, want subcommander", role)
	}
}

func TestRoleBearingClaimResponsePreservesData(t *testing.T) {
	result := callNotificationTestTool(t, "atct_project_claim", map[string]any{"project_id": "project-1"},
		`{"data":{"id":"project-1","name":"atct","claimed_by":"run-1"},"role":"commander"}`)

	assertClaimPayload := func(t *testing.T, result map[string]json.RawMessage) {
		t.Helper()
		var data struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			ClaimedBy string `json:"claimed_by"`
		}
		if err := json.Unmarshal(result["data"], &data); err != nil {
			t.Fatalf("unmarshal data: %v", err)
		}
		if data.ID != "project-1" || data.Name != "atct" || data.ClaimedBy != "run-1" {
			t.Fatalf("data = %s, want existing claim payload", result["data"])
		}
	}
	assertClaimPayload(t, result)

	t.Run("role lookup failure preserves claim payload", func(t *testing.T) {
		result, err := callNotificationTestToolWithResponses(t, "atct_project_claim",
			map[string]any{"project_id": "project-1"},
			`{"result":{"data":{"id":"project-1","name":"atct","claimed_by":"run-1"}}}`,
			`{"error":{"code":-32000,"message":"role unavailable"}}`,
		)
		if err != nil {
			t.Fatalf("CallTool should preserve a successful claim: %v", err)
		}
		assertClaimPayload(t, result)
		if _, ok := result["role"]; ok {
			t.Fatalf("role = %s, want role omitted when lookup fails", result["role"])
		}
	})
}

func TestOtherToolResponsesRemainUnchanged(t *testing.T) {
	want := `{"ok":true}`
	for _, name := range []string{
		"atct_task_create",
		"atct_task_declare",
		"atct_task_update",
		"atct_decision_ask",
		"atct_decision_poll",
		"atct_decision_withdraw",
		"atct_goal_complete",
		"atct_goal_set_derived_from",
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
	result, err := callNotificationTestToolWithResponses(t, name, args, `{"result":`+daemonResult+`}`)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return result
}

func callNotificationTestToolWithResponses(t *testing.T, name string, args map[string]any, responses ...string) (map[string]json.RawMessage, error) {
	t.Helper()
	socketPath := startNotificationTestDaemonWithResponses(t, responses...)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	Register(server, NewClient(socketPath), 1)

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
		return nil, err
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal structured content %s: %v", encoded, err)
	}
	return got, nil
}

func notificationTestArgs(name string) map[string]any {
	switch name {
	case "atct_task_create", "atct_task_declare":
		return map[string]any{
			"goal_id": "goal-1", "titles": []string{"task"},
			"descriptions":    []string{"Complete the created task and verify its result."},
			"idempotency_key": "key-1", "agent": "agent-1",
		}
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
	case "atct_goal_set_derived_from":
		return map[string]any{"goal_id": "goal-1", "derived_from_goal_id": "goal-2"}
	default:
		return nil
	}
}

func startNotificationTestDaemon(t *testing.T, result string) string {
	return startNotificationTestDaemonWithResponses(t, `{"result":`+result+`}`)
}

func startNotificationTestDaemonWithResponses(t *testing.T, responses ...string) string {
	t.Helper()
	if len(responses) == 0 {
		t.Fatal("startNotificationTestDaemonWithResponses requires a response")
	}
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
	responseIndex := 0
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
				response := responses[len(responses)-1]
				if responseIndex < len(responses) {
					response = responses[responseIndex]
				}
				responseIndex++
				_, _ = io.WriteString(conn, response+"\n")
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
