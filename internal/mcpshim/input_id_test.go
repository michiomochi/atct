package mcpshim

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPIDInputsAcceptCanonicalNumbersAndLegacyStrings(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{name: "number", json: `{"goal_id":7}`, want: "7"},
		{name: "numeric string", json: `{"goal_id":"7"}`, want: "7"},
		{name: "legacy string", json: `{"goal_id":"deadbeef-old-id"}`, want: "deadbeef-old-id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in GoalGetIn
			if err := json.Unmarshal([]byte(tt.json), &in); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := string(in.GoalID); got != tt.want {
				t.Fatalf("goal_id = %q, want %q", got, tt.want)
			}
		})
	}

	var in GoalGetIn
	if err := json.Unmarshal([]byte(`{"goal_id":7.5}`), &in); err == nil {
		t.Fatal("fractional goal_id unexpectedly accepted")
	}
}

func TestMCPIDInputSchemasAllowNumbersAndStrings(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "schema-test", Version: "test"}, nil)
	Register(server, NewClient("unused"), 1)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-client", Version: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer clientSession.Close()

	tools, err := clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	wantFields := map[string][]string{
		"atct_goal_get":              {"goal_id"},
		"atct_project_claim":         {"project_id"},
		"atct_task_update":           {"task_id"},
		"atct_decision_withdraw":     {"decision_id"},
		"atct_goal_set_derived_from": {"goal_id", "derived_from_goal_id"},
	}
	for _, tool := range tools.Tools {
		fields, ok := wantFields[tool.Name]
		if !ok {
			continue
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s input schema = %T, want object", tool.Name, tool.InputSchema)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s properties = %T, want object", tool.Name, schema["properties"])
		}
		for _, field := range fields {
			property, ok := properties[field].(map[string]any)
			if !ok {
				t.Fatalf("%s.%s schema = %T, want object", tool.Name, field, properties[field])
			}
			types, ok := property["type"].([]any)
			if !ok {
				t.Fatalf("%s.%s type = %#v, want string/integer union", tool.Name, field, property["type"])
			}
			seen := map[string]bool{}
			for _, value := range types {
				if name, ok := value.(string); ok {
					seen[name] = true
				}
			}
			if !seen["string"] || !seen["integer"] {
				t.Errorf("%s.%s type = %#v, want string and integer", tool.Name, field, property["type"])
			}
		}
	}
}

func TestShimResponsesUseNumericIDs(t *testing.T) {
	response, err := decodeRoleResponse(json.RawMessage(`{"role":"commander","project_id":2,"does":[],"does_not":[]}`))
	if err != nil {
		t.Fatalf("decode role response: %v", err)
	}
	role, ok := response.(commanderRole)
	if !ok {
		t.Fatalf("decoded role response = %T, want commanderRole", response)
	}
	encodedRole, err := json.Marshal(role)
	if err != nil {
		t.Fatalf("marshal role response: %v", err)
	}
	var roleFields map[string]json.RawMessage
	if err := json.Unmarshal(encodedRole, &roleFields); err != nil {
		t.Fatalf("decode role response: %v", err)
	}
	var projectID int64
	if err := json.Unmarshal(roleFields["project_id"], &projectID); err != nil {
		t.Fatalf("decode numeric role project_id: %v", err)
	}
	if _, ok := roleFields["goal_id"]; ok {
		t.Fatalf("commander role unexpectedly contains goal_id: %s", encodedRole)
	}

	var notice UnappliedDecisionNotice
	if err := json.Unmarshal([]byte(`{"decision_id":4,"question":"choose"}`), &notice); err != nil {
		t.Fatalf("unmarshal decision notice: %v", err)
	}
	encodedNotice, err := json.Marshal(notice)
	if err != nil {
		t.Fatalf("marshal decision notice: %v", err)
	}
	var noticeFields map[string]json.RawMessage
	if err := json.Unmarshal(encodedNotice, &noticeFields); err != nil {
		t.Fatalf("decode decision notice: %v", err)
	}
	var id int64
	if err := json.Unmarshal(noticeFields["decision_id"], &id); err != nil {
		t.Fatalf("decode numeric decision ID: %v", err)
	}
}

func TestShimRoleResponsesUseRoleSpecificTypes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "commander", raw: `{"role":"commander","project_id":1}`, want: "commander"},
		{name: "subcommander", raw: `{"role":"subcommander","goal_id":2}`, want: "subcommander"},
		{name: "executor", raw: `{"role":"executor"}`, want: "executor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := decodeRoleResponse(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("decodeRoleResponse: %v", err)
			}
			switch tt.want {
			case "commander":
				if _, ok := response.(commanderRole); !ok {
					t.Fatalf("decoded response = %T, want commanderRole", response)
				}
			case "subcommander":
				if _, ok := response.(subcommanderRole); !ok {
					t.Fatalf("decoded response = %T, want subcommanderRole", response)
				}
			case "executor":
				if _, ok := response.(executorRole); !ok {
					t.Fatalf("decoded response = %T, want executorRole", response)
				}
			}
		})
	}
}
