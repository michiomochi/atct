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
