package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	codexMonitorAckSocketEnvironment     = "ATCT_MONITOR_ACK_SOCKET"
	codexMonitorAckCapabilityEnvironment = "ATCT_MONITOR_ACK_CAPABILITY"
	codexMonitorAckRecordAcknowledgement = "acknowledgement"
	codexMonitorAckDeadline              = time.Second
)

type monitorMutationKey struct {
	ActionClass string `json:"action_class"`
	HandoffID   string `json:"handoff_id"`
	TargetID    int64  `json:"target_id"`
}

type codexMonitorAckRecord struct {
	Type codexMonitorAckRecordType `json:"type"`
	Key  monitorMutationKey        `json:"key,omitempty"`
}

type codexMonitorAckRecordType string

type codexMonitorAckEnvelope struct {
	Capability string                `json:"capability"`
	Record     codexMonitorAckRecord `json:"record"`
}

type codexMonitorAckMapping struct {
	actionClass string
	targetField string
}

var codexMonitorAckMappings = map[string]codexMonitorAckMapping{
	"atct_handoff_request":             {actionClass: "task.handoff.request", targetField: "task_id"},
	"atct_task_handoff_request":        {actionClass: "task.handoff.request", targetField: "task_id"},
	"atct_handoff_receive":             {actionClass: "task.handoff.receive", targetField: "task_id"},
	"atct_task_handoff_receive":        {actionClass: "task.handoff.receive", targetField: "task_id"},
	"atct_handoff_complete":            {actionClass: "task.handoff.complete", targetField: "task_id"},
	"atct_task_handoff_complete":       {actionClass: "task.handoff.complete", targetField: "task_id"},
	"atct_task_handoff_review_request": {actionClass: "task.handoff.review.request", targetField: "task_id"},
	"atct_task_handoff_review_receive": {actionClass: "task.handoff.review.receive", targetField: "task_id"},
	"atct_task_handoff_review_reject":  {actionClass: "task.handoff.review.reject", targetField: "task_id"},
	"atct_handoff_report_amend":        {actionClass: "handoff_reported.task", targetField: "task_id"},
	"atct_task_handoff_report_amend":   {actionClass: "handoff_reported.task", targetField: "task_id"},
	"atct_goal_handoff_request":        {actionClass: "goal.handoff.request", targetField: "goal_id"},
	"atct_goal_handoff_receive":        {actionClass: "goal.handoff.receive", targetField: "goal_id"},
	"atct_goal_handoff_complete":       {actionClass: "goal.handoff.complete", targetField: "goal_id"},
	"atct_goal_handoff_review_request": {actionClass: "goal.handoff.review.request", targetField: "goal_id"},
	"atct_goal_handoff_review_receive": {actionClass: "goal.handoff.review.receive", targetField: "goal_id"},
	"atct_goal_handoff_review_reject":  {actionClass: "goal.handoff.review.reject", targetField: "goal_id"},
	"atct_goal_handoff_report_amend":   {actionClass: "handoff_reported.goal", targetField: "goal_id"},
	"atct_plan_handoff_review_request": {actionClass: "plan.handoff.review.request", targetField: "goal_id"},
	"atct_plan_handoff_review_receive": {actionClass: "plan.handoff.review.receive", targetField: "goal_id"},
	"atct_plan_handoff_review_reject":  {actionClass: "plan.handoff.review.reject", targetField: "goal_id"},
}

type codexMonitorAckSender func(address, capability string, record codexMonitorAckRecord) error

type codexMonitorAckTransport struct {
	base       mcp.Transport
	address    string
	capability string
	send       codexMonitorAckSender
}

func newCodexMonitorAckTransport(base mcp.Transport, address, capability string, send codexMonitorAckSender) mcp.Transport {
	if base == nil || !validCodexMonitorAckConfiguration(address, capability) {
		return base
	}
	if send == nil {
		send = sendCodexMonitorAcknowledgement
	}
	return &codexMonitorAckTransport{base: base, address: address, capability: capability, send: send}
}

func newMCPTransport(base mcp.Transport) mcp.Transport {
	return newCodexMonitorAckTransport(
		base,
		os.Getenv(codexMonitorAckSocketEnvironment),
		os.Getenv(codexMonitorAckCapabilityEnvironment),
		sendCodexMonitorAcknowledgement,
	)
}

func validCodexMonitorAckConfiguration(address, capability string) bool {
	return address != "" && strings.TrimSpace(address) == address && !strings.ContainsRune(address, '\x00') &&
		capability != "" && strings.TrimSpace(capability) == capability && !strings.ContainsRune(capability, '\x00')
}

func (t *codexMonitorAckTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &codexMonitorAckConnection{
		base:       connection,
		address:    t.address,
		capability: t.capability,
		send:       t.send,
		pending:    make(map[string]codexMonitorAckMapping),
	}, nil
}

