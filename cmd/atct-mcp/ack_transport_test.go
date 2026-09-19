package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeAckTransport struct {
	connection *fakeAckConnection
}

func (t *fakeAckTransport) Connect(context.Context) (mcp.Connection, error) {
	return t.connection, nil
}

type fakeAckConnection struct {
	reads    []jsonrpc.Message
	writes   []jsonrpc.Message
	writeErr error
	events   *[]string
}

func (c *fakeAckConnection) Read(context.Context) (jsonrpc.Message, error) {
	if len(c.reads) == 0 {
		return nil, errors.New("no fake message")
	}
	message := c.reads[0]
	c.reads = c.reads[1:]
	return message, nil
}

func (c *fakeAckConnection) Write(_ context.Context, message jsonrpc.Message) error {
	if c.events != nil {
		*c.events = append(*c.events, "write")
	}
	c.writes = append(c.writes, message)
	return c.writeErr
}

func (c *fakeAckConnection) Close() error { return nil }

func (c *fakeAckConnection) SessionID() string { return "fake" }

type ackTransportCase struct {
	tool        string
	actionClass string
	targetField string
	goal        bool
}

func supportedAckTransportCases() []ackTransportCase {
	return []ackTransportCase{
		{tool: "atct_handoff_request", actionClass: "task.handoff.request", targetField: "task_id"},
		{tool: "atct_task_handoff_request", actionClass: "task.handoff.request", targetField: "task_id"},
		{tool: "atct_handoff_receive", actionClass: "task.handoff.receive", targetField: "task_id"},
		{tool: "atct_task_handoff_receive", actionClass: "task.handoff.receive", targetField: "task_id"},
		{tool: "atct_handoff_complete", actionClass: "task.handoff.complete", targetField: "task_id"},
		{tool: "atct_task_handoff_complete", actionClass: "task.handoff.complete", targetField: "task_id"},
		{tool: "atct_task_handoff_review_request", actionClass: "task.handoff.review.request", targetField: "task_id"},
		{tool: "atct_task_handoff_review_receive", actionClass: "task.handoff.review.receive", targetField: "task_id"},
		{tool: "atct_task_handoff_review_reject", actionClass: "task.handoff.review.reject", targetField: "task_id"},
		{tool: "atct_handoff_report_amend", actionClass: "handoff_reported.task", targetField: "task_id"},
		{tool: "atct_task_handoff_report_amend", actionClass: "handoff_reported.task", targetField: "task_id"},
		{tool: "atct_goal_handoff_request", actionClass: "goal.handoff.request", targetField: "goal_id", goal: true},
		{tool: "atct_goal_handoff_receive", actionClass: "goal.handoff.receive", targetField: "goal_id", goal: true},
		{tool: "atct_goal_handoff_complete", actionClass: "goal.handoff.complete", targetField: "goal_id", goal: true},
		{tool: "atct_goal_handoff_review_request", actionClass: "goal.handoff.review.request", targetField: "goal_id", goal: true},
		{tool: "atct_goal_handoff_review_receive", actionClass: "goal.handoff.review.receive", targetField: "goal_id", goal: true},
		{tool: "atct_goal_handoff_review_reject", actionClass: "goal.handoff.review.reject", targetField: "goal_id", goal: true},
		{tool: "atct_goal_handoff_report_amend", actionClass: "handoff_reported.goal", targetField: "goal_id", goal: true},
		{tool: "atct_plan_handoff_review_request", actionClass: "plan.handoff.review.request", targetField: "goal_id", goal: true},
		{tool: "atct_plan_handoff_review_receive", actionClass: "plan.handoff.review.receive", targetField: "goal_id", goal: true},
		{tool: "atct_plan_handoff_review_reject", actionClass: "plan.handoff.review.reject", targetField: "goal_id", goal: true},
	}
}

