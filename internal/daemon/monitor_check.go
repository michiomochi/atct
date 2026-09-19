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
	assignment, err := d.store.MonitorAssignment(ctx, agentSessionID)
	if err != nil {
		return monitorCheckResponse{}, fmt.Errorf("derive monitor assignment: %w", err)
	}
	// A session with no project and no role has no scope yet, and a Monitor
	// binds to a scope. Denying here closes the only way in: the assignment
	// comes from receiving a handoff or claiming a project, the Monitor comes
	// from the assignment, and the gate would demand the Monitor first. Nothing
	// is being protected either, because a wakeup is addressed to a scope and
	// this session is in none of them.
	if assignment.ProjectID <= 0 || assignment.Role == "" {
		return monitorCheckResponse{}, nil
	}
	hasLive := func(scope store.MonitorLiveScope) (bool, error) {
		return d.store.HasLiveMonitorForScope(ctx, scope)
	}
	if assignment.Role == "executor" {
		for _, task := range assignment.Tasks {
			if task.ProjectID <= 0 || task.GoalID <= 0 {
				continue
			}
			goalID := task.GoalID
			live, err := hasLive(store.MonitorLiveScope{ProjectID: task.ProjectID, Role: assignment.Role, GoalID: &goalID})
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
	scope := store.MonitorLiveScope{ProjectID: assignment.ProjectID, Role: assignment.Role}
	if assignment.GoalID != 0 {
		goalID := assignment.GoalID
		scope.GoalID = &goalID
	}
	live, err := hasLive(scope)
	if err != nil {
		return monitorCheckResponse{}, err
	}
	if live {
		return monitorCheckResponse{}, nil
	}
	return monitorCheckResponse{
		Decision: "block",
		Reason: "ATCT: this session has no live Monitor, so a wakeup would never reach it. " +
			"Claude Code: run `atct watch --monitor --token <monitor_token>` with the token from SessionStart. " +
			"Codex: relaunch the pane through `atct codex monitor -- <codex args>`. " +
			"Then retry.",
	}, nil
}
