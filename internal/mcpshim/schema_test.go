package mcpshim_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemon"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisterPublishesTwentyFourToolsWithFlexibleOutputSchema(t *testing.T) {
	ctx := context.Background()
	socketPath := startSchemaTestDaemon(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	mcpshim.Register(server, mcpshim.NewClient(socketPath), 1)

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
		"atct_goal_list":             true,
		"atct_goal_get":              true,
		"atct_goal_sessions":         true,
		"atct_task_declare":          true,
		"atct_task_claim":            true,
		"atct_task_release":          true,
		"atct_task_update":           true,
		"atct_decision_ask":          true,
		"atct_decision_poll":         true,
		"atct_decision_withdraw":     true,
		"atct_goal_complete":         true,
		"atct_goal_set_derived_from": true,
		"atct_goal_claim":            true,
		"atct_goal_release":          true,
		"atct_goal_update_content":   true,
		"atct_task_update_content":   true,
		"atct_project_claim":         true,
		"atct_project_release":       true,
		"atct_role":                  true,
		"atct_session_identify":      true,
		"atct_handoff_request":       true,
		"atct_handoff_receive":       true,
		"atct_handoff_complete":      true,
		"atct_goal_handoff_request":  true,
		"atct_goal_handoff_receive":  true,
		"atct_goal_handoff_complete": true,
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
		switch tool.Name {
		case "atct_handoff_request", "atct_goal_handoff_request", "atct_handoff_receive", "atct_goal_handoff_receive", "atct_handoff_complete", "atct_goal_handoff_complete":
			idField := "task_id"
			if tool.Name == "atct_goal_handoff_request" || tool.Name == "atct_goal_handoff_receive" || tool.Name == "atct_goal_handoff_complete" {
				idField = "goal_id"
			}
			inputSchema, ok := tool.InputSchema.(map[string]any)
			if !ok {
				t.Fatalf("%s input schema = %T, want object schema", tool.Name, tool.InputSchema)
			}
			inputProperties, ok := inputSchema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("%s input schema properties = %T, want object", tool.Name, inputSchema["properties"])
			}
			wantPropertyCount := 2
			reportField := ""
			switch tool.Name {
			case "atct_handoff_request", "atct_goal_handoff_request":
				wantPropertyCount = 3
				reportField = "request_report"
			case "atct_handoff_complete", "atct_goal_handoff_complete":
				wantPropertyCount = 3
				reportField = "complete_report"
			}
			if len(inputProperties) != wantPropertyCount {
				t.Errorf("%s input property count = %d, want %d", tool.Name, len(inputProperties), wantPropertyCount)
			}
			for _, field := range []string{"handoff_id", idField} {
				if _, ok := inputProperties[field]; !ok {
					t.Errorf("%s input schema omitted %q", tool.Name, field)
				}
			}
			for _, field := range []string{"requested_by", "received_by"} {
				if _, ok := inputProperties[field]; ok {
					t.Errorf("%s input schema exposes shim-owned field %q", tool.Name, field)
				}
			}
			if reportField != "" {
				if _, ok := inputProperties[reportField]; !ok {
					t.Errorf("%s input schema omitted %q", tool.Name, reportField)
				}
			}
			required, ok := inputSchema["required"].([]any)
			if !ok {
				t.Fatalf("%s input schema required = %T, want array", tool.Name, inputSchema["required"])
			}
			requiredFields := make(map[string]bool, len(required))
			for _, field := range required {
				name, ok := field.(string)
				if ok {
					requiredFields[name] = true
				}
			}
			if !requiredFields[idField] {
				t.Errorf("%s input schema must require %s", tool.Name, idField)
			}
			if reportField != "" && requiredFields[reportField] {
				t.Errorf("%s input schema must allow omitted %s", tool.Name, reportField)
			}
			if tool.Name == "atct_handoff_receive" || tool.Name == "atct_goal_handoff_receive" || tool.Name == "atct_handoff_complete" || tool.Name == "atct_goal_handoff_complete" {
				if requiredFields["handoff_id"] {
					t.Errorf("%s input schema must allow %s-only calls", tool.Name, idField)
				}
			}
		}
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
		_, hasUnapplied := properties["unapplied_decisions"]
		wantUnapplied := true
		if hasUnapplied != wantUnapplied {
			t.Errorf("%s unapplied_decisions property present = %v, want %v", tool.Name, hasUnapplied, wantUnapplied)
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
		{name: "atct_goal_get", args: map[string]any{"goal_id": "goal-1"}},
		{name: "atct_goal_claim", args: map[string]any{"goal_id": "goal-1"}},
		{name: "atct_goal_update_content", args: map[string]any{
			"goal_id": "goal-1", "content": "updated goal",
		}},
		{name: "atct_task_update_content", args: map[string]any{
			"task_id": "task-1", "description": "updated task",
		}},
		{name: "atct_task_update_content", args: map[string]any{
			"task_id": "task-1", "title": "updated title",
		}},
		{name: "atct_task_update_content", args: map[string]any{
			"task_id": "task-1", "title": "updated title", "description": "updated task",
		}},
		{name: "atct_task_declare", args: map[string]any{
			"goal_id": "goal-1", "titles": []string{"task"},
			"descriptions":    []string{"Complete the declared task and verify its result."},
			"idempotency_key": "key-1", "agent": "agent-1",
		}},
		{name: "atct_task_claim", args: map[string]any{"task_id": "task-1"}},
		{name: "atct_task_update", args: map[string]any{"task_id": "task-1", "status": "doing"}},
		{name: "atct_handoff_request", args: map[string]any{
			"handoff_id": "handoff-1", "task_id": "task-1",
		}},
		{name: "atct_handoff_receive", args: map[string]any{
			"handoff_id": "handoff-1", "task_id": "task-1",
		}},
		{name: "atct_handoff_complete", args: map[string]any{
			"handoff_id": "handoff-1", "task_id": "task-1",
		}},
		{name: "atct_goal_handoff_request", args: map[string]any{
			"handoff_id": "goal-handoff-1", "goal_id": "goal-1",
		}},
		{name: "atct_goal_handoff_receive", args: map[string]any{
			"goal_id": "goal-1",
		}},
		{name: "atct_goal_handoff_complete", args: map[string]any{
			"handoff_id": "goal-handoff-1", "goal_id": "goal-1",
		}},
		{name: "atct_decision_ask", args: map[string]any{
			"goal_id": "goal-1", "question": "question", "options": []any{}, "wait_ms": 0,
		}},
		{name: "atct_decision_poll", args: map[string]any{}},
		{name: "atct_decision_withdraw", args: map[string]any{"decision_id": "decision-1", "reason": "reason"}},
		{name: "atct_goal_complete", args: map[string]any{
			"goal_id": "goal-1", "work_done": "done", "now_possible": "ready",
			"how_to_verify": "check the goal", "surprises": "なし",
			"needs_review": "なし", "next_steps": "なし",
		}},
		{name: "atct_goal_set_derived_from", args: map[string]any{
			"goal_id": "goal-1", "derived_from_goal_id": "goal-2",
		}},
		{name: "atct_role", args: map[string]any{}},
		{name: "atct_session_identify", args: map[string]any{"session_key": "stable-key"}},
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

func TestDecisionWithdrawSendsAgentSessionID(t *testing.T) {
	ctx := context.Background()
	socketPath, calls := startCapturingSchemaTestDaemon(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	mcpshim.Register(server, mcpshim.NewClient(socketPath), 2)

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

	identifyResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "atct_session_identify",
		Arguments: map[string]any{
			"session_key": "withdraw-session",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(atct_session_identify): %v", err)
	}
	if identifyResult == nil || identifyResult.IsError {
		t.Fatalf("atct_session_identify returned error result: %+v", identifyResult)
	}
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session.identify RPC")
	}

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "atct_decision_withdraw",
		Arguments: map[string]any{
			"decision_id": "decision-1", "reason": "reason",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(atct_decision_withdraw): %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("atct_decision_withdraw returned error result: %+v", result)
	}

	var call capturedSchemaDaemonCall
	select {
	case call = <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for decision.withdraw RPC")
	}
	if call.method != "decision.withdraw" {
		t.Fatalf("RPC method = %q, want decision.withdraw", call.method)
	}
	if got := call.params["agent_session_id"]; got != float64(9) {
		t.Errorf("agent_session_id = %#v, want 9", got)
	}
}

func TestTaskUpdateContentOmitsUnspecifiedOptionalParameters(t *testing.T) {
	ctx := context.Background()
	socketPath, calls := startCapturingSchemaTestDaemon(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	const sessionID int64 = 3
	mcpshim.Register(server, mcpshim.NewClient(socketPath), sessionID)

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

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "atct_task_update_content",
		Arguments: map[string]any{
			"task_id": "task-1", "description": "updated task",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(atct_task_update_content): %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("atct_task_update_content returned error result: %+v", result)
	}

	var call capturedSchemaDaemonCall
	select {
	case call = <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task.update_content RPC")
	}
	if call.method != "task.update_content" {
		t.Fatalf("RPC method = %q, want task.update_content", call.method)
	}
	if got := call.params["task_id"]; got != "task-1" {
		t.Errorf("task_id = %#v, want task-1", got)
	}
	if got := call.params["description"]; got != "updated task" {
		t.Errorf("description = %#v, want updated task", got)
	}
	if got := call.params["agent_session_id"]; got != float64(sessionID) {
		t.Errorf("agent_session_id = %#v, want %d", got, sessionID)
	}
	if got := call.params["include_unapplied_answers"]; got != true {
		t.Errorf("include_unapplied_answers = %#v, want true", got)
	}
	for _, field := range []string{"title"} {
		if _, ok := call.params[field]; ok {
			t.Errorf("RPC params unexpectedly included %q", field)
		}
	}
}

func TestSessionIdentifyUpdatesAgentSessionIDForFollowingTool(t *testing.T) {
	ctx := context.Background()
	socketPath, calls := startCapturingSchemaTestDaemon(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	const transportSessionID int64 = 4
	mcpshim.Register(server, mcpshim.NewClient(socketPath), transportSessionID)

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

	identifyResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "atct_session_identify",
		Arguments: map[string]any{"session_key": "stable-key"},
	})
	if err != nil {
		t.Fatalf("CallTool(atct_session_identify): %v", err)
	}
	if identifyResult == nil || identifyResult.IsError {
		t.Fatalf("atct_session_identify returned error result: %+v", identifyResult)
	}
	identifyCall := <-calls
	if identifyCall.method != "session.identify" {
		t.Fatalf("identify RPC method = %q, want session.identify", identifyCall.method)
	}
	if got := identifyCall.params["agent_session_id"]; got != float64(transportSessionID) {
		t.Fatalf("identify agent_session_id = %#v, want %d", got, transportSessionID)
	}

	roleResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "atct_role",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool(atct_role): %v", err)
	}
	if roleResult == nil || roleResult.IsError {
		t.Fatalf("atct_role returned error result: %+v", roleResult)
	}
	roleCall := <-calls
	if roleCall.method != "session.role" {
		t.Fatalf("role RPC method = %q, want session.role", roleCall.method)
	}
	if got := roleCall.params["agent_session_id"]; got != float64(9) {
		t.Fatalf("role agent_session_id = %#v, want 9", got)
	}
}