type codexMonitorAckConnection struct {
	base       mcp.Connection
	address    string
	capability string
	send       codexMonitorAckSender

	mu      sync.Mutex
	pending map[string]codexMonitorAckMapping
}

func (c *codexMonitorAckConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.base.Read(ctx)
	if err != nil {
		return nil, err
	}
	request, ok := message.(*jsonrpc.Request)
	if !ok || !request.ID.IsValid() {
		return message, nil
	}
	id, ok := codexMonitorAckRequestID(request.ID)
	if !ok {
		return message, nil
	}

	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()

	if request.Method != "tools/call" {
		return message, nil
	}
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return message, nil
	}
	mapping, ok := codexMonitorAckMappings[params.Name]
	if !ok {
		return message, nil
	}
	c.mu.Lock()
	c.pending[id] = mapping
	c.mu.Unlock()
	return message, nil
}

func (c *codexMonitorAckConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if err := c.base.Write(ctx, message); err != nil {
		return err
	}
	response, ok := message.(*jsonrpc.Response)
	if !ok || response.Error != nil {
		return nil
	}
	id, ok := codexMonitorAckRequestID(response.ID)
	if !ok {
		return nil
	}
	c.mu.Lock()
	mapping, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if !ok {
		return nil
	}
	key, ok := decodeCodexMonitorMutationKey(mapping, response.Result)
	if !ok || c.send == nil {
		return nil
	}
	_ = c.send(c.address, c.capability, codexMonitorAckRecord{
		Type: codexMonitorAckRecordAcknowledgement,
		Key:  key,
	})
	return nil
}

func (c *codexMonitorAckConnection) Close() error {
	return c.base.Close()
}

func (c *codexMonitorAckConnection) SessionID() string {
	return c.base.SessionID()
}

func codexMonitorAckRequestID(id jsonrpc.ID) (string, bool) {
	if !id.IsValid() {
		return "", false
	}
	return fmt.Sprintf("%T:%v", id.Raw(), id.Raw()), true
}

func decodeCodexMonitorMutationKey(mapping codexMonitorAckMapping, result json.RawMessage) (monitorMutationKey, bool) {
	var response struct {
		IsError           bool            `json:"isError"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(result, &response); err != nil || response.IsError || len(response.StructuredContent) == 0 {
		return monitorMutationKey{}, false
	}

	var structured map[string]json.RawMessage
	if err := json.Unmarshal(response.StructuredContent, &structured); err != nil {
		return monitorMutationKey{}, false
	}
	data, ok := structured["data"]
	if !ok {
		return monitorMutationKey{}, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return monitorMutationKey{}, false
	}
	handoffRaw, ok := codexMonitorAckResponseField(fields, "handoff_id")
	if !ok {
		return monitorMutationKey{}, false
	}
	var handoffID string
	if err := json.Unmarshal(handoffRaw, &handoffID); err != nil || strings.TrimSpace(handoffID) == "" {
		return monitorMutationKey{}, false
	}
	targetRaw, ok := codexMonitorAckResponseField(fields, mapping.targetField)
	if !ok {
		return monitorMutationKey{}, false
	}
	targetID, ok := decodeCodexMonitorNumericID(targetRaw)
	if !ok {
		return monitorMutationKey{}, false
	}
	key := monitorMutationKey{ActionClass: mapping.actionClass, HandoffID: strings.TrimSpace(handoffID), TargetID: targetID}
	if key.TargetID <= 0 || key.ActionClass == "" {
		return monitorMutationKey{}, false
	}
	return key, true
}

func codexMonitorAckResponseField(fields map[string]json.RawMessage, field string) (json.RawMessage, bool) {
	names := []string{field}
	switch field {
	case "handoff_id":
		names = append(names, "HandoffID", "ID", "id")
	case "task_id":
		names = append(names, "TaskID")
	case "goal_id":
		names = append(names, "GoalID")
	}
	for _, name := range names {
		if value, ok := fields[name]; ok {
			return value, true
		}
	}
	return nil, false
}

func decodeCodexMonitorNumericID(raw json.RawMessage) (int64, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return 0, false
	}
	var text string
	switch value := value.(type) {
	case json.Number:
		text = value.String()
	case string:
		text = strings.TrimSpace(value)
	default:
		return 0, false
	}
	targetID, err := strconv.ParseInt(text, 10, 64)
	if err != nil || targetID <= 0 {
		return 0, false
	}
	return targetID, true
}

func sendCodexMonitorAcknowledgement(address, capability string, record codexMonitorAckRecord) error {
	connection, err := net.DialTimeout("unix", address, codexMonitorAckDeadline)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetWriteDeadline(time.Now().Add(codexMonitorAckDeadline)); err != nil {
		return err
	}
	if err := json.NewEncoder(connection).Encode(codexMonitorAckEnvelope{Capability: capability, Record: record}); err != nil {
		return err
	}
	if err := connection.SetReadDeadline(time.Now().Add(codexMonitorAckDeadline)); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, connection)
	return err
}
