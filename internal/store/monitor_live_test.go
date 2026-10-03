package store

import (
	"context"
	"testing"
	"time"
)

func TestHasLiveMonitorForScopeSeesFreshHeartbeat(t *testing.T) {
	s := newTestStore(t)
	health := testMonitorHealth(t, time.Now().UTC())
	if err := s.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatal(err)
	}
	live, err := s.HasLiveMonitorForScope(context.Background(), scopeOf(health))
	if err != nil {
		t.Fatal(err)
	}
	if !live {
		t.Fatal("HasLiveMonitorForScope = false, want true for a fresh heartbeat")
	}
}

func TestHasLiveMonitorForScopeIgnoresExpiredHeartbeat(t *testing.T) {
	s := newTestStore(t)
	health := testMonitorHealth(t, time.Now().UTC().Add(-MonitorHealthLease-time.Second))
	if err := s.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatal(err)
	}
	live, err := s.HasLiveMonitorForScope(context.Background(), scopeOf(health))
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("HasLiveMonitorForScope = true, want false once the lease expired")
	}
}

func TestHasLiveMonitorForScopeIgnoresStoppedMonitor(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	health := testMonitorHealth(t, now)
	if err := s.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatal(err)
	}
	if err := s.StopMonitorHealth(context.Background(), health.MonitorID, now); err != nil {
		t.Fatal(err)
	}
	live, err := s.HasLiveMonitorForScope(context.Background(), scopeOf(health))
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("HasLiveMonitorForScope = true, want false for a stopped monitor")
	}
}

func scopeOf(health MonitorHealth) MonitorLiveScope {
	return MonitorLiveScope{ProjectID: health.ProjectID, Role: health.Role, GoalID: health.GoalID}
}

func TestHasLiveMonitorForScopeIgnoresOtherGoal(t *testing.T) {
	s := newTestStore(t)
	health := testMonitorHealth(t, time.Now().UTC())
	if err := s.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatal(err)
	}
	other := int64(999)
	scope := scopeOf(health)
	scope.GoalID = &other
	live, err := s.HasLiveMonitorForScope(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("HasLiveMonitorForScope = true, want false for a different goal")
	}
}

func TestRearmingMonitorKeepsScopeAndSessionLiveUntilGraceEnds(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	session, err := s.RegisterAgentSession(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindMonitorToken(ctx, "rearm-token", session); err != nil {
		t.Fatal(err)
	}
	health := testMonitorHealth(t, time.Now().UTC())
	health.MonitorToken = "rearm-token"
	health.State = "rearming"
	if err := s.UpsertMonitorHealth(ctx, health); err != nil {
		t.Fatal(err)
	}
	live, err := s.HasLiveMonitorForScope(ctx, scopeOf(health))
	if err != nil || !live {
		t.Fatalf("HasLiveMonitorForScope = %v, %v; want true while re-arming", live, err)
	}
	if !s.AgentSessionLive(ctx, session, time.Now()) {
		t.Fatal("AgentSessionLive = false right after rearming")
	}
	// Past the monitor-health lease but inside the grace: still live.
	later := time.Now().Add(MonitorHealthLease + time.Minute)
	if !s.AgentSessionLive(ctx, session, later) {
		t.Fatal("session lease lapsed inside the grace")
	}
	if s.AgentSessionLive(ctx, session, time.Now().Add(MonitorRearmGrace+time.Minute)) {
		t.Fatal("session lease outlived the grace")
	}

	original := MonitorRearmGrace
	MonitorRearmGrace = -time.Second
	defer func() { MonitorRearmGrace = original }()
	if err := s.UpsertMonitorHealth(ctx, health); err != nil {
		t.Fatal(err)
	}
	if live, err := s.HasLiveMonitorForScope(ctx, scopeOf(health)); err != nil || live {
		t.Fatalf("HasLiveMonitorForScope = %v, %v; want false once the grace is negative", live, err)
	}
}