func TestSessionIdentifyKeepsTransportIDWhenDaemonReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	socketPath, calls := startCapturingSchemaTestDaemonWithIdentifyResponse(t, `{"result":{"agent_session_id":0,"reattached":false}}`)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	const transportSessionID int64 = 4
	mcpshim.Register(server, mcpshim.NewClient(socketPath), transportSessionID)

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

	identifyResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "atct_session_identify",
		Arguments: map[string]any{"session_key": "stable-key"},
	})
	if err != nil {
		t.Fatalf("CallTool(atct_session_identify): %v", err)
	}
	if identifyResult == nil || identifyResult.IsError {
		t.Fatalf("atct_session_identify returned error result: %+v", identifyResult)
	}
	<-calls

	roleResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "atct_role",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool(atct_role): %v", err)
	}
	if roleResult == nil || roleResult.IsError {
		t.Fatalf("atct_role returned error result: %+v", roleResult)
	}
	roleCall := <-calls
	if roleCall.method != "session.role" {
		t.Fatalf("role RPC method = %q, want session.role", roleCall.method)
	}
	if got := roleCall.params["agent_session_id"]; got != float64(transportSessionID) {
		t.Fatalf("role agent_session_id = %#v, want %d", got, transportSessionID)
	}
}

func TestTaskReleaseInjectsAgentSessionID(t *testing.T) {
	ctx := context.Background()
	socketPath, calls := startCapturingSchemaTestDaemon(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	const sessionID int64 = 5
	mcpshim.Register(server, mcpshim.NewClient(socketPath), sessionID)

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

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "atct_task_release",
		Arguments: map[string]any{"task_id": "task-1"},
	})
	if err != nil {
		t.Fatalf("CallTool(atct_task_release): %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("atct_task_release returned error result: %+v", result)
	}

	call := <-calls
	if call.method != "task.release" {
		t.Fatalf("RPC method = %q, want task.release", call.method)
	}
	if got := call.params["agent_session_id"]; got != float64(sessionID) {
		t.Fatalf("task.release agent_session_id = %#v, want %d", got, sessionID)
	}
}

