package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testMonitorHealth(t *testing.T, now time.Time) MonitorHealth {
	t.Helper()
	goalID := int64(11)
	taskID := int64(22)
	health := MonitorHealth{
		CWD:              "/tmp/monitor-project/worktree",
		Role:             "executor",
		State:            "healthy",
		Reason:           "reconciled",
		ProjectID:        7,
		GoalID:           &goalID,
		TaskID:           &taskID,
		PID:              1234,
		ProcessStartedAt: now.Add(-time.Minute),
		TransitionedAt:   now,
		LastSeenAt:       now,
	}
	health.MonitorID = MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
	return health
}

func TestMonitorHealthRejectsForgedIdentity(t *testing.T) {
	s := newTestStore(t)
	health := testMonitorHealth(t, time.Now().UTC())
	health.MonitorID = strings.Repeat("f", 64)
	if err := s.UpsertMonitorHealth(context.Background(), health); err == nil {
		t.Fatal("UpsertMonitorHealth accepted forged monitor identity")
	}
}

func TestMonitorHealthExpiresUngracefulProcess(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	health := testMonitorHealth(t, now.Add(-76*time.Second))
	if err := s.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListMonitorHealth(context.Background(), health.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expired health rows = %+v, want none", rows)
	}
}

func TestMonitorHealthHidesStoppedRow(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	health := testMonitorHealth(t, now)
	if err := s.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatal(err)
	}
	if err := s.StopMonitorHealth(context.Background(), health.MonitorID, now); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListMonitorHealth(context.Background(), health.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("stopped health rows = %+v, want none", rows)
	}
}

func TestMonitorHealthPrunesExpiredRows(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	health := testMonitorHealth(t, now.Add(-25*time.Hour))
	if err := s.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatal(err)
	}
	current := testMonitorHealth(t, now)
	current.PID = 1235
	current.MonitorID = MonitorHealthID(current.CWD, current.Role, current.ProjectID, current.GoalID, current.TaskID, current.PID, current.ProcessStartedAt)
	if err := s.UpsertMonitorHealth(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB().QueryRow("SELECT COUNT(*) FROM monitor_health").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("monitor health row count = %d, want 1 after prune", count)
	}
}
