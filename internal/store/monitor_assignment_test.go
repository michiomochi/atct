package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestMonitorAssignmentListsEveryReceivedOpenTaskHandoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "monitor-assignment", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	addLiveProjectClaim(t, s, goal.ID, "monitor-assignment-commander")
	registerNamedTestAgentSession(t, s, "monitor-assignment-subcommander", os.Getpid())
	registerNamedTestAgentSession(t, s, "monitor-assignment-executor", os.Getpid())
	subcommander := testSessionID("monitor-assignment-subcommander")
	if _, err := s.RequestGoalHandoff(ctx, "monitor-assignment-goal", goal.ID, testSessionID("monitor-assignment-commander"), "delegate"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, "monitor-assignment-goal", goal.ID, subcommander); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MonitorAssignment(ctx, subcommander); err != nil {
		t.Fatal(err)
	} else if want := (MonitorAssignment{Role: "subcommander", ProjectID: project.ID, GoalID: goal.ID}); !reflect.DeepEqual(got, want) {
		t.Fatalf("subcommander assignment = %+v, want %+v", got, want)
	}
	tasks, err := s.CreateTasks(ctx, goal.ID, "agent", "monitor-assignment", []string{"one", "two"}, []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	session := testSessionID("monitor-assignment-executor")
	for _, task := range tasks {
		if _, err := s.RequestTaskHandoff(ctx, "handoff-"+task.Title, task.ID, subcommander, "delegate"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReceiveTaskHandoff(ctx, "handoff-"+task.Title, task.ID, session); err != nil {
			t.Fatal(err)
		}
	}

	assignment, err := s.MonitorAssignment(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	want := MonitorAssignment{
		Role:      "executor",
		ProjectID: project.ID,
		Tasks: []MonitorScope{
			{ProjectID: project.ID, GoalID: goal.ID, TaskID: tasks[0].ID},
			{ProjectID: project.ID, GoalID: goal.ID, TaskID: tasks[1].ID},
		},
	}
	if !reflect.DeepEqual(assignment, want) {
		t.Fatalf("assignment = %+v, want %+v", assignment, want)
	}
}

func TestMonitorBindingUsesCanonicalSessionID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "monitor-binding", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstSession, err := s.RegisterAgentSession(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	canonicalID, _, err := s.IdentifyAgentSession(ctx, firstSession, "session-key-1")
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := s.RegisterAgentSession(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, reattached, err := s.IdentifyAgentSession(ctx, secondSession, "session-key-1"); err != nil {
		t.Fatal(err)
	} else if got != canonicalID || !reattached {
		t.Fatalf("IdentifyAgentSession = (%d, %t), want (%d, true)", got, reattached, canonicalID)
	}
	if _, err := s.ClaimProject(ctx, project.ID, canonicalID); err != nil {
		t.Fatal(err)
	}
	if err := s.BindMonitorToken(ctx, "token-1", canonicalID); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.MonitorBinding(ctx, "token-1")
	if err != nil {
		t.Fatal(err)
	}
	want := MonitorBinding{Assignment: MonitorAssignment{Role: "commander", ProjectID: project.ID}}
	if !reflect.DeepEqual(resolved, want) {
		t.Fatalf("binding = %+v, want %+v", resolved, want)
	}
}

func TestMonitorBindingExpiresWithItsSession(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	session, err := s.RegisterAgentSession(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindMonitorToken(ctx, "expired-token", session); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET registered_at = ? WHERE id = ?`, time.Now().UTC().Add(-agentSessionRetention-time.Second).Format(time.RFC3339Nano), session); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RegisterAgentSession(ctx, 0); err != nil {
		t.Fatalf("RegisterAgentSession cleanup: %v", err)
	}
	if _, err := s.MonitorBinding(ctx, "expired-token"); !errors.Is(err, ErrMonitorBindingNotFound) {
		t.Fatalf("MonitorBinding after session expiry = %v, want %v", err, ErrMonitorBindingNotFound)
	}
}

func TestMonitorBindingTracksAssignmentTransitions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "monitor-transitions", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	commander := registerNamedTestAgentSession(t, s, "monitor-transitions-commander", os.Getpid())
	subcommander := registerNamedTestAgentSession(t, s, "monitor-transitions-subcommander", os.Getpid())
	executor := registerNamedTestAgentSession(t, s, "monitor-transitions-executor", os.Getpid())
	if err := s.BindMonitorToken(ctx, "commander-token", commander); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimProject(ctx, project.ID, commander); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MonitorBinding(ctx, "commander-token"); err != nil {
		t.Fatal(err)
	} else if want := (MonitorBinding{Assignment: MonitorAssignment{Role: "commander", ProjectID: project.ID}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("binding after claim = %+v, want %+v", got, want)
	}
	if _, err := s.RequestGoalHandoff(ctx, "monitor-transitions-goal", goal.ID, commander, "delegate"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, "monitor-transitions-goal", goal.ID, subcommander); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.CreateTasks(ctx, goal.ID, "agent", "monitor-transitions", []string{"one"}, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestTaskHandoff(ctx, "monitor-transitions-task", tasks[0].ID, subcommander, "delegate"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindMonitorToken(ctx, "executor-token", executor); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MonitorBinding(ctx, "executor-token"); err != nil {
		t.Fatal(err)
	} else if want := (MonitorBinding{Assignment: MonitorAssignment{Role: "executor"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("binding before receive = %+v, want %+v", got, want)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, "monitor-transitions-task", tasks[0].ID, executor); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MonitorBinding(ctx, "executor-token"); err != nil {
		t.Fatal(err)
	} else if want := (MonitorBinding{Assignment: MonitorAssignment{Role: "executor", ProjectID: project.ID, Tasks: []MonitorScope{{ProjectID: project.ID, GoalID: goal.ID, TaskID: tasks[0].ID}}}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("binding after receive = %+v, want %+v", got, want)
	}
	if _, err := s.CompleteTaskHandoff(ctx, "monitor-transitions-task", tasks[0].ID, "done"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MonitorBinding(ctx, "executor-token"); err != nil {
		t.Fatal(err)
	} else if want := (MonitorBinding{Assignment: MonitorAssignment{Role: "executor"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("binding after completion = %+v, want %+v", got, want)
	}
}

func TestMonitorBindingWatermarkSurvivesTokenRebind(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	firstSession, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	const token = "watermark-rebind-token"
	const watermark = "2026-09-19T00:00:00Z"
	if err := s.BindMonitorToken(ctx, token, firstSession); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE monitor_bindings SET last_reconciled_at = ? WHERE token = ?`, watermark, token); err != nil {
		t.Fatal(err)
	}
	if err := s.BindMonitorToken(ctx, token, secondSession); err != nil {
		t.Fatal(err)
	}

	var gotSession int64
	var gotWatermark string
	if err := s.DB().QueryRowContext(ctx, `SELECT agent_session_id, last_reconciled_at FROM monitor_bindings WHERE token = ?`, token).Scan(&gotSession, &gotWatermark); err != nil {
		t.Fatal(err)
	}
	if gotSession != secondSession || gotWatermark != watermark {
		t.Fatalf("monitor binding = (%d, %q), want (%d, %q)", gotSession, gotWatermark, secondSession, watermark)
	}
}

func TestMonitorHealthAdvancesProjectCommanderWatermark(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "monitor-watermark", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	const token = "health-watermark-token"
	if err := s.BindMonitorToken(ctx, token, session); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	health := MonitorHealth{
		MonitorToken: token, CWD: project.RootPath, Role: "commander", State: "healthy", Reason: "reconciled",
		ProjectID: project.ID, PID: os.Getpid(), ProcessStartedAt: now.Add(-time.Minute),
		TransitionedAt: now, LastSeenAt: now, ScopeKey: "project:monitor",
	}
	health.MonitorID = MonitorHealthID(health.CWD, health.Role, health.ProjectID, nil, nil, health.PID, health.ProcessStartedAt, health.ScopeKey)
	if err := s.UpsertMonitorHealth(ctx, health); err != nil {
		t.Fatal(err)
	}

	var got string
	if err := s.DB().QueryRowContext(ctx, `SELECT last_reconciled_at FROM monitor_bindings WHERE token = ?`, token).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != now.Format(timestampLayout) {
		t.Fatalf("last_reconciled_at = %q, want %q", got, now.Format(timestampLayout))
	}
}

func TestMonitorHealthDoesNotAdvanceScopedOrUnhealthyWatermark(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "monitor-watermark-guard", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	const token = "health-watermark-guard-token"
	const initial = "2026-09-18T00:00:00Z"
	if err := s.BindMonitorToken(ctx, token, session); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE monitor_bindings SET last_reconciled_at = ? WHERE token = ?`, initial, token); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	goalID := int64(9)
	for _, health := range []MonitorHealth{
		{MonitorToken: token, CWD: project.RootPath, Role: "commander", State: "recovering", ProjectID: project.ID, PID: os.Getpid(), ProcessStartedAt: now.Add(-time.Minute), TransitionedAt: now, LastSeenAt: now},
		{MonitorToken: token, CWD: project.RootPath, Role: "subcommander", State: "healthy", ProjectID: project.ID, GoalID: &goalID, PID: os.Getpid(), ProcessStartedAt: now.Add(-time.Minute), TransitionedAt: now, LastSeenAt: now},
	} {
		health.MonitorID = MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
		if err := s.UpsertMonitorHealth(ctx, health); err != nil {
			t.Fatal(err)
		}
	}

	var got string
	if err := s.DB().QueryRowContext(ctx, `SELECT last_reconciled_at FROM monitor_bindings WHERE token = ?`, token).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != initial {
		t.Fatalf("last_reconciled_at = %q after guarded reports, want %q", got, initial)
	}
}

func TestMonitorHealthWatermarkOrdersByTimeAcrossFractionWidths(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "monitor-watermark-order", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	const token = "health-watermark-order-token"
	if err := s.BindMonitorToken(ctx, token, session); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 3, 1, 0, 5, 0, time.UTC)
	report := func(lastSeen time.Time) string {
		t.Helper()
		health := MonitorHealth{
			MonitorToken: token, CWD: project.RootPath, Role: "commander", State: "healthy", Reason: "reconciled",
			ProjectID: project.ID, PID: os.Getpid(), ProcessStartedAt: base.Add(-time.Minute),
			TransitionedAt: lastSeen, LastSeenAt: lastSeen,
		}
		health.MonitorID = MonitorHealthID(health.CWD, health.Role, health.ProjectID, nil, nil, health.PID, health.ProcessStartedAt)
		if err := s.UpsertMonitorHealth(ctx, health); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := s.DB().QueryRowContext(ctx, `SELECT last_reconciled_at FROM monitor_bindings WHERE token = ?`, token).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	at51 := base.Add(510 * time.Millisecond)
	if got := report(at51); got != at51.Format(timestampLayout) {
		t.Fatalf("watermark = %q, want %q", got, at51.Format(timestampLayout))
	}
	// .5 is earlier than .51 but sorts after it as a trimmed RFC3339Nano string.
	if got := report(base.Add(500 * time.Millisecond)); got != at51.Format(timestampLayout) {
		t.Fatalf("watermark moved backwards to %q, want %q", got, at51.Format(timestampLayout))
	}
	at60 := base.Add(600 * time.Millisecond)
	if got := report(at60); got != at60.Format(timestampLayout) {
		t.Fatalf("watermark after .6 = %q, want advance to %q", got, at60.Format(timestampLayout))
	}
}
