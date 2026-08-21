package store

import (
	"context"
	"os"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestDetectWakeupReportsUnstartedTasksWithoutRunningClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Resume the active goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "wakeup-tasks", []string{"First task", "Second task"}, []string{"Complete the first task.", "Complete the second task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.ActiveGoalCount != 1 || state.UnstartedTaskCount != len(tasks) {
		t.Fatalf("wakeup state counts = %+v, want one active goal and %d tasks", state, len(tasks))
	}
	if len(state.Tasks) != len(tasks) || state.Tasks[0].ID != tasks[0].ID || state.Tasks[1].ID != tasks[1].ID {
		t.Fatalf("wakeup tasks = %#v, want declared tasks %#v", state.Tasks, tasks)
	}

	counted, err := s.CountUnstartedTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != len(tasks) {
		t.Fatalf("counted unstarted tasks = %d, want %d", counted, len(tasks))
	}
}

func TestDetectWakeupExcludesGoalWaitingForOpenDecision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Wait for human answer", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "wakeup-decision", []string{"Blocked task"}, []string{"Complete the task after the human answer."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID,
		Kind: domain.KindDecision, Question: "Which path should the agent take?",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.ActiveGoalCount != 0 || state.UnstartedTaskCount != 0 || len(state.Tasks) != 0 {
		t.Fatalf("wakeup state = %+v, want no actionable goal", state)
	}
	if state.WaitingAnswerCount != 1 {
		t.Fatalf("waiting answer count = %d, want 1", state.WaitingAnswerCount)
	}

	counted, err := s.CountUnstartedTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != 0 {
		t.Fatalf("counted unstarted tasks = %d, want 0 for a goal waiting on an answer", counted)
	}
}

func TestDetectWakeupExcludesProposedGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Await approval before wakeup", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, goal.ID, "agent", "wakeup-proposed", []string{"Proposed task"}, []string{"Wait for approval before wakeup."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.ActiveGoalCount != 0 || state.UnstartedTaskCount != 0 || len(state.Tasks) != 0 {
		t.Fatalf("wakeup state = %+v, want proposed goal excluded", state)
	}
}

func TestDetectWakeupExcludesGoalWithRunningClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Continue the running goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "wakeup-running", []string{"Running task", "Waiting task"}, []string{"Keep working on the running task.", "Continue with the waiting task later."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "wakeup-running-session", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "wakeup-running-session", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "wakeup-running-session"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.ActiveGoalCount != 0 || state.UnstartedTaskCount != 0 || len(state.Tasks) != 0 {
		t.Fatalf("wakeup state = %+v, want running goal excluded", state)
	}

	counted, err := s.CountUnstartedTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != 1 {
		t.Fatalf("counted unstarted tasks = %d, want 1", counted)
	}
}