func TestHandoffToolsInjectAgentSessionID(t *testing.T) {
	ctx := context.Background()
	socketPath, calls := startCapturingSchemaTestDaemon(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	const sessionID int64 = 6
	mcpshim.Register(server, mcpshim.NewClient(socketPath), sessionID)

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

	for _, tc := range []struct {
		name        string
		method      string
		ownedBy     string
		otherOwned  string
		reportField string
		reportValue string
		args        map[string]any
	}{
		{
			name: "atct_handoff_request", method: "handoff.request", ownedBy: "requested_by", otherOwned: "received_by",
			reportField: "request_report", reportValue: "task request report",
			args: map[string]any{"handoff_id": "handoff-1", "task_id": "task-1", "request_report": "task request report"},
		},
		{
			name: "atct_handoff_receive", method: "handoff.receive", ownedBy: "received_by", otherOwned: "requested_by",
			args: map[string]any{"task_id": "task-1"},
		},
		{
			name: "atct_goal_handoff_request", method: "goal.handoff.request", ownedBy: "requested_by", otherOwned: "received_by",
			reportField: "request_report", reportValue: "goal request report",
			args: map[string]any{"handoff_id": "goal-handoff-1", "goal_id": "goal-1", "request_report": "goal request report"},
		},
		{
			name: "atct_goal_handoff_receive", method: "goal.handoff.receive", ownedBy: "received_by", otherOwned: "requested_by",
			args: map[string]any{"goal_id": "goal-1"},
		},
		{
			name: "atct_handoff_complete", method: "handoff.complete",
			reportField: "complete_report", reportValue: "task complete report",
			args: map[string]any{"task_id": "task-1", "complete_report": "task complete report"},
		},
		{
			name: "atct_goal_handoff_complete", method: "goal.handoff.complete",
			reportField: "complete_report", reportValue: "goal complete report",
			args: map[string]any{"goal_id": "goal-1", "complete_report": "goal complete report"},
		},
	} {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      tc.name,
			Arguments: tc.args,
		})
		if err != nil {
			t.Fatalf("CallTool(%s): %v", tc.name, err)
		}
		if result == nil || result.IsError {
			t.Fatalf("CallTool(%s) returned error result: %+v", tc.name, result)
		}

		var call capturedSchemaDaemonCall
		select {
		case call = <-calls:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s RPC", tc.method)
		}
		if call.method != tc.method {
			t.Fatalf("RPC method = %q, want %q", call.method, tc.method)
		}
		if tc.ownedBy != "" {
			if got := call.params[tc.ownedBy]; got != float64(sessionID) {
				t.Errorf("%s = %#v, want injected agent session ID", tc.ownedBy, got)
			}
			if _, ok := call.params[tc.otherOwned]; ok {
				t.Errorf("RPC params unexpectedly included %q", tc.otherOwned)
			}
		}
		if tc.reportField != "" {
			if got := call.params[tc.reportField]; got != tc.reportValue {
				t.Errorf("%s = %#v, want %q", tc.reportField, got, tc.reportValue)
			}
		}
		if tc.name == "atct_handoff_receive" || tc.name == "atct_goal_handoff_receive" || tc.name == "atct_handoff_complete" || tc.name == "atct_goal_handoff_complete" {
			if _, ok := call.params["handoff_id"]; ok {
				idField := "goal_id"
				if tc.name == "atct_handoff_receive" || tc.name == "atct_handoff_complete" {
					idField = "task_id"
				}
				t.Errorf("%s-only %s unexpectedly included handoff_id", idField, tc.method)
			}
		}
	}
}

