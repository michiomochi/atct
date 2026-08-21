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

func TestDetectWakeupDoesNotReportGoalWithTasksAsUndeclared(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	declaredGoal, err := s.CreateGoal(ctx, project.ID, "Goal with a declared task", "human")
	if err != nil {
		t.Fatalf("CreateGoal declared: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, declaredGoal.ID, "agent", "wakeup-declared", []string{"Declared task"}, []string{"Complete the declared task."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	emptyGoal, err := s.CreateGoal(ctx, project.ID, "Goal without declared tasks", "human")
	if err != nil {
		t.Fatalf("CreateGoal empty: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.UndeclaredGoals) != 1 || state.UndeclaredGoals[0].ID != emptyGoal.ID {
		t.Fatalf("undeclared goals = %#v, want only %s", state.UndeclaredGoals, emptyGoal.ID)
	}
}

func TestDetectWakeupDoesNotReportGoalWithLinkedTaskCommitAsCommitless(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	commitlessGoal, err := s.CreateGoal(ctx, project.ID, "Goal without a linked commit", "human")
	if err != nil {
		t.Fatalf("CreateGoal commitless: %v", err)
	}
	commitlessTasks, err := s.DeclareTasks(ctx, commitlessGoal.ID, "agent", "wakeup-commitless", []string{"Commitless task"}, []string{"Complete the commitless task."})
	if err != nil {
		t.Fatalf("DeclareTasks commitless: %v", err)
	}
	linkedGoal, err := s.CreateGoal(ctx, project.ID, "Goal with a linked commit", "human")
	if err != nil {
		t.Fatalf("CreateGoal linked: %v", err)
	}
	linkedTasks, err := s.DeclareTasks(ctx, linkedGoal.ID, "agent", "wakeup-linked", []string{"Linked task"}, []string{"Complete the linked task."})
	if err != nil {
		t.Fatalf("DeclareTasks linked: %v", err)
	}
	updateWakeupTask(t, s, commitlessTasks[0].ID, domain.TaskDone)
	updateWakeupTask(t, s, linkedTasks[0].ID, domain.TaskDone)
	if err := s.LinkTaskCommit(ctx, linkedTasks[0].ID, domain.TaskCommit{
		SHA:     "abc123",
		Subject: "Link the completed task to a commit",
	}); err != nil {
		t.Fatalf("LinkTaskCommit: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.CommitlessGoals) != 1 || state.CommitlessGoals[0].ID != commitlessGoal.ID {
		t.Fatalf("commitless goals = %#v, want only %s", state.CommitlessGoals, commitlessGoal.ID)
	}
}

func TestDetectWakeupDoesNotReportGoalWithOpenCompletionDecisionAsCommitless(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	waitingGoal, err := s.CreateGoal(ctx, project.ID, "Goal waiting for completion decision", "human")
	if err != nil {
		t.Fatalf("CreateGoal waiting: %v", err)
	}
	waitingTasks, err := s.DeclareTasks(ctx, waitingGoal.ID, "agent", "wakeup-completion-open", []string{"Waiting task"}, []string{"Complete the waiting task."})
	if err != nil {
		t.Fatalf("DeclareTasks waiting: %v", err)
	}
	readyGoal, err := s.CreateGoal(ctx, project.ID, "Goal without completion decision", "human")
	if err != nil {
		t.Fatalf("CreateGoal ready: %v", err)
	}
	readyTasks, err := s.DeclareTasks(ctx, readyGoal.ID, "agent", "wakeup-completion-ready", []string{"Ready task"}, []string{"Complete the ready task."})
	if err != nil {
		t.Fatalf("DeclareTasks ready: %v", err)
	}
	updateWakeupTask(t, s, waitingTasks[0].ID, domain.TaskDone)
	updateWakeupTask(t, s, readyTasks[0].ID, domain.TaskDone)
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: waitingGoal.ID, TaskID: waitingTasks[0].ID,
		Kind: domain.KindCompletion, Question: "Should completion be reported now?",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.CommitlessGoals) != 1 || state.CommitlessGoals[0].ID != readyGoal.ID {
		t.Fatalf("commitless goals = %#v, want only %s", state.CommitlessGoals, readyGoal.ID)
	}
}

func TestDetectWakeupDoesNotReportAllDroppedGoalAsCommitless(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	droppedGoal, err := s.CreateGoal(ctx, project.ID, "Goal with only dropped tasks", "human")
	if err != nil {
		t.Fatalf("CreateGoal dropped: %v", err)
	}
	droppedTasks, err := s.DeclareTasks(ctx, droppedGoal.ID, "agent", "wakeup-all-dropped", []string{"Dropped task"}, []string{"Withdraw the dropped task."})
	if err != nil {
		t.Fatalf("DeclareTasks dropped: %v", err)
	}
	mixedGoal, err := s.CreateGoal(ctx, project.ID, "Goal with done and dropped tasks", "human")
	if err != nil {
		t.Fatalf("CreateGoal mixed: %v", err)
	}
	mixedTasks, err := s.DeclareTasks(ctx, mixedGoal.ID, "agent", "wakeup-done-dropped", []string{"Done task", "Dropped task"}, []string{"Complete the done task.", "Withdraw the dropped task."})
	if err != nil {
		t.Fatalf("DeclareTasks mixed: %v", err)
	}
	updateWakeupTask(t, s, droppedTasks[0].ID, domain.TaskDropped)
	updateWakeupTask(t, s, mixedTasks[0].ID, domain.TaskDone)
	updateWakeupTask(t, s, mixedTasks[1].ID, domain.TaskDropped)

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.CommitlessGoals) != 1 || state.CommitlessGoals[0].ID != mixedGoal.ID {
		t.Fatalf("commitless goals = %#v, want only %s", state.CommitlessGoals, mixedGoal.ID)
	}
}

func TestDetectWakeupDoesNotReportProposedGoalAsUndeclaredOrCommitless(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	proposedEmptyGoal, err := s.CreateGoal(ctx, project.ID, "Proposed empty goal", "agent")
	if err != nil {
		t.Fatalf("CreateGoal proposed empty: %v", err)
	}
	proposedDoneGoal, err := s.CreateGoal(ctx, project.ID, "Proposed done goal", "agent")
	if err != nil {
		t.Fatalf("CreateGoal proposed done: %v", err)
	}
	proposedTasks, err := s.DeclareTasks(ctx, proposedDoneGoal.ID, "agent", "wakeup-proposed-done", []string{"Proposed done task"}, []string{"Complete the proposed done task."})
	if err != nil {
		t.Fatalf("DeclareTasks proposed: %v", err)
	}
	updateWakeupTask(t, s, proposedTasks[0].ID, domain.TaskDone)

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.UndeclaredGoals) != 0 {
		t.Fatalf("undeclared goals = %#v, want proposed goal %s excluded", state.UndeclaredGoals, proposedEmptyGoal.ID)
	}
	if len(state.CommitlessGoals) != 0 {
		t.Fatalf("commitless goals = %#v, want proposed goal %s excluded", state.CommitlessGoals, proposedDoneGoal.ID)
	}
}

func updateWakeupTask(t *testing.T, s *Store, taskID string, status domain.TaskStatus) {
	t.Helper()
	if _, err := s.UpdateTask(context.Background(), taskID, status, ""); err != nil {
		t.Fatalf("UpdateTask %s: %v", taskID, err)
	}
}
