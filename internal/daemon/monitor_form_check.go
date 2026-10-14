package daemon

import (
	"context"
)

// monitorFormCheck refuses a Monitor-form watch for a subcommander whose only
// open work is an answer it is waiting for. The Monitor expires every 30
// minutes with nothing new to report, so a --once Bash is the right form there.
// Any failure allows the Monitor: this check must never block a watch it
// cannot judge.
func (d *Daemon) monitorFormCheck(ctx context.Context, sessionKey string) monitorCheckResponse {
	agentSessionID, err := d.store.AgentSessionIDByKey(ctx, sessionKey)
	if err != nil {
		return monitorCheckResponse{}
	}
	return d.monitorFormCheckFor(ctx, agentSessionID)
}

func (d *Daemon) monitorFormCheckFor(ctx context.Context, agentSessionID int64) monitorCheckResponse {
	role, err := d.deriveSessionRole(ctx, agentSessionID)
	if err != nil || role.Role != "subcommander" || role.GoalID == 0 {
		return monitorCheckResponse{}
	}
	goal, err := d.store.GetGoal(ctx, role.GoalID)
	if err != nil {
		return monitorCheckResponse{}
	}
	// An archived project is refused by the stop check and gets no Monitor check either.
	if err := d.store.EnsureProjectActive(ctx, goal.ProjectID); err != nil {
		return monitorCheckResponse{}
	}
	// Judged as if the Monitor were live: the question is whether anything
	// besides the answer is still open, not whether a Monitor is attached.
	live := true
	detail, err := d.subcommanderStopDetail(ctx, agentSessionID, goal.ID, &live)
	if err != nil || detail != "" {
		return monitorCheckResponse{}
	}
	goalHandoffs, err := d.store.ListGoalHandoffs(ctx, goal.ID)
	if err != nil {
		return monitorCheckResponse{}
	}
	if !d.subcommanderWaiting(ctx, agentSessionID, goal, goalHandoffs, true) {
		return monitorCheckResponse{}
	}
	return monitorCheckResponse{
		Decision: "block",
		Reason: "ATCT: this session holds only an answer it is waiting for, so a Monitor adds nothing: " +
			"it expires every 30 minutes and wakes the session with no event. " +
			"Use Bash with `run_in_background` and `atct watch --monitor --token <monitor_token> --once` instead: " +
			"it ends at the first event and has no expiry. Then retry.",
	}
}
