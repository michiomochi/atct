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
	// A session with no project and no role has no scope yet, and a Monitor
	// binds to a scope. Denying here closes the only way in: the assignment
	// comes from receiving a handoff or claiming a project, the Monitor comes
	// from the assignment, and the gate would demand the Monitor first. Nothing
	// is being protected either, because a wakeup is addressed to a scope and
	// this session is in none of them.
	if role.ProjectID <= 0 || role.Role == "" {
		return monitorCheckResponse{}, nil
	}
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
	return monitorCheckResponse{
		Decision: "block",
		// Claude Code can attach its own Monitor, so tell it how. Codex cannot:
		// the Monitor belongs to the supervisor that started the pane, and an
		// agent inside the pane has no way to start another. Telling it to
		// relaunch the pane is an instruction to the wrong reader, so say what
		// it can do instead: report what it already holds, and let the
		// delegator replace the worker.
		Reason: "ATCT: this session has no live Monitor, so a wakeup would never reach it. " +
			"Claude Code: run `atct watch --monitor --token <monitor_token>` with the token from SessionStart, then retry. " +
			"Codex: you cannot restart your own Monitor from inside this pane. " +
			"If the work you hold is finished, report it with the review request, which is still allowed. " +
			"Otherwise stop and say so: your delegator sees the lost Monitor and replaces the worker.",
	}, nil
}
