package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// A session used to be judged by the process recorded against it. Over the
// HTTP transport that process is the daemon, alive as long as ATCT runs, so
// every session read as live and no claim could ever be reclaimed. The lease
// is held by the monitor, which is one process per session and dies with the
// pane.
func TestSessionIsDeadWithoutALease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Registered against this very process, which is unmistakably alive. Rows
	// that predate the lease have no heartbeat, which is what this stands in
	// for; registering starts one, so clear it.
	sessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET last_heartbeat_at = NULL WHERE id = ?`, sessionID); err != nil {
		t.Fatalf("clear lease: %v", err)
	}

	if s.AgentSessionLive(ctx, sessionID, time.Now()) {
		t.Error("a session that never held a lease reads as live")
	}
	if claimIsRunning(ctx, s, sessionID) {
		t.Error("claimIsRunning = true for a session that never held a lease")
	}
	if !claimIsDefinitelyDead(ctx, s, sessionID) {
		t.Error("claimIsDefinitelyDead = false for a session that never held a lease")
	}
}

func TestSessionIsLiveWhileItsLeaseHolds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Registered against a process that is gone, so only the lease can answer.
	sessionID, err := s.RegisterAgentSession(ctx, 999000)
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	now := time.Now().UTC()
	// The monitor renews it; the recorded process stays gone throughout.
	if err := s.HeartbeatAgentSession(ctx, sessionID, now); err != nil {
		t.Fatalf("HeartbeatAgentSession: %v", err)
	}

	if !s.AgentSessionLive(ctx, sessionID, now) {
		t.Error("a session that just renewed its lease reads as dead")
	}
	if !claimIsRunning(ctx, s, sessionID) {
		t.Error("claimIsRunning = false while the lease holds")
	}
	if claimIsDefinitelyDead(ctx, s, sessionID) {
		t.Error("claimIsDefinitelyDead = true while the lease holds")
	}
}

func TestSessionIsDeadOnceItsLeaseLapses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	now := time.Now().UTC()
	if err := s.HeartbeatAgentSession(ctx, sessionID, now.Add(-RuntimeLeaseDuration-time.Second)); err != nil {
		t.Fatalf("HeartbeatAgentSession: %v", err)
	}

	if s.AgentSessionLive(ctx, sessionID, now) {
		t.Error("a lapsed lease still reads as live")
	}
	if !claimIsDefinitelyDead(ctx, s, sessionID) {
		t.Error("a lapsed lease is not treated as proof the session is gone")
	}
}

// The monitor polls its binding every second; the lease only needs renewing a
// few times a minute, and every renewal is a write.
func TestRenewMonitorLeaseThrottlesItsWrites(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	const token = "monitor-token-lease"
	if err := s.BindMonitorToken(ctx, token, sessionID); err != nil {
		t.Fatalf("BindMonitorToken: %v", err)
	}

	if err := s.RenewMonitorLease(ctx, token); err != nil {
		t.Fatalf("RenewMonitorLease(first): %v", err)
	}
	first := sessionHeartbeat(t, s, sessionID)
	if first == "" {
		t.Fatal("the first poll did not start a lease")
	}
	if !s.AgentSessionLive(ctx, sessionID, time.Now()) {
		t.Fatal("the session is not live after its monitor polled")
	}

	if err := s.RenewMonitorLease(ctx, token); err != nil {
		t.Fatalf("RenewMonitorLease(second): %v", err)
	}
	if second := sessionHeartbeat(t, s, sessionID); second != first {
		t.Fatalf("a poll inside the throttle window wrote again: %q -> %q", first, second)
	}
}

func TestRenewMonitorLeaseRejectsAnUnboundToken(t *testing.T) {
	s := newTestStore(t)
	if err := s.RenewMonitorLease(context.Background(), "never-bound"); err == nil {
		t.Fatal("RenewMonitorLease accepted a token bound to no session")
	}
}

func sessionHeartbeat(t *testing.T, s *Store, agentSessionID int64) string {
	t.Helper()
	var heartbeat *string
	if err := s.DB().QueryRowContext(context.Background(),
		`SELECT last_heartbeat_at FROM agent_sessions WHERE id = ?`, agentSessionID).Scan(&heartbeat); err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if heartbeat == nil {
		return ""
	}
	return *heartbeat
}