func TestRoleToolReturnsAllRolesWithEvidence(t *testing.T) {
	tests := []struct {
		name         string
		wantRole     string
		claimProject bool
		claimGoal    bool
		withTask     bool
	}{
		{name: "commander_project_only", wantRole: "commander", claimProject: true},
		{name: "commander_project_and_goal", wantRole: "commander", claimProject: true, claimGoal: true},
		{name: "subcommander_empty_task_goal", wantRole: "subcommander", claimGoal: true},
		{name: "subcommander_active_goal", wantRole: "subcommander", claimGoal: true, withTask: true},
		{name: "executor_unclaimed", wantRole: "executor"},
		{name: "executor_second_unclaimed", wantRole: "executor"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, callErr, wantProjectID, wantGoalID := callRoleTool(t, tc.claimProject, tc.claimGoal, tc.withTask, "")
			if callErr != nil {
				t.Fatalf("atct_role: %v", callErr)
			}
			if result == nil || result.IsError {
				t.Fatalf("atct_role returned error result: %+v", result)
			}
			data := decodeRoleResult(t, result)
			if got := decodeRoleString(t, data, "role"); got != tc.wantRole {
				t.Errorf("role = %q, want %q", got, tc.wantRole)
			}
			switch tc.wantRole {
			case "commander":
				if got := decodeRoleID(t, data, "project_id"); got != wantProjectID {
					t.Errorf("project_id = %d, want %d", got, wantProjectID)
				}
				if _, ok := data["goal_id"]; ok {
					t.Errorf("commander role unexpectedly contains goal_id")
				}
			case "subcommander":
				if got := decodeRoleID(t, data, "goal_id"); got != wantGoalID {
					t.Errorf("goal_id = %d, want %d", got, wantGoalID)
				}
				if _, ok := data["project_id"]; ok {
					t.Errorf("subcommander role unexpectedly contains project_id")
				}
			case "executor":
				for _, field := range []string{"project_id", "goal_id"} {
					if _, ok := data[field]; ok {
						t.Errorf("executor role unexpectedly contains %s", field)
					}
				}
			}
		})
	}
}

