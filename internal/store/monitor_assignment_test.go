package store

import (
	"context"
	"os"
	"reflect"
	"testing"
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
