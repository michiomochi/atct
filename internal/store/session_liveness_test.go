package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// sessionMonitorHealth reports a Monitor serving agentSessionID, the way
// `atct watch --monitor` and `atct codex monitor` do.
func sessionMonitorHealth(t *testing.T, s *Store, agentSessionID int64, lastSeen time.Time, stopped bool) string {
	t.Helper()
	goalID := int64(11)
	health := MonitorHealth{
		CWD:              "/tmp/monitor-project/worktree",
		Role:             "subcommander",
		State:            "healthy",
		ProjectID:        7,
		GoalID:           &goalID,
		AgentSessionID:   agentSessionID,
		PID:              os.Getpid(),
		ProcessStartedAt: lastSeen.Add(-time.Minute),
		TransitionedAt:   lastSeen,
		LastSeenAt:       lastSeen,
	}
	health.MonitorID = MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
	if err := s.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatalf("UpsertMonitorHealth: %v", err)
	}
	if stopped {
		if err := s.StopMonitorHealth(context.Background(), health.MonitorID, lastSeen); err != nil {
			t.Fatalf("StopMonitorHealth: %v", err)
		}
	}
	return health.MonitorID
}

// Over the HTTP MCP transport the session has no process of its own: the
// daemon serves every session, so agent_sessions.pid names the daemon, which
// is always alive and can never be disproven. The session's Monitor is a
// process per session, and it stops heartbeating when the pane closes.
func TestSessionLivenessFollowsAStoppedMonitor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Registered with this live process, so only the Monitor can make it stale.
	sessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	_ = sessionMonitorHealth(t, s, sessionID, time.Now().UTC().Add(-MonitorHealthLease-time.Second), false)

	if claimIsRunning(ctx, s, sessionID) {
		t.Error("claimIsRunning = true though the session's Monitor stopped heartbeating")
	}
	if !claimIsDefinitelyDead(ctx, s, sessionID) {
		t.Error("claimIsDefinitelyDead = false though the session's Monitor stopped heartbeating")
	}
}

func TestSessionLivenessSeesAHeartbeatingMonitor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Registered with a process that is gone, so only the Monitor can make it live.
	sessionID, err := s.RegisterAgentSession(ctx, 999000)
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	_ = sessionMonitorHealth(t, s, sessionID, time.Now().UTC(), false)

	if !claimIsRunning(ctx, s, sessionID) {
		t.Error("claimIsRunning = false though the session's Monitor is heartbeating")
	}
	if claimIsDefinitelyDead(ctx, s, sessionID) {
		t.Error("claimIsDefinitelyDead = true though the session's Monitor is heartbeating")
	}
}

// A Monitor that reported its own stop is gone even inside the lease window.
func TestSessionLivenessFollowsAMonitorThatReportedItsStop(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	_ = sessionMonitorHealth(t, s, sessionID, time.Now().UTC(), true)

	if claimIsRunning(ctx, s, sessionID) {
		t.Error("claimIsRunning = true though the session's Monitor reported its stop")
	}
	if !claimIsDefinitelyDead(ctx, s, sessionID) {
		t.Error("claimIsDefinitelyDead = false though the session's Monitor reported its stop")
	}
}

// A session that never reported a Monitor keeps the old rule.
func TestSessionLivenessFallsBackToTheSessionProcess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	live, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(live): %v", err)
	}
	if !claimIsRunning(ctx, s, live) {
		t.Error("claimIsRunning = false for a session whose own process is alive")
	}

	dead, err := s.RegisterAgentSession(ctx, 999000)
	if err != nil {
		t.Fatalf("RegisterAgentSession(dead): %v", err)
	}
	if claimIsRunning(ctx, s, dead) {
		t.Error("claimIsRunning = true for a session whose own process is gone")
	}
}

// The monitor knows the token it was launched with, not the id of the session
// it serves. monitor_bindings is the only record linking the two, so the
// server resolves it: without this the agent_session_id column stays 0 and
// session liveness never sees a Monitor at all.
func TestMonitorHealthResolvesItsSessionFromTheBoundToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID, err := s.RegisterAgentSession(ctx, 999000)
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	const token = "monitor-token-for-liveness"
	if err := s.BindMonitorToken(ctx, token, sessionID); err != nil {
		t.Fatalf("BindMonitorToken: %v", err)
	}

	now := time.Now().UTC()
	goalID := int64(11)
	health := MonitorHealth{
		CWD:              "/tmp/monitor-project/worktree",
		Role:             "subcommander",
		State:            "healthy",
		ProjectID:        7,
		GoalID:           &goalID,
		MonitorToken:     token,
		PID:              os.Getpid(),
		ProcessStartedAt: now.Add(-time.Minute),
		TransitionedAt:   now,
		LastSeenAt:       now,
	}
	health.MonitorID = MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
	if err := s.UpsertMonitorHealth(ctx, health); err != nil {
		t.Fatalf("UpsertMonitorHealth: %v", err)
	}

	var stored int64
	if err := s.DB().QueryRowContext(ctx, `SELECT agent_session_id FROM monitor_health WHERE monitor_id = ?`, health.MonitorID).Scan(&stored); err != nil {
		t.Fatalf("read stored agent_session_id: %v", err)
	}
	if stored != sessionID {
		t.Fatalf("stored agent_session_id = %d, want %d", stored, sessionID)
	}
	if !claimIsRunning(ctx, s, sessionID) {
		t.Error("claimIsRunning = false though the bound Monitor is heartbeating")
	}
}