func unsupportedAckTools() []string {
	return []string{
		"atct_project_claim",
		"atct_project_release",
		"atct_goal_claim",
		"atct_goal_release",
		"atct_goal_update_content",
		"atct_goal_update_request_report",
		"atct_task_create",
		"atct_task_claim",
		"atct_task_release",
		"atct_task_update",
		"atct_task_update_content",
		"atct_plan_handoff_complete",
		"atct_decision_ask",
		"atct_decision_poll",
		"atct_decision_withdraw",
		"atct_goal_complete",
		"atct_goal_review_request",
		"atct_goal_review_complete",
		"atct_goal_set_derived_from",
		"atct_goal_withdraw",
		"atct_development_start",
		"atct_handoff_recover",
		"atct_task_create_handoff_receive",
		"atct_task_handoff_review_reject_receive",
		"atct_goal_handoff_review_reject_receive",
		"atct_plan_handoff_review_reject_receive",
		"atct_session_identify",
		"atct_role",
		"atct_goal_list",
		"atct_goal_get",
		"atct_goal_sessions",
	}
}

func newFakeAckConnection(t *testing.T, send func(codexMonitorAckRecord) error) (*codexMonitorAckConnection, *fakeAckConnection) {
	t.Helper()
	underlying := &fakeAckConnection{}
	transport := newCodexMonitorAckTransport(&fakeAckTransport{connection: underlying}, "ack.sock", "capability", func(_, _ string, record codexMonitorAckRecord) error {
		return send(record)
	})
	connection, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	wrapped, ok := connection.(*codexMonitorAckConnection)
	if !ok {
		t.Fatalf("connection type = %T, want *codexMonitorAckConnection", connection)
	}
	return wrapped, underlying
}

func underlyingConnection(connection *codexMonitorAckConnection) *fakeAckConnection {
	return connection.base.(*fakeAckConnection)
}

func ackRequest(t *testing.T, id int64, tool string) *jsonrpc.Request {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"name":      tool,
		"arguments": map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return &jsonrpc.Request{ID: ackID(t, id), Method: "tools/call", Params: params}
}

func ackID(t *testing.T, id int64) jsonrpc.ID {
	t.Helper()
	result, err := jsonrpc.MakeID(float64(id))
	if err != nil {
		t.Fatalf("make JSON-RPC ID: %v", err)
	}
	return result
}

