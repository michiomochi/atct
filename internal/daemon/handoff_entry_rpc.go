package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

type handoffEntryRPCParams struct {
	HandoffID         string `json:"handoff_id"`
	TaskID            int64  `json:"task_id"`
	GoalID            int64  `json:"goal_id"`
	Kind              string `json:"kind"`
	Body              string `json:"body"`
	InReplyToID       *int64 `json:"in_reply_to_id,omitempty"`
	AgentSessionID    int64  `json:"agent_session_id"`
	Capability        string `json:"capability"`
	CallerCapability  string `json:"caller_capability"`
	MonitorCapability string `json:"monitor_capability"`
}

type handoffHistoryRPCParams struct {
	HandoffID         string `json:"handoff_id"`
	TaskID            int64  `json:"task_id"`
	GoalID            int64  `json:"goal_id"`
	AfterID           int64  `json:"after_id"`
	Limit             int    `json:"limit"`
	AgentSessionID    int64  `json:"agent_session_id"`
	Capability        string `json:"capability"`
	CallerCapability  string `json:"caller_capability"`
	MonitorCapability string `json:"monitor_capability"`
}

type handoffEntryRPCOutput struct {
	ID              int64     `json:"id"`
	HandoffID       string    `json:"handoff_id"`
	Kind            string    `json:"kind"`
	Body            string    `json:"body"`
	AuthorSessionID int64     `json:"author_session_id"`
	InReplyToID     *int64    `json:"in_reply_to_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type handoffEntryHistoryRPCOutput struct {
	Entries     []handoffEntryRPCOutput `json:"entries"`
	HasMore     bool                    `json:"has_more"`
	NextAfterID int64                   `json:"next_after_id"`
}

func decodeHandoffEntryRPCParams(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("handoff entry params must contain one JSON value")
		}
		return err
	}
	return nil
}

func inReplyToStoreValue(value *int64) (string, error) {
	if value == nil {
		return "", nil
	}
	if *value <= 0 {
		return "", fmt.Errorf("%w: in_reply_to_id must be a positive integer", store.ErrHandoffEntryRelatesToInvalid)
	}
	return strconv.FormatInt(*value, 10), nil
}

func canonicalHandoffEntryRPCOutput(entry store.HandoffEntry) handoffEntryRPCOutput {
	return handoffEntryRPCOutput{
		ID:              entry.ID,
		HandoffID:       entry.HandoffID,
		Kind:            entry.Kind,
		Body:            entry.Body,
		AuthorSessionID: entry.AuthorSessionID,
		InReplyToID:     entry.InReplyToID,
		CreatedAt:       entry.CreatedAt,
	}
}

func canonicalHandoffEntryHistoryRPCOutput(page store.HandoffEntryPage) handoffEntryHistoryRPCOutput {
	entries := make([]handoffEntryRPCOutput, 0, len(page.Entries))
	for _, entry := range page.Entries {
		entries = append(entries, canonicalHandoffEntryRPCOutput(entry))
	}
	return handoffEntryHistoryRPCOutput{
		Entries:     entries,
		HasMore:     page.HasMore,
		NextAfterID: page.NextCursor,
	}
}

func marshalCanonicalHandoffEntry(entry store.HandoffEntry, err error) (json.RawMessage, error) {
	return marshal(canonicalHandoffEntryRPCOutput(entry), err)
}

func marshalCanonicalHandoffEntryHistory(page store.HandoffEntryPage, err error) (json.RawMessage, error) {
	return marshal(canonicalHandoffEntryHistoryRPCOutput(page), err)
}

// marshalCanonicalHandoff preserves the established parent handoff fields,
// while replacing nested entry/page payloads with the canonical caller
// contract. Parent request/complete reports remain compatibility fields until
// their downstream callers are cut over.
func marshalCanonicalHandoff(handoff any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(handoff)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if rawEntries, ok := fields["entries"]; ok {
		var entries []store.HandoffEntry
		if err := json.Unmarshal(rawEntries, &entries); err != nil {
			return nil, fmt.Errorf("decode handoff entries: %w", err)
		}
		encoded, err := json.Marshal(canonicalHandoffEntries(entries))
		if err != nil {
			return nil, err
		}
		fields["entries"] = encoded
	}
	if rawHistory, ok := fields["history"]; ok {
		var page store.HandoffEntryPage
		if err := json.Unmarshal(rawHistory, &page); err != nil {
			return nil, fmt.Errorf("decode handoff history: %w", err)
		}
		encoded, err := json.Marshal(canonicalHandoffEntryHistoryRPCOutput(page))
		if err != nil {
			return nil, err
		}
		fields["history"] = encoded
	}
	if cursor, ok := fields["next_cursor"]; ok {
		fields["next_after_id"] = cursor
		delete(fields, "next_cursor")
	}
	return json.Marshal(fields)
}

func marshalCanonicalHandoffWithRoleEvidence(handoff any, response responseWithRoleEvidence) (json.RawMessage, error) {
	raw, err := marshalCanonicalHandoff(handoff, nil)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields["data"] = raw
	role, err := json.Marshal(response.Role)
	if err != nil {
		return nil, err
	}
	fields["role"] = role
	evidence, err := json.Marshal(response.ClaimEvidence)
	if err != nil {
		return nil, err
	}
	fields["claim_evidence"] = evidence
	return json.Marshal(fields)
}

func canonicalHandoffEntries(entries []store.HandoffEntry) []handoffEntryRPCOutput {
	output := make([]handoffEntryRPCOutput, 0, len(entries))
	for _, entry := range entries {
		output = append(output, canonicalHandoffEntryRPCOutput(entry))
	}
	return output
}

func (p handoffEntryRPCParams) capabilityFields() capabilityFields {
	return capabilityFields{
		Capability: p.Capability, CallerCapability: p.CallerCapability, MonitorCapability: p.MonitorCapability,
	}
}

func (p handoffHistoryRPCParams) capabilityFields() capabilityFields {
	return capabilityFields{
		Capability: p.Capability, CallerCapability: p.CallerCapability, MonitorCapability: p.MonitorCapability,
	}
}

func (d *Daemon) appendTaskHandoffEntryRPC(ctx context.Context, raw json.RawMessage, peerID uint64) (json.RawMessage, error) {
	var p handoffEntryRPCParams
	if err := decodeHandoffEntryRPCParams(raw, &p); err != nil {
		return nil, err
	}
	handoff, err := d.store.GetTaskHandoff(ctx, p.HandoffID)
	if err != nil {
		return nil, err
	}
	if handoff.TaskID != p.TaskID {
		return nil, fmt.Errorf("%w: %q belongs to task %d, not %d", store.ErrTaskHandoffTaskMismatch, p.HandoffID, handoff.TaskID, p.TaskID)
	}
	p.AgentSessionID, err = d.sessionFromCapability(p.capabilityFields(), p.AgentSessionID, peerID)
	if err != nil {
		return nil, err
	}
	relatesTo, err := inReplyToStoreValue(p.InReplyToID)
	if err != nil {
		return nil, err
	}
	entry, err := d.store.AppendTaskHandoffEntry(ctx, p.HandoffID, p.Kind, p.Body, p.AgentSessionID, relatesTo)
	return marshalCanonicalHandoffEntry(entry, err)
}

func (d *Daemon) appendGoalHandoffEntryRPC(ctx context.Context, raw json.RawMessage, peerID uint64) (json.RawMessage, error) {
	var p handoffEntryRPCParams
	if err := decodeHandoffEntryRPCParams(raw, &p); err != nil {
		return nil, err
	}
	handoff, err := d.store.GetGoalHandoff(ctx, p.HandoffID)
	if err != nil {
		return nil, err
	}
	if handoff.GoalID != p.GoalID {
		return nil, fmt.Errorf("%w: %q belongs to goal %d, not %d", store.ErrGoalHandoffGoalMismatch, p.HandoffID, handoff.GoalID, p.GoalID)
	}
	p.AgentSessionID, err = d.sessionFromCapability(p.capabilityFields(), p.AgentSessionID, peerID)
	if err != nil {
		return nil, err
	}
	relatesTo, err := inReplyToStoreValue(p.InReplyToID)
	if err != nil {
		return nil, err
	}
	entry, err := d.store.AppendGoalHandoffEntry(ctx, p.HandoffID, p.Kind, p.Body, p.AgentSessionID, relatesTo)
	return marshalCanonicalHandoffEntry(entry, err)
}

func (d *Daemon) historyTaskHandoffEntryRPC(ctx context.Context, raw json.RawMessage, peerID uint64) (json.RawMessage, error) {
	var p handoffHistoryRPCParams
	if err := decodeHandoffEntryRPCParams(raw, &p); err != nil {
		return nil, err
	}
	handoff, err := d.store.GetTaskHandoff(ctx, p.HandoffID)
	if err != nil {
		return nil, err
	}
	if handoff.TaskID != p.TaskID {
		return nil, fmt.Errorf("%w: %q belongs to task %d, not %d", store.ErrTaskHandoffTaskMismatch, p.HandoffID, handoff.TaskID, p.TaskID)
	}
	p.AgentSessionID, err = d.sessionFromCapability(p.capabilityFields(), p.AgentSessionID, peerID)
	if err != nil {
		return nil, err
	}
	if err := requireHandoffParticipant(p.AgentSessionID, handoff.RequestedBy, handoff.ReceivedBy); err != nil {
		return nil, err
	}
	if p.Limit == 0 {
		p.Limit = store.HandoffHistoryMaxLimit
	}
	page, err := d.store.ListTaskHandoffEntries(ctx, p.HandoffID, p.AfterID, p.Limit)
	return marshalCanonicalHandoffEntryHistory(page, err)
}

func (d *Daemon) historyGoalHandoffEntryRPC(ctx context.Context, raw json.RawMessage, peerID uint64) (json.RawMessage, error) {
	var p handoffHistoryRPCParams
	if err := decodeHandoffEntryRPCParams(raw, &p); err != nil {
		return nil, err
	}
	handoff, err := d.store.GetGoalHandoff(ctx, p.HandoffID)
	if err != nil {
		return nil, err
	}
	if handoff.GoalID != p.GoalID {
		return nil, fmt.Errorf("%w: %q belongs to goal %d, not %d", store.ErrGoalHandoffGoalMismatch, p.HandoffID, handoff.GoalID, p.GoalID)
	}
	p.AgentSessionID, err = d.sessionFromCapability(p.capabilityFields(), p.AgentSessionID, peerID)
	if err != nil {
		return nil, err
	}
	if err := requireHandoffParticipant(p.AgentSessionID, handoff.RequestedBy, handoff.ReceivedBy); err != nil {
		return nil, err
	}
	if p.Limit == 0 {
		p.Limit = store.HandoffHistoryMaxLimit
	}
	page, err := d.store.ListGoalHandoffEntries(ctx, p.HandoffID, p.AfterID, p.Limit)
	return marshalCanonicalHandoffEntryHistory(page, err)
}

func requireHandoffParticipant(agentSessionID, requestedBy, receivedBy int64) error {
	if agentSessionID <= 0 || (agentSessionID != requestedBy && agentSessionID != receivedBy) {
		return fmt.Errorf("%w: author %d is not a thread participant", store.ErrHandoffEntryParticipant, agentSessionID)
	}
	return nil
}