func TestRoleToolReportsExpectedRoleMismatchInResult(t *testing.T) {
	result, callErr, _, wantGoalID := callRoleTool(t, false, true, false, "executor")
	if callErr != nil {
		t.Fatalf("atct_role: %v", callErr)
	}
	if result == nil || result.IsError {
		t.Fatalf("atct_role mismatch returned error result: %+v", result)
	}
	data := decodeRoleResult(t, result)
	if got := decodeRoleString(t, data, "role"); got != "subcommander" {
		t.Errorf("role = %q, want subcommander", got)
	}
	if got := decodeRoleString(t, data, "expected_role"); got != "executor" {
		t.Errorf("expected_role = %q, want executor", got)
	}
	var matches bool
	if err := json.Unmarshal(data["matches"], &matches); err != nil {
		t.Fatalf("decode matches: %v", err)
	}
	if matches {
		t.Error("matches = true, want false")
	}
	if _, ok := data["project_id"]; ok {
		t.Errorf("subcommander mismatch unexpectedly contains project_id")
	}
	if got := decodeRoleID(t, data, "goal_id"); got != wantGoalID {
		t.Errorf("goal_id = %d, want %d", got, wantGoalID)
	}
}

func callRoleTool(t *testing.T, claimProject, claimGoal, withTask bool, expectedRole string) (*mcp.CallToolResult, error, int64, int64) {
	t.Helper()
	ctx := context.Background()
	storeDir := t.TempDir()
	s, err := store.Open(filepath.Join(storeDir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	project, err := s.CreateProject(ctx, "atct", filepath.Join(storeDir, "repo"))
	if err != nil {
		s.Close()
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "role fixture", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal: %v", err)
	}
	if withTask {
		if _, err := s.DeclareTasks(ctx, goal.ID, "agent", "role-fixture-task", []string{"role fixture task"}, []string{"Complete the role fixture task."}); err != nil {
			s.Close()
			t.Fatalf("DeclareTasks: %v", err)
		}
	}
	sessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		s.Close()
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if claimProject {
		if _, err := s.ClaimProject(ctx, project.ID, sessionID); err != nil {
			s.Close()
			t.Fatalf("ClaimProject: %v", err)
		}
	}
	if claimGoal {
		if _, err := s.ClaimGoal(ctx, goal.ID, sessionID); err != nil {
			s.Close()
			t.Fatalf("ClaimGoal: %v", err)
		}
	}

	socketDir, err := os.MkdirTemp("/tmp", "atct-role-")
	if err != nil {
		s.Close()
		t.Fatalf("MkdirTemp socket: %v", err)
	}
	serverCtx, cancel := context.WithCancel(ctx)
	socketPath := filepath.Join(socketDir, "daemon.sock")
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- daemon.New(s).Serve(serverCtx, socketPath) }()
	var dialErr error
	for i := 0; i < 50; i++ {
		var probe net.Conn
		probe, dialErr = net.Dial("unix", socketPath)
		if dialErr == nil {
			probe.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if dialErr != nil {
		cancel()
		s.Close()
		os.RemoveAll(socketDir)
		t.Fatalf("dial daemon: %v", dialErr)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	mcpshim.Register(server, mcpshim.NewClient(socketPath), sessionID)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(serverCtx, serverTransport, nil)
	if err != nil {
		cancel()
		s.Close()
		os.RemoveAll(socketDir)
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "role-test", Version: "test"}, nil)
	clientSession, err := client.Connect(serverCtx, clientTransport, nil)
	if err != nil {
		serverSession.Close()
		cancel()
		s.Close()
		os.RemoveAll(socketDir)
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Close()
		cancel()
		if serveErr := <-daemonDone; serveErr != nil {
			t.Errorf("daemon.Serve: %v", serveErr)
		}
		s.Close()
		os.RemoveAll(socketDir)
	})

	args := map[string]any{}
	if expectedRole != "" {
		args["expected_role"] = expectedRole
	}
	result, callErr := clientSession.CallTool(serverCtx, &mcp.CallToolParams{
		Name: "atct_role", Arguments: args,
	})
	wantProjectID, wantGoalID := int64(0), int64(0)
	if claimProject {
		wantProjectID = project.ID
	}
	if claimGoal {
		wantGoalID = goal.ID
	}
	return result, callErr, wantProjectID, wantGoalID
}

func decodeRoleResult(t *testing.T, result *mcp.CallToolResult) map[string]json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("decode role envelope: %v (json=%s)", err, encoded)
	}
	if len(envelope.Data) == 0 {
		t.Fatalf("role envelope has no data: %s", encoded)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode role data: %v (data=%s)", err, envelope.Data)
	}
	return data
}