func ackSuccessResponse(t *testing.T, id int64, targetField string, target any, handoffID string) *jsonrpc.Response {
	t.Helper()
	data := map[string]any{
		targetField:  target,
		"handoff_id": handoffID,
	}
	result, err := json.Marshal(map[string]any{
		"content": []any{},
		"structuredContent": map[string]any{
			"data": data,
		},
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &jsonrpc.Response{ID: ackID(t, id), Result: result}
}

func ackStoreSuccessResponse(t *testing.T, id int64, targetField string, target any, handoffID string) *jsonrpc.Response {
	t.Helper()
	storeTargetField := map[string]string{"task_id": "TaskID", "goal_id": "GoalID"}[targetField]
	result, err := json.Marshal(map[string]any{
		"content": []any{},
		"structuredContent": map[string]any{
			"data": map[string]any{
				storeTargetField: target,
				"ID":             handoffID,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal store response: %v", err)
	}
	return &jsonrpc.Response{ID: ackID(t, id), Result: result}
}

func TestAckTransportSupportedMatrix(t *testing.T) {
	for index, tc := range supportedAckTransportCases() {
		t.Run(fmt.Sprintf("%02d_%s", index, tc.tool), func(t *testing.T) {
			var records []codexMonitorAckRecord
			connection, underlying := newFakeAckConnection(t, func(record codexMonitorAckRecord) error {
				records = append(records, record)
				return nil
			})
			request := ackRequest(t, 41, tc.tool)
			underlying.reads = []jsonrpc.Message{request}
			if _, err := connection.Read(context.Background()); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if err := connection.Write(context.Background(), ackSuccessResponse(t, 41, tc.targetField, 7, "handoff-41")); err != nil {
				t.Fatalf("Write: %v", err)
			}
			want := codexMonitorAckRecord{
				Type: codexMonitorAckRecordAcknowledgement,
				Key:  monitorMutationKey{ActionClass: tc.actionClass, HandoffID: "handoff-41", TargetID: 7},
			}
			if !reflect.DeepEqual(records, []codexMonitorAckRecord{want}) {
				t.Fatalf("records = %#v, want %#v", records, []codexMonitorAckRecord{want})
			}
		})
	}
}

func TestAckTransportUsesAcceptedEnvelopeKeyShape(t *testing.T) {
	wire, err := json.Marshal(codexMonitorAckEnvelope{
		Capability: "capability",
		Record: codexMonitorAckRecord{
			Type: codexMonitorAckRecordAcknowledgement,
			Key: monitorMutationKey{
				ActionClass: "task.handoff.request",
				HandoffID:   "handoff-41",
				TargetID:    7,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal acknowledgement envelope: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal acknowledgement envelope: %v", err)
	}
	want := map[string]any{
		"capability": "capability",
		"record": map[string]any{
			"type": "acknowledgement",
			"key": map[string]any{
				"action_class": "task.handoff.request",
				"handoff_id":   "handoff-41",
				"target_id":    float64(7),
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envelope = %#v, want %#v", got, want)
	}
}

func TestAckTransportDecodesStoreHandoffResponseFields(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tool        string
		actionClass string
		targetField string
	}{
		{name: "task", tool: "atct_task_handoff_request", actionClass: "task.handoff.request", targetField: "task_id"},
		{name: "goal", tool: "atct_goal_handoff_request", actionClass: "goal.handoff.request", targetField: "goal_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var records []codexMonitorAckRecord
			connection, underlying := newFakeAckConnection(t, func(record codexMonitorAckRecord) error {
				records = append(records, record)
				return nil
			})
			underlying.reads = []jsonrpc.Message{ackRequest(t, 1, tc.tool)}
			if _, err := connection.Read(context.Background()); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if err := connection.Write(context.Background(), ackStoreSuccessResponse(t, 1, tc.targetField, 7, "handoff")); err != nil {
				t.Fatalf("Write: %v", err)
			}
			want := codexMonitorAckRecord{
				Type: codexMonitorAckRecordAcknowledgement,
				Key:  monitorMutationKey{ActionClass: tc.actionClass, HandoffID: "handoff", TargetID: 7},
			}
			if !reflect.DeepEqual(records, []codexMonitorAckRecord{want}) {
				t.Fatalf("records = %#v, want %#v", records, []codexMonitorAckRecord{want})
			}
		})
	}
}

func TestAckTransportGoalReceiveUsesResponseAcknowledgementRecord(t *testing.T) {
	var records []codexMonitorAckRecord
	connection, _ := newFakeAckConnection(t, func(record codexMonitorAckRecord) error {
		records = append(records, record)
		return nil
	})
	underlying := underlyingConnection(connection)
	underlying.reads = []jsonrpc.Message{ackRequest(t, 1, "atct_goal_handoff_receive")}
	if _, err := connection.Read(context.Background()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := connection.Write(context.Background(), ackSuccessResponse(t, 1, "goal_id", 7, "goal-handoff")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(records) != 1 || records[0].Type != codexMonitorAckRecordAcknowledgement {
		t.Fatalf("records = %#v, want one acknowledgement record", records)
	}
	if records[0].Key.ActionClass != "goal.handoff.receive" {
		t.Fatalf("action class = %q, want goal.handoff.receive", records[0].Key.ActionClass)
	}
}

func TestAckTransportExcludesUnsupportedAndReadOnlyTools(t *testing.T) {
	for _, tool := range unsupportedAckTools() {
		t.Run(tool, func(t *testing.T) {
			var records []codexMonitorAckRecord
			connection, _ := newFakeAckConnection(t, func(record codexMonitorAckRecord) error {
				records = append(records, record)
				return nil
			})
			underlying := underlyingConnection(connection)
			underlying.reads = []jsonrpc.Message{ackRequest(t, 1, tool)}
			if _, err := connection.Read(context.Background()); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if err := connection.Write(context.Background(), ackSuccessResponse(t, 1, "task_id", 7, "handoff")); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if len(records) != 0 {
				t.Fatalf("records = %#v, want none", records)
			}
		})
	}
}

func TestAckTransportReportsAfterSuccessfulResponseWrite(t *testing.T) {
	var events []string
	var records []codexMonitorAckRecord
	connection, _ := newFakeAckConnection(t, func(record codexMonitorAckRecord) error {
		events = append(events, "ack")
		records = append(records, record)
		return nil
	})
	underlying := underlyingConnection(connection)
	underlying.events = &events
	underlying.reads = []jsonrpc.Message{ackRequest(t, 1, "atct_task_handoff_request")}
	if _, err := connection.Read(context.Background()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := connection.Write(context.Background(), ackSuccessResponse(t, 1, "task_id", 7, "handoff")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !reflect.DeepEqual(events, []string{"write", "ack"}) {
		t.Fatalf("events = %#v, want [write ack]", events)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one record", records)
	}
}

func TestAckTransportSendsAcceptedWireToUnixListener(t *testing.T) {
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "ack.sock"))
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	type wireResult struct {
		envelope codexMonitorAckEnvelope
		err      error
	}
	received := make(chan wireResult, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			received <- wireResult{err: err}
			return
		}
		defer connection.Close()
		var envelope codexMonitorAckEnvelope
		err = json.NewDecoder(connection).Decode(&envelope)
		received <- wireResult{envelope: envelope, err: err}
	}()

	underlying := &fakeAckConnection{reads: []jsonrpc.Message{ackRequest(t, 1, "atct_task_handoff_request")}}
	transport := newCodexMonitorAckTransport(
		&fakeAckTransport{connection: underlying},
		listener.Addr().String(),
		"capability",
		sendCodexMonitorAcknowledgement,
	)
	connection, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	wrapper := connection.(*codexMonitorAckConnection)
	if _, err := wrapper.Read(context.Background()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := wrapper.Write(context.Background(), ackSuccessResponse(t, 1, "task_id", 7, "handoff-1")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case result := <-received:
		if result.err != nil {
			t.Fatalf("decode acknowledgement: %v", result.err)
		}
		want := codexMonitorAckEnvelope{
			Capability: "capability",
			Record: codexMonitorAckRecord{
				Type: codexMonitorAckRecordAcknowledgement,
				Key: monitorMutationKey{
					ActionClass: "task.handoff.request",
					HandoffID:   "handoff-1",
					TargetID:    7,
				},
			},
		}
		if !reflect.DeepEqual(result.envelope, want) {
			t.Fatalf("wire envelope = %#v, want %#v", result.envelope, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for acknowledgement wire message")
	}
}

func TestAckTransportIgnoresAcknowledgementSendFailure(t *testing.T) {
	underlying := &fakeAckConnection{}
	sendCalls := 0
	transport := newCodexMonitorAckTransport(&fakeAckTransport{connection: underlying}, "ack.sock", "capability", func(string, string, codexMonitorAckRecord) error {
		sendCalls++
		return errors.New("ack listener unavailable")
	})
	connection, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	wrapped := connection.(*codexMonitorAckConnection)
	underlying.reads = []jsonrpc.Message{ackRequest(t, 1, "atct_task_handoff_request")}
	if _, err := wrapped.Read(context.Background()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := wrapped.Write(context.Background(), ackSuccessResponse(t, 1, "task_id", 7, "handoff")); err != nil {
		t.Fatalf("Write returned acknowledgement error: %v", err)
	}
	if sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", sendCalls)
	}
}

func TestAckTransportNoAckFailures(t *testing.T) {
	tests := []struct {
		name       string
		response   func(t *testing.T) *jsonrpc.Response
		writeError error
	}{
		{name: "daemon_error", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return &jsonrpc.Response{ID: ackID(t, 1), Error: errors.New("daemon failed")}
		}},
		{name: "tool_error", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			result, err := json.Marshal(map[string]any{"isError": true, "content": []any{}})
			if err != nil {
				t.Fatalf("marshal tool error: %v", err)
			}
			return &jsonrpc.Response{ID: ackID(t, 1), Result: result}
		}},
		{name: "missing_target", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return ackSuccessResponse(t, 1, "goal_id", 7, "handoff")
		}},
		{name: "missing_handoff", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return ackSuccessResponse(t, 1, "task_id", 7, "")
		}},
		{name: "invalid_target", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return ackSuccessResponse(t, 1, "task_id", "not-numeric", "handoff")
		}},
		{name: "zero_target", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return ackSuccessResponse(t, 1, "task_id", 0, "handoff")
		}},
		{name: "fractional_target", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return ackSuccessResponse(t, 1, "task_id", 1.5, "handoff")
		}},
		{name: "mismatched_response_id", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return ackSuccessResponse(t, 2, "task_id", 7, "handoff")
		}},
		{name: "malformed_response", response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return &jsonrpc.Response{ID: ackID(t, 1), Result: json.RawMessage(`{"structuredContent":`)}
		}},
		{name: "stdout_write_failure", writeError: errors.New("stdout failed"), response: func(t *testing.T) *jsonrpc.Response {
			t.Helper()
			return ackSuccessResponse(t, 1, "task_id", 7, "handoff")
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var records []codexMonitorAckRecord
			connection, underlying := newFakeAckConnection(t, func(record codexMonitorAckRecord) error {
				records = append(records, record)
				return nil
			})
			underlying.writeErr = tc.writeError
			underlying.reads = []jsonrpc.Message{ackRequest(t, 1, "atct_task_handoff_request")}
			if _, err := connection.Read(context.Background()); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if err := connection.Write(context.Background(), tc.response(t)); !errors.Is(err, tc.writeError) {
				t.Fatalf("Write error = %v, want %v", err, tc.writeError)
			}
			if len(records) != 0 {
				t.Fatalf("records = %#v, want none", records)
			}
		})
	}
}

func TestAckTransportDoesNotExposeCapabilityInResponse(t *testing.T) {
	var records []codexMonitorAckRecord
	connection, _ := newFakeAckConnection(t, func(record codexMonitorAckRecord) error {
		records = append(records, record)
		return nil
	})
	underlying := underlyingConnection(connection)
	underlying.reads = []jsonrpc.Message{ackRequest(t, 1, "atct_task_handoff_request")}
	if _, err := connection.Read(context.Background()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	response := ackSuccessResponse(t, 1, "task_id", 7, "handoff")
	before, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response before write: %v", err)
	}
	if err := connection.Write(context.Background(), response); err != nil {
		t.Fatalf("Write: %v", err)
	}
	after, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response after write: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("response changed across write: before %s, after %s", before, after)
	}
	if strings.Contains(string(after), "capability") {
		t.Fatalf("response contains capability: %s", after)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one record", records)
	}
}

func TestAckTransportUsesExactRequestResponseIDs(t *testing.T) {
	var records []codexMonitorAckRecord
	connection, _ := newFakeAckConnection(t, func(record codexMonitorAckRecord) error {
		records = append(records, record)
		return nil
	})
	underlying := underlyingConnection(connection)
	underlying.reads = []jsonrpc.Message{
		ackRequest(t, 1, "atct_task_handoff_request"),
		ackRequest(t, 2, "atct_goal_handoff_request"),
	}
	if _, err := connection.Read(context.Background()); err != nil {
		t.Fatalf("Read first: %v", err)
	}
	if _, err := connection.Read(context.Background()); err != nil {
		t.Fatalf("Read second: %v", err)
	}
	if err := connection.Write(context.Background(), ackSuccessResponse(t, 2, "goal_id", 8, "goal-handoff")); err != nil {
		t.Fatalf("Write second: %v", err)
	}
	if err := connection.Write(context.Background(), ackSuccessResponse(t, 1, "task_id", 7, "task-handoff")); err != nil {
		t.Fatalf("Write first: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %#v, want two records", records)
	}
}
