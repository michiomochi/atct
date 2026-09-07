package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

func TestMonitorHealthGetRequiresProject(t *testing.T) {
	f := newBareFixture(t)
	status, headers, body := doHandlerRequest(t, httpapi.New(f.store).Handler(), http.MethodGet, "/api/monitor-health", nil)
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
}

func TestMonitorHealthGetFiltersOtherProject(t *testing.T) {
	f := newBareFixture(t)
	now := time.Now().UTC()
	goalID := f.goal.ID
	health := store.MonitorHealth{CWD: f.project.RootPath, Role: "subcommander", State: "healthy", ProjectID: f.project.ID, GoalID: &goalID, PID: 101, ProcessStartedAt: now, LastSeenAt: now, TransitionedAt: now}
	health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, nil, health.PID, health.ProcessStartedAt)
	if err := f.store.UpsertMonitorHealth(f.ctx, health); err != nil {
		t.Fatal(err)
	}
	status, _, body := doHandlerRequest(t, httpapi.New(f.store).Handler(), http.MethodGet, "/api/monitor-health?project_id="+strconv.FormatInt(f.project.ID+100, 10), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	var rows []store.MonitorHealth
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return
	}
	t.Fatalf("foreign project health response = %s", body)
}

func TestMonitorHealthGetFiltersGoalAndTaskSelector(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	goalID := f.goal.ID
	taskID := f.tasks[0].ID
	health := store.MonitorHealth{CWD: f.project.RootPath, Role: "executor", State: "healthy", ProjectID: f.project.ID, GoalID: &goalID, TaskID: &taskID, PID: 102, ProcessStartedAt: now, LastSeenAt: now, TransitionedAt: now}
	health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
	if err := f.store.UpsertMonitorHealth(f.ctx, health); err != nil {
		t.Fatal(err)
	}
	status, _, body := doHandlerRequest(t, httpapi.New(f.store).Handler(), http.MethodGet, "/api/monitor-health?project_id="+strconv.FormatInt(f.project.ID, 10)+"&goal_id="+strconv.FormatInt(goalID, 10)+"&task_id="+strconv.FormatInt(taskID, 10), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	if string(body) == "[]" || string(body) == "null" {
		t.Fatalf("matching health row missing: %s", body)
	}
}

func TestMonitorHealthGetOmitsExpiredAndStoppedRows(t *testing.T) {
	f := newBareFixture(t)
	now := time.Now().UTC()
	goalID := f.goal.ID
	for i, at := range []time.Time{now.Add(-76 * time.Second), now} {
		health := store.MonitorHealth{CWD: f.project.RootPath, Role: "subcommander", State: "healthy", ProjectID: f.project.ID, GoalID: &goalID, PID: 110 + i, ProcessStartedAt: at, LastSeenAt: at, TransitionedAt: at}
		health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, nil, health.PID, health.ProcessStartedAt)
		if err := f.store.UpsertMonitorHealth(f.ctx, health); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err := f.store.StopMonitorHealth(f.ctx, health.MonitorID, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	status, _, body := doHandlerRequest(t, httpapi.New(f.store).Handler(), http.MethodGet, "/api/monitor-health?project_id="+strconv.FormatInt(f.project.ID, 10), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	if string(body) != "[]\n" && string(body) != "[]" {
		t.Fatalf("expired/stopped health response = %s", body)
	}
}

func TestMonitorHealthPostRejectsCommanderAndMismatchedScope(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	goalID := f.goal.ID
	taskID := f.tasks[0].ID
	base := store.MonitorHealth{CWD: f.project.RootPath, Role: "commander", State: "healthy", ProjectID: f.project.ID, GoalID: &goalID, PID: 120, ProcessStartedAt: now, LastSeenAt: now, TransitionedAt: now}
	base.MonitorID = store.MonitorHealthID(base.CWD, base.Role, base.ProjectID, base.GoalID, nil, base.PID, base.ProcessStartedAt)
	for _, health := range []store.MonitorHealth{base, func() store.MonitorHealth {
		m := base
		m.Role = "executor"
		m.TaskID = &taskID
		m.MonitorID = store.MonitorHealthID(m.CWD, m.Role, m.ProjectID, m.GoalID, m.TaskID, m.PID, m.ProcessStartedAt)
		m.GoalID = func() *int64 { v := goalID + 1; return &v }()
		return m
	}()} {
		status, _, _ := doHandlerRequest(t, httpapi.New(f.store).Handler(), http.MethodPost, "/api/monitor-health", mustJSON(t, health))
		if status != http.StatusBadRequest {
			t.Fatalf("health POST status = %d, want 400", status)
		}
	}
}

func TestMonitorHealthPostAcceptsProjectCommanderScope(t *testing.T) {
	f := newBareFixture(t)
	commanderID := registerTestSession(t, f.store, "monitor-health-commander", 0)
	if _, err := f.store.ClaimProject(f.ctx, f.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	now := time.Now().UTC()
	health := store.MonitorHealth{
		CWD: f.project.RootPath, Role: "commander", State: "healthy", ProjectID: f.project.ID,
		ScopeKey: store.ProjectOrchestrationScopeKey(f.project.ID), AgentSessionID: commanderID,
		PID: 121, ProcessStartedAt: now, LastSeenAt: now, TransitionedAt: now,
	}
	health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, nil, nil, health.PID, health.ProcessStartedAt, health.ScopeKey)
	status, _, body := doHandlerRequest(t, httpapi.New(f.store).Handler(), http.MethodPost, "/api/monitor-health", mustJSON(t, health))
	if status != http.StatusOK {
		t.Fatalf("commander health POST status = %d; body=%s", status, body)
	}
}