func decodeRoleString(t *testing.T, data map[string]json.RawMessage, field string) string {
	t.Helper()
	value, ok := data[field]
	if !ok {
		t.Fatalf("role response omitted %q", field)
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	return decoded
}

func decodeRoleID(t *testing.T, data map[string]json.RawMessage, field string) int64 {
	t.Helper()
	value, ok := data[field]
	if !ok {
		t.Fatalf("role response omitted %q", field)
	}
	var decoded int64
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	return decoded
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
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var request struct {
					Method string `json:"method"`
				}
				if err := json.Unmarshal(line, &request); err != nil {
					return
				}
				response := `{"result":{"ok":true}}`
				if request.Method == "session.role" {
					response = `{"result":{"role":"executor","does":[],"does_not":[]}}`
				}
				_, _ = io.WriteString(conn, response+"\n")
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

type capturedSchemaDaemonCall struct {
	method string
	params map[string]any
}

func startCapturingSchemaTestDaemon(t *testing.T) (string, <-chan capturedSchemaDaemonCall) {
	return startCapturingSchemaTestDaemonWithIdentifyResponse(t, `{"result":{"agent_session_id":9,"reattached":true}}`)
}

func startCapturingSchemaTestDaemonWithIdentifyResponse(t *testing.T, identifyResponse string) (string, <-chan capturedSchemaDaemonCall) {
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
	calls := make(chan capturedSchemaDaemonCall, 2)
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
				var request struct {
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := json.Unmarshal(line, &request); err != nil {
					return
				}
				calls <- capturedSchemaDaemonCall{method: request.Method, params: request.Params}
				response := `{"result":{"ok":true}}`
				switch request.Method {
				case "session.identify":
					response = identifyResponse
				case "session.role":
					response = `{"result":{"role":"executor","does":[],"does_not":[]}}`
				}
				_, _ = io.WriteString(conn, response+"\n")
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
		os.RemoveAll(dir)
	})
	return socketPath, calls
}

func TestDecisionAskRejectsDefaultNotInOptions(t *testing.T) {
	result, callErr, s, goalID := callDecisionAsk(t, map[string]any{
		"options": []map[string]any{
			{"label": "A", "description": "", "consequence": ""},
			{"label": "B", "description": "", "consequence": ""},
		},
		"default_option": "C",
		"wait_ms":        0,
	})
	if callErr == nil && (result == nil || !result.IsError) {
		t.Fatalf("decision.ask with invalid default succeeded: result=%+v", result)
	}
	open, err := s.ListOpenDecisions(context.Background(), goalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("invalid default created %d decisions, want none", len(open))
	}
}

func TestDecisionAskAcceptsDefaultMatchingOption(t *testing.T) {
	result, callErr, s, goalID := callDecisionAsk(t, map[string]any{
		"options": []map[string]any{
			{"label": "A", "description": "", "consequence": ""},
			{"label": "B", "description": "", "consequence": ""},
		},
		"default_option":   "A",
		"default_after_ms": 0,
		"wait_ms":          0,
	})
	if callErr != nil {
		t.Fatalf("decision.ask with valid default: %v", callErr)
	}
	if result == nil || result.IsError {
		t.Fatalf("decision.ask with valid default returned error result: %+v", result)
	}
	open, err := s.ListOpenDecisions(context.Background(), goalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("valid default created %d decisions, want 1", len(open))
	}
	if open[0].DefaultOption != "A" {
		t.Fatalf("DefaultOption = %q, want A", open[0].DefaultOption)
	}
	if open[0].DefaultAfterMs == nil || *open[0].DefaultAfterMs != 0 {
		t.Fatalf("DefaultAfterMs = %v, want pointer to 0", open[0].DefaultAfterMs)
	}
}

func TestDecisionAskWithoutDefaultStillWorks(t *testing.T) {
	result, callErr, s, goalID := callDecisionAsk(t, map[string]any{
		"options": []map[string]any{
			{"label": "A", "description": "", "consequence": ""},
			{"label": "B", "description": "", "consequence": ""},
		},
		"wait_ms": 0,
	})
	if callErr != nil {
		t.Fatalf("decision.ask without default: %v", callErr)
	}
	if result == nil || result.IsError {
		t.Fatalf("decision.ask without default returned error result: %+v", result)
	}
	open, err := s.ListOpenDecisions(context.Background(), goalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("decision without default created %d decisions, want 1", len(open))
	}
	if open[0].DefaultOption != "" || open[0].DefaultAfterMs != nil {
		t.Fatalf("decision without default has default fields: %+v", open[0])
	}
}

func callDecisionAsk(t *testing.T, args map[string]any) (*mcp.CallToolResult, error, *store.Store, int64) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	project, err := s.CreateProject(context.Background(), "atct", "/repos/atct")
	if err != nil {
		s.Close()
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(context.Background(), project.ID, "decision defaults", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal: %v", err)
	}
	// An active decision has to name the task it is holding up.
	tasks, err := s.DeclareTasks(context.Background(), goal.ID, "agent", "batch-1", []string{"blocked task"}, []string{"Complete the blocked task after the decision is resolved."})
	if err != nil {
		s.Close()
		t.Fatalf("DeclareTasks: %v", err)
	}
	registeredSessionID, err := s.RegisterAgentSession(context.Background(), os.Getpid())
	if err != nil {
		s.Close()
		t.Fatalf("RegisterAgentSession: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	socketDir, err := os.MkdirTemp("/tmp", "atct")
	if err != nil {
		s.Close()
		t.Fatalf("MkdirTemp socket: %v", err)
	}
	socketPath := filepath.Join(socketDir, "daemon.sock")
	go daemon.New(s).Serve(ctx, socketPath)
	var probe net.Conn
	for i := 0; i < 50; i++ {
		probe, err = net.Dial("unix", socketPath)
		if err == nil {
			probe.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		cancel()
		s.Close()
		t.Fatalf("dial daemon: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	mcpshim.Register(server, mcpshim.NewClient(socketPath), registeredSessionID)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		cancel()
		s.Close()
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		serverSession.Close()
		cancel()
		s.Close()
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Close()
		cancel()
		s.Close()
		os.RemoveAll(socketDir)
	})

	args["goal_id"] = goal.ID
	if _, ok := args["task_id"]; !ok {
		args["task_id"] = tasks[0].ID
	}
	args["question"] = "Should we continue?"
	got, callErr := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "atct_decision_ask", Arguments: args,
	})
	return got, callErr, s, goal.ID
}
