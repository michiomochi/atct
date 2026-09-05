package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/michiomochi/atct/internal/store"
)

type handoffEntryRPCParams struct {
	HandoffID         string `json:"handoff_id"`
	TaskID            int64  `json:"task_id"`
	GoalID            int64  `json:"goal_id"`
	Kind              string `json:"kind"`
	Body              string `json:"body"`
	RelatesTo         string `json:"relates_to"`
	AgentSessionID    int64  `json:"agent_session_id"`
	Capability        string `json:"capability"`
	CallerCapability  string `json:"caller_capability"`
	MonitorCapability string `json:"monitor_capability"`
}

type handoffHistoryRPCParams struct {
	HandoffID         string `json:"handoff_id"`
	TaskID            int64  `json:"task_id"`
	GoalID            int64  `json:"goal_id"`
	Cursor            int64  `json:"cursor"`
	Limit             int    `json:"limit"`
	AgentSessionID    int64  `json:"agent_session_id"`
	Capability        string `json:"capability"`
	CallerCapability  string `json:"caller_capability"`
	MonitorCapability string `json:"monitor_capability"`
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
	if err := json.Unmarshal(raw, &p); err != nil {
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
	entry, err := d.store.AppendTaskHandoffEntry(ctx, p.HandoffID, p.Kind, p.Body, p.AgentSessionID, p.RelatesTo)
	return marshal(entry, err)
}

func (d *Daemon) appendGoalHandoffEntryRPC(ctx context.Context, raw json.RawMessage, peerID uint64) (json.RawMessage, error) {
	var p handoffEntryRPCParams
	if err := json.Unmarshal(raw, &p); err != nil {
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
	entry, err := d.store.AppendGoalHandoffEntry(ctx, p.HandoffID, p.Kind, p.Body, p.AgentSessionID, p.RelatesTo)
	return marshal(entry, err)
}

func (d *Daemon) historyTaskHandoffEntryRPC(ctx context.Context, raw json.RawMessage, peerID uint64) (json.RawMessage, error) {
	var p handoffHistoryRPCParams
	if err := json.Unmarshal(raw, &p); err != nil {
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
	page, err := d.store.ListTaskHandoffEntries(ctx, p.HandoffID, p.Cursor, p.Limit)
	return marshal(page, err)
}

func (d *Daemon) historyGoalHandoffEntryRPC(ctx context.Context, raw json.RawMessage, peerID uint64) (json.RawMessage, error) {
	var p handoffHistoryRPCParams
	if err := json.Unmarshal(raw, &p); err != nil {
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
	page, err := d.store.ListGoalHandoffEntries(ctx, p.HandoffID, p.Cursor, p.Limit)
	return marshal(page, err)
}

func requireHandoffParticipant(agentSessionID, requestedBy, receivedBy int64) error {
	if agentSessionID <= 0 || (agentSessionID != requestedBy && agentSessionID != receivedBy) {
		return fmt.Errorf("%w: author %d is not a thread participant", store.ErrHandoffEntryParticipant, agentSessionID)
	}
	return nil
}
