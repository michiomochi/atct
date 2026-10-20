package store

import (
	"testing"
	"time"
)

func TestMonitorLastSeenAtScopesToGoalSubcommanders(t *testing.T) {
	g1, g2 := int64(1), int64(2)
	now := time.Now().UTC()
	history := []MonitorHealth{
		{Role: "subcommander", GoalID: &g1, LastSeenAt: now.Add(-20 * time.Minute)},
		{Role: "subcommander", GoalID: &g1, LastSeenAt: now.Add(-2 * time.Minute)},
		{Role: "executor", GoalID: &g1, LastSeenAt: now},
		{Role: "subcommander", GoalID: &g2, LastSeenAt: now},
	}
	if got, ok := MonitorLastSeenAt(history, g1); !ok || !got.Equal(now.Add(-2*time.Minute)) {
		t.Errorf("goal 1 = %v, %v; want the subcommander row 2 minutes ago", got, ok)
	}
	if got, ok := MonitorLastSeenAt(history, 0); !ok || !got.Equal(now) {
		t.Errorf("project = %v, %v; want the newest row", got, ok)
	}
	if _, ok := MonitorLastSeenAt(history, 3); ok {
		t.Error("goal without a subcommander row reported a heartbeat")
	}
	if _, ok := MonitorLastSeenAt(nil, 0); ok {
		t.Error("empty history reported a heartbeat")
	}
}
