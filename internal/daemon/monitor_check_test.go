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
