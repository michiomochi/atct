package daemon

import (
	"context"
	"fmt"

	"github.com/michiomochi/atct/internal/store"
)

type monitorCheckResponse struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// monitorCheck refuses ATCT work for a session that has no live Monitor.
// Without one, wakeup has nowhere to land: the work proceeds and the answer
// never arrives.
func (d *Daemon) monitorCheck(ctx context.Context, sessionKey string) (monitorCheckResponse, error) {
	agentSessionID, err := d.store.AgentSessionIDByKey(ctx, sessionKey)
	if err != nil {
		return monitorCheckResponse{}, fmt.Errorf("resolve session key: %w", err)
	}
	role, err := d.deriveSessionRole(ctx, agentSessionID)
	if err != nil {
		return monitorCheckResponse{}, fmt.Errorf("derive session role: %w", err)
	}
	// No project or role means no Monitor can be attached to this session.
	// Report that, not the scope validation error.
	if role.ProjectID > 0 && role.Role != "" {
		scope := store.MonitorLiveScope{ProjectID: role.ProjectID, Role: role.Role}
		if role.GoalID != 0 {
			goalID := role.GoalID
			scope.GoalID = &goalID
		}
		live, err := d.store.HasLiveMonitorForScope(ctx, scope)
		if err != nil {
			return monitorCheckResponse{}, err
		}
		if live {
			return monitorCheckResponse{}, nil
		}
	}
	return monitorCheckResponse{
		Decision: "block",
		Reason: "ATCT: this session has no live Monitor, so a wakeup would never reach it. " +
			"Claude Code: run `atct watch --monitor --token <monitor_token>` with the token from SessionStart. " +
			"Codex: relaunch the pane through `atct codex monitor -- <codex args>`. " +
			"Then retry.",
	}, nil
}
