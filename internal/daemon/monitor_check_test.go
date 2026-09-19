package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
)

func monitorCheckDecision(t *testing.T, fixture goalListFixture, sessionKey string) monitorCheckResponse {
	t.Helper()
	params, err := json.Marshal(map[string]any{"session_key": sessionKey})
	if err != nil {
		t.Fatalf("marshal session.monitor_check params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "session.monitor_check", Params: params})
	if err != nil {
		t.Fatalf("session.monitor_check: %v", err)
	}
	var response monitorCheckResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode session.monitor_check response: %v", err)
	}
	return response
}

// A session earns its Monitor scope by receiving a handoff, and the gate asks
// for a live Monitor before it lets any ATCT tool run. Gating a session that
// has no scope yet closes the only door into the loop: it can never receive,
// so it can never be assigned, so it can never have a Monitor.
func TestMonitorCheckAllowsASessionWithNoAssignmentYet(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	const sessionKey = "monitor-check-unassigned"
	sessionID := daemonTestSessionID(t, fixture.store, "monitor-check-unassigned")
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, sessionID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}

	response := monitorCheckDecision(t, fixture, sessionKey)
	if response.Decision != "" {
		t.Fatalf("monitor_check denied a session with no assignment: %+v", response)
	}
}

// Once the session does hold a scope, a missing Monitor is a real problem: a
// wakeup addressed to that scope would never arrive.
func TestMonitorCheckStillDeniesAnAssignedSessionWithoutAMonitor(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	const sessionKey = "monitor-check-assigned"
	sessionID := daemonTestSessionID(t, fixture.store, "monitor-check-assigned")
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, sessionID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, sessionID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	response := monitorCheckDecision(t, fixture, sessionKey)
	if response.Decision != "block" {
		t.Fatalf("monitor_check allowed a commander with no live Monitor: %+v", response)
	}
}

// The whole way in, end to end. A pane opens with nothing but a handoff id in
// its launch message: no claim, no assignment, no Monitor. It has to be able
// to receive, because receiving is what earns it the scope a Monitor binds to.
func TestAFreshSessionCanReceiveItsGoalHandoffAndBecomeSubcommander(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	goalID := fixture.active[0].ID

	// The commander registers the handoff before the worker pane exists.
	commanderID := daemonTestSessionID(t, fixture.store, "entry-commander")
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	const handoffID = "goal-handoff-entry"
	if _, err := fixture.store.RequestGoalHandoff(ctx, handoffID, goalID, commanderID, "do the goal"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}

	// The pane opens and identifies itself. Nothing else is true of it yet.
	const sessionKey = "entry-subcommander"
	workerID := daemonTestSessionID(t, fixture.store, "entry-subcommander")
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, workerID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	if assignment, err := fixture.store.MonitorAssignment(ctx, workerID); err != nil {
		t.Fatalf("MonitorAssignment: %v", err)
	} else if assignment.ProjectID != 0 {
		t.Fatalf("a session that has received nothing already has a scope: %+v", assignment)
	}

	// The gate must let it through: it holds no scope, so no Monitor could
	// exist for it, and no wakeup could be addressed to it either.
	if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "" {
		t.Fatalf("the gate denied the call that would create the scope: %+v", response)
	}

	params, err := json.Marshal(map[string]any{
		"handoff_id":  handoffID,
		"goal_id":     goalID,
		"received_by": workerID,
	})
	if err != nil {
		t.Fatalf("marshal goal.handoff.receive params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "goal.handoff.receive", Params: params}); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}

	// Receiving is what makes it a subcommander, which is the scope its
	// Monitor binds to.
	assignment, err := fixture.store.MonitorAssignment(ctx, workerID)
	if err != nil {
		t.Fatalf("MonitorAssignment after receive: %v", err)
	}
	if assignment.Role != "subcommander" || assignment.GoalID != goalID || assignment.ProjectID != fixture.project.ID {
		t.Fatalf("assignment after receive = %+v, want subcommander on project %d goal %d", assignment, fixture.project.ID, goalID)
	}

	// And from here the gate does apply: the scope exists, so a missing
	// Monitor is a real problem again.
	if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "block" {
		t.Fatalf("the gate stopped applying once the session held a scope: %+v", response)
	}
}
