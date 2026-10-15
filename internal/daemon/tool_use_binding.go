package daemon

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	// A permission prompt can keep a tool call waiting for a while, so the
	// binding must outlive the hook by more than a few seconds.
	toolUseBindingTTL   = 30 * time.Minute
	toolUseBindingLimit = 1024
)

type toolUseBinding struct {
	sessionKey string
	expiresAt  time.Time
}

// toolUseBindings maps a Claude Code tool_use_id to the session_key its
// PreToolUse hook reported. The MCP request for the same call carries the same
// id in _meta, which lets the transport attach to the identified session without
// the agent calling atct_session_identify. Memory only: a restarted daemon
// forgets them, and the agent falls back to atct_session_identify.
type toolUseBindings struct {
	mu      sync.Mutex
	entries map[string]toolUseBinding
}

func (b *toolUseBindings) add(now time.Time, toolUseID, sessionKey string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = make(map[string]toolUseBinding)
	}
	b.entries[toolUseID] = toolUseBinding{sessionKey: sessionKey, expiresAt: now.Add(toolUseBindingTTL)}
	for id, entry := range b.entries {
		if !now.Before(entry.expiresAt) {
			delete(b.entries, id)
		}
	}
	// ponytail: the oldest entry is found by a linear scan. Use a heap if the table ever runs near the limit.
	for len(b.entries) > toolUseBindingLimit {
		oldest := ""
		for id, entry := range b.entries {
			if oldest == "" || entry.expiresAt.Before(b.entries[oldest].expiresAt) {
				oldest = id
			}
		}
		delete(b.entries, oldest)
	}
}

// take returns the session_key bound to toolUseID and consumes the binding, so
// one tool call attaches at most one transport.
func (b *toolUseBindings) take(now time.Time, toolUseID string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.entries[toolUseID]
	if !ok {
		return "", false
	}
	delete(b.entries, toolUseID)
	if !now.Before(entry.expiresAt) {
		return "", false
	}
	return entry.sessionKey, true
}

type toolUseBindResponse struct {
	OK bool `json:"ok"`
}

// bindToolUse records the binding only for a session_key that session.identify
// has registered. Any other key is not an error: the hook must not block the
// tool, and the MCP side then behaves as it did before this binding existed.
func (d *Daemon) bindToolUse(ctx context.Context, sessionKey, toolUseID string) toolUseBindResponse {
	toolUseID = strings.TrimSpace(toolUseID)
	if toolUseID == "" {
		return toolUseBindResponse{}
	}
	if _, err := d.store.AgentSessionIDByKey(ctx, sessionKey); err != nil {
		return toolUseBindResponse{}
	}
	d.toolUse.add(d.clock(), toolUseID, strings.TrimSpace(sessionKey))
	return toolUseBindResponse{OK: true}
}

type toolUseResolveResponse struct {
	AgentSessionID int64 `json:"agent_session_id"`
}

// resolveToolUse returns the canonical agent session for the binding of
// toolUseID, or 0 when there is none. A binding attaches the transport
// agentSessionID to that session exactly as session.identify would, so the
// discard and key checks of IdentifyAgentSession still apply.
func (d *Daemon) resolveToolUse(ctx context.Context, agentSessionID int64, toolUseID string) (toolUseResolveResponse, error) {
	sessionKey, ok := d.toolUse.take(d.clock(), strings.TrimSpace(toolUseID))
	if !ok {
		return toolUseResolveResponse{}, nil
	}
	canonicalID, err := d.store.AgentSessionIDByKey(ctx, sessionKey)
	if err != nil {
		return toolUseResolveResponse{}, err
	}
	if canonicalID == agentSessionID {
		return toolUseResolveResponse{AgentSessionID: canonicalID}, nil
	}
	canonicalID, _, err = d.store.IdentifyAgentSession(ctx, agentSessionID, sessionKey)
	if err != nil {
		return toolUseResolveResponse{}, err
	}
	return toolUseResolveResponse{AgentSessionID: canonicalID}, nil
}
