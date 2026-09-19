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
