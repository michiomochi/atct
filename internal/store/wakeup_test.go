package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func TestSnoozeTaskUsesDeadlineToControlWakeupWithoutChangingTodo(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Defer tasks", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "snooze-tasks", []string{
		"Future task",
		"Expired task",
		"Empty deadline task",
		"Cleared deadline task",
	}, []string{
		"Stay deferred until the future deadline.",
		"Return to wakeup after the deadline.",
		"Remain actionable without a deadline.",
		"Return to wakeup after clearing the deadline.",
	})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	future := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)

	deferred, err := s.SnoozeTask(ctx, tasks[0].ID, &future)
	if err != nil {
		t.Fatalf("SnoozeTask future: %v", err)
	}
	if deferred.Status != domain.TaskTodo || deferred.SnoozedUntil == nil || !deferred.SnoozedUntil.Equal(future) {
		t.Fatalf("future snooze result = %+v, want todo with deadline %s", deferred, future)
	}

	expired, err := s.SnoozeTask(ctx, tasks[1].ID, &past)
	if err != nil {
		t.Fatalf("SnoozeTask past: %v", err)
	}
	if expired.Status != domain.TaskTodo {
		t.Fatalf("expired snooze changed status to %q, want todo", expired.Status)
	}

	if _, err := s.SnoozeTask(ctx, tasks[3].ID, &future); err != nil {
		t.Fatalf("SnoozeTask before clear: %v", err)
	}
	cleared, err := s.SnoozeTask(ctx, tasks[3].ID, nil)
	if err != nil {
		t.Fatalf("SnoozeTask clear: %v", err)
	}
	if cleared.Status != domain.TaskTodo || cleared.SnoozedUntil != nil {
		t.Fatalf("cleared snooze result = %+v, want todo without deadline", cleared)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.UnstartedTaskCount != 3 || len(state.Tasks) != 3 {
		t.Fatalf("wakeup state = %+v, want three actionable todo tasks", state)
	}
	got := make(map[int64]bool, len(state.Tasks))
	for _, task := range state.Tasks {
		got[task.ID] = true
	}
	for _, task := range tasks[1:] {
		if !got[task.ID] {
			t.Fatalf("wakeup tasks = %#v, missing actionable task %d", state.Tasks, task.ID)
		}
	}
	if got[tasks[0].ID] {
		t.Fatalf("future-snoozed task %d was included in wakeup tasks", tasks[0].ID)
	}

	counted, err := s.CountUnstartedTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != 3 {
		t.Fatalf("counted unstarted tasks = %d, want 3", counted)
	}

	listed, err := s.ListTasks(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, task := range listed {
		if task.Status != domain.TaskTodo {
			t.Fatalf("task %d status = %q, want todo", task.ID, task.Status)
		}
	}
}

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
	if state.ActionableGoalCount != 1 || state.UnstartedTaskCount != len(tasks) {
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

func TestDetectWakeupClassifiesUnstartedTasksForGoalWaitingForOpenDecision(t *testing.T) {
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
	if state.ActionableGoalCount != 1 || state.UnstartedTaskCount != 1 || len(state.Tasks) != 0 {
		t.Fatalf("wakeup state = %+v, want one counted but non-actionable task", state)
	}
	if state.WaitingAnswerCount != 1 {
		t.Fatalf("waiting answer count = %d, want 1", state.WaitingAnswerCount)
	}
	if state.WaitingAnswerTaskCount != 1 || state.UntouchedTaskCount != 0 {
		t.Fatalf("wakeup task breakdown = %+v, want one waiting-answer task", state)
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
	goal, err := s.CreateGoal(ctx, project.ID, "Await approval before wakeup", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, goal.ID, "agent", "wakeup-proposed", []string{"Proposed task"}, []string{"Wait for approval before wakeup."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	// This state can only exist in databases created before the declaration gate.
	if _, err := s.db.ExecContext(ctx, "UPDATE goals SET status = ? WHERE id = ?", string(domain.GoalProposed), goal.ID); err != nil {
		t.Fatalf("set goal proposed: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.ActionableGoalCount != 0 || state.UnstartedTaskCount != 0 || len(state.Tasks) != 0 {
		t.Fatalf("wakeup state = %+v, want proposed goal excluded", state)
	}
}

func TestDetectWakeupClassifiesUnstartedTasksForGoalWithRunningClaim(t *testing.T) {
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
	runningSessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, runningSessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, runningSessionID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.ActionableGoalCount != 1 || state.UnstartedTaskCount != 1 || len(state.Tasks) != 1 || state.Tasks[0].ID != tasks[1].ID {
		t.Fatalf("wakeup state = %+v, want one counted and actionable sibling task", state)
	}
	if state.WaitingAnswerTaskCount != 0 || state.UntouchedTaskCount != 1 {
		t.Fatalf("wakeup task breakdown = %+v, want one untouched task", state)
	}
	if state.UnstartedTaskCount != state.WaitingAnswerTaskCount+state.UntouchedTaskCount {
		t.Fatalf("unstarted task breakdown = %+v, want total to equal category sum", state)
	}

	counted, err := s.CountUnstartedTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != 1 {
		t.Fatalf("counted unstarted tasks = %d, want 1", counted)
	}

	wakeupCount, err := s.CountUnstartedTasksForWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasksForWakeup: %v", err)
	}
	if wakeupCount != 1 {
		t.Fatalf("wakeup-rule unstarted tasks = %d, want 1 for a goal with a running claim", wakeupCount)
	}
}

func TestDetectWakeupClassifiesUnstartedTasksByOwnOpenDecision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Continue independent tasks", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "wakeup-task-level", []string{"Claimed sibling", "Waiting task", "Available sibling"}, []string{"Continue the claimed sibling.", "Continue after its decision is answered.", "Claim the available sibling."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	taskLevelSessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, taskLevelSessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, taskLevelSessionID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goal.ID, TaskID: tasks[1].ID,
		Kind: domain.KindDecision, Question: "Which path should the waiting task take?",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.UnstartedTaskCount != 2 {
		t.Fatalf("unstarted task count = %d, want 2", state.UnstartedTaskCount)
	}
	if state.WaitingAnswerTaskCount != 1 || state.UntouchedTaskCount != 1 {
		t.Fatalf("wakeup task breakdown = %+v, want one waiting and one untouched task", state)
	}
	if state.UnstartedTaskCount != state.WaitingAnswerTaskCount+state.UntouchedTaskCount {
		t.Fatalf("unstarted task breakdown = %+v, want total to equal category sum", state)
	}
	if len(state.Tasks) != state.UntouchedTaskCount || len(state.Tasks) != 1 || state.Tasks[0].ID != tasks[2].ID {
		t.Fatalf("actionable tasks = %#v, want only available sibling %d", state.Tasks, tasks[2].ID)
	}
}

func TestDetectWakeupCountsAndClassifiesAllUnstartedTasks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	declareGoal := func(title, source string, taskTitles ...string) (domain.Goal, []domain.Task) {
		t.Helper()
		if len(taskTitles) == 0 {
			taskTitles = []string{"Running task", "Unstarted task"}
		}
		goal, err := s.CreateGoal(ctx, project.ID, title, "human")
		if err != nil {
			t.Fatalf("CreateGoal %q: %v", title, err)
		}
		taskDescriptions := make([]string, len(taskTitles))
		for i := range taskDescriptions {
			taskDescriptions[i] = "Complete the task."
		}
		tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", source, taskTitles, taskDescriptions)
		if err != nil {
			t.Fatalf("DeclareTasks %q: %v", title, err)
		}
		return goal, tasks
	}
	runningSessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, runningSessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	claim := func(taskID int64) {
		t.Helper()
		if _, err := s.ClaimTask(ctx, taskID, runningSessionID); err != nil {
			t.Fatalf("ClaimTask %d: %v", taskID, err)
		}
	}
	ask := func(goalID, taskID int64, question string) {
		t.Helper()
		if _, err := s.AskDecision(ctx, AskInput{
			GoalID: goalID, TaskID: taskID,
			Kind: domain.KindDecision, Question: question,
		}); err != nil {
			t.Fatalf("AskDecision %d: %v", goalID, err)
		}
	}

	answerOnlyGoal, answerOnlyTasks := declareGoal("Answer-only goal", "wakeup-breakdown-answer-only", "Unstarted task")
	ask(answerOnlyGoal.ID, answerOnlyTasks[0].ID, "Choose the answer-only path.")
	overlapGoal, overlapTasks := declareGoal("Answer and working goal", "wakeup-breakdown-overlap")
	claim(overlapTasks[0].ID)
	ask(overlapGoal.ID, overlapTasks[0].ID, "Choose the overlapping path.")
	_, workingTasks := declareGoal("Working-only goal", "wakeup-breakdown-working-only")
	claim(workingTasks[0].ID)
	_, workingSecondTasks := declareGoal("Second working-only goal", "wakeup-breakdown-working-second")
	claim(workingSecondTasks[0].ID)
	_, untouchedTasks := declareGoal("Untouched goal", "wakeup-breakdown-untouched", "Unstarted task")
	_, secondUntouchedTasks := declareGoal("Second untouched goal", "wakeup-breakdown-untouched-second", "Unstarted task")

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.ActionableGoalCount != 6 {
		t.Fatalf("actionable goal count = %d, want 6", state.ActionableGoalCount)
	}
	if state.UnstartedTaskCount != 6 {
		t.Fatalf("unstarted task count = %d, want 6", state.UnstartedTaskCount)
	}
	if state.WaitingAnswerTaskCount != 1 {
		t.Fatalf("waiting-answer task count = %d, want 1", state.WaitingAnswerTaskCount)
	}
	if state.UntouchedTaskCount != 5 {
		t.Fatalf("untouched task count = %d, want 5", state.UntouchedTaskCount)
	}
	if state.UnstartedTaskCount != state.WaitingAnswerTaskCount+state.UntouchedTaskCount {
		t.Fatalf("unstarted task breakdown = %+v, want total to equal category sum", state)
	}
	wantTaskIDs := map[int64]struct{}{
		overlapTasks[1].ID:         {},
		workingTasks[1].ID:         {},
		workingSecondTasks[1].ID:   {},
		untouchedTasks[0].ID:       {},
		secondUntouchedTasks[0].ID: {},
	}
	if len(state.Tasks) != len(wantTaskIDs) || len(state.Tasks) != state.UntouchedTaskCount {
		t.Fatalf("actionable tasks = %#v, want %d untouched tasks", state.Tasks, len(wantTaskIDs))
	}
	for _, task := range state.Tasks {
		if _, ok := wantTaskIDs[task.ID]; !ok {
			t.Fatalf("unexpected actionable task = %#v", task)
		}
		delete(wantTaskIDs, task.ID)
	}
	if len(wantTaskIDs) != 0 {
		t.Fatalf("missing actionable tasks = %#v", wantTaskIDs)
	}

	wakeupCount, err := s.CountUnstartedTasksForWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasksForWakeup: %v", err)
	}
	if wakeupCount != state.UnstartedTaskCount {
		t.Fatalf("wakeup count = %d, DetectWakeup total = %d", wakeupCount, state.UnstartedTaskCount)
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
		t.Fatalf("undeclared goals = %#v, want only %d", state.UndeclaredGoals, emptyGoal.ID)
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
		t.Fatalf("commitless goals = %#v, want only %d", state.CommitlessGoals, commitlessGoal.ID)
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
		t.Fatalf("commitless goals = %#v, want only %d", state.CommitlessGoals, readyGoal.ID)
	}
}

func TestDetectWakeupCountsActionableGoalsWithoutCompletionApproval(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	waitingGoal, err := s.CreateGoal(ctx, project.ID, "Goal awaiting completion approval", "human")
	if err != nil {
		t.Fatalf("CreateGoal waiting: %v", err)
	}
	waitingTasks, err := s.DeclareTasks(ctx, waitingGoal.ID, "agent", "wakeup-actionable-completion", []string{"Completed task"}, []string{"Complete the task."})
	if err != nil {
		t.Fatalf("DeclareTasks waiting: %v", err)
	}
	updateWakeupTask(t, s, waitingTasks[0].ID, domain.TaskDone)
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: waitingGoal.ID, TaskID: waitingTasks[0].ID,
		Kind: domain.KindCompletion, Question: "Approve this goal as complete?",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	actionableGoal, err := s.CreateGoal(ctx, project.ID, "Goal with actionable task", "human")
	if err != nil {
		t.Fatalf("CreateGoal actionable: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, actionableGoal.ID, "agent", "wakeup-actionable-task", []string{"Todo task"}, []string{"Complete the task."}); err != nil {
		t.Fatalf("DeclareTasks actionable: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.ActionableGoalCount != 1 {
		t.Fatalf("actionable goal count = %d, want 1", state.ActionableGoalCount)
	}
}

func TestWakeupEventMarshalsWakeupCounts(t *testing.T) {
	event := WakeupEvent{WakeupID: "wakeup-json", ProjectID: 1, ActionableGoalCount: 3, DelegatedTaskCount: 2}
	got, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	const want = `{"wakeup_id":"wakeup-json","project_id":1,"actionable_goal_count":3,"unstarted_task_count":0,"waiting_answer_task_count":0,"untouched_task_count":0,"delegated_task_count":2,"waiting_answer_count":0}`
	if string(got) != want {
		t.Fatalf("wakeup JSON = %s, want %s", got, want)
	}
}

func TestDetectionEventJSONIncludesCompletionReport(t *testing.T) {
	event := DetectionEvent{
		DetectionID:    "detection-report",
		ProjectID:      1,
		TaskID:         2,
		HandoffID:      "handoff",
		CompleteReport: "task report",
	}
	got, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	const want = `{"detection_id":"detection-report","project_id":1,"task_id":2,"handoff_id":"handoff","complete_report":"task report"}`
	if string(got) != want {
		t.Fatalf("detection JSON = %s, want %s", got, want)
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
		t.Fatalf("commitless goals = %#v, want only %d", state.CommitlessGoals, mixedGoal.ID)
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
	proposedDoneGoal, err := s.CreateGoal(ctx, project.ID, "Proposed done goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal proposed done: %v", err)
	}
	proposedTasks, err := s.DeclareTasks(ctx, proposedDoneGoal.ID, "agent", "wakeup-proposed-done", []string{"Proposed done task"}, []string{"Complete the proposed done task."})
	if err != nil {
		t.Fatalf("DeclareTasks proposed: %v", err)
	}
	// This state can only exist in databases created before the declaration gate.
	if _, err := s.db.ExecContext(ctx, "UPDATE goals SET status = ? WHERE id = ?", string(domain.GoalProposed), proposedDoneGoal.ID); err != nil {
		t.Fatalf("set goal proposed: %v", err)
	}
	updateWakeupTask(t, s, proposedTasks[0].ID, domain.TaskDone)

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.UndeclaredGoals) != 0 {
		t.Fatalf("undeclared goals = %#v, want proposed goal %d excluded", state.UndeclaredGoals, proposedEmptyGoal.ID)
	}
	if len(state.CommitlessGoals) != 0 {
		t.Fatalf("commitless goals = %#v, want proposed goal %d excluded", state.CommitlessGoals, proposedDoneGoal.ID)
	}
}

func TestDetectWakeupCollectsStalledHandoffCandidates(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	taskIDs := addTestTasks(t, s, 8)
	const claimSessionID = "stalled-handoff-claim"
	addLiveTaskClaim(t, s, taskIDs[3], claimSessionID)
	addLiveParentGoalClaim(t, s, taskIDs[0], "stalled-handoff-requester")
	stalledHandoffReceiverID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession receiver: %v", err)
	}

	requesterID := testSessionID("stalled-handoff-requester")
	unreceived, err := s.RequestTaskHandoff(ctx, "handoff-unreceived", taskIDs[0], requesterID, "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff unreceived: %v", err)
	}
	unreported, err := s.RequestTaskHandoff(ctx, "handoff-unreported", taskIDs[1], requesterID, "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff unreported: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, unreported.ID, taskIDs[1], stalledHandoffReceiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff unreported: %v", err)
	}
	secondUnreported, err := s.RequestTaskHandoff(ctx, "handoff-unreported-second", taskIDs[5], requesterID, "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff second unreported: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, secondUnreported.ID, taskIDs[5], stalledHandoffReceiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff second unreported: %v", err)
	}
	completed, err := s.RequestTaskHandoff(ctx, "handoff-completed", taskIDs[2], requesterID, "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff completed: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, completed.ID, taskIDs[2], stalledHandoffReceiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff completed: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, completed.ID, taskIDs[2], "completed for the first stalled-handoff fixture"); err != nil {
		t.Fatalf("CompleteTaskHandoff completed: %v", err)
	}
	secondCompleted, err := s.RequestTaskHandoff(ctx, "handoff-completed-second", taskIDs[7], requesterID, "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff second completed: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, secondCompleted.ID, taskIDs[7], stalledHandoffReceiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff second completed: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, secondCompleted.ID, taskIDs[7], "completed for the second stalled-handoff fixture"); err != nil {
		t.Fatalf("CompleteTaskHandoff second completed: %v", err)
	}
	requestedClaim, err := s.RequestTaskHandoff(ctx, "handoff-requested-claim", taskIDs[4], requesterID, "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff requested claim: %v", err)
	}
	secondUnreceived, err := s.RequestTaskHandoff(ctx, "handoff-unreceived-second", taskIDs[6], requesterID, "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff second unreceived: %v", err)
	}

	state, err := s.DetectWakeup(ctx, taskIDsProjectID(t, s, taskIDs[0]))
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsAwaitingReceipt) != 3 {
		t.Fatalf("handoffs awaiting receipt = %#v, want %s, %s, and %s", state.HandoffsAwaitingReceipt, unreceived.ID, requestedClaim.ID, secondUnreceived.ID)
	}
	if len(state.HandoffsAwaitingReport) != 2 {
		t.Fatalf("handoffs awaiting report = %#v, want %s and %s", state.HandoffsAwaitingReport, unreported.ID, secondUnreported.ID)
	}
	if state.DelegatedTaskCount != 2 {
		t.Fatalf("delegated task count = %d, want 2 open received handoffs", state.DelegatedTaskCount)
	}
	if len(state.UndelegatedClaims) != 1 || state.UndelegatedClaims[0].ID != taskIDs[3] {
		t.Fatalf("undelegated claims = %#v, want only %d", state.UndelegatedClaims, taskIDs[3])
	}
	for _, handoff := range state.HandoffsAwaitingReceipt {
		if handoff.ID == completed.ID || handoff.ID == secondCompleted.ID {
			t.Fatalf("completed handoff was reported as awaiting receipt: %#v", handoff)
		}
	}
	for _, handoff := range state.HandoffsAwaitingReport {
		if handoff.ID == completed.ID || handoff.ID == secondCompleted.ID {
			t.Fatalf("completed handoff was reported as awaiting report: %#v", handoff)
		}
	}
}

func TestDetectWakeupCollectsReportedTaskAndGoalHandoffs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Reported handoffs", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	goalForGoalHandoff, err := s.CreateGoal(ctx, project.ID, "Reported goal handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal goal handoff: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "reported-handoffs", []string{"Reported task"}, []string{"Complete the reported task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	const requesterLabel = "reported-handoff-requester"
	const receiverLabel = "reported-handoff-receiver"
	requesterID := testSessionID(requesterLabel)
	receiverID := testSessionID(receiverLabel)
	addLiveParentGoalClaim(t, s, tasks[0].ID, requesterLabel)
	addLiveProjectClaim(t, s, goalForGoalHandoff.ID, requesterLabel)
	addTestAgentSession(t, s, receiverLabel)

	taskHandoff, err := s.RequestTaskHandoff(ctx, "reported-task-handoff", tasks[0].ID, requesterID, "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, taskHandoff.ID, tasks[0].ID, receiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, taskHandoff.ID, tasks[0].ID, "task report"); err != nil {
		t.Fatalf("CompleteTaskHandoff: %v", err)
	}

	goalHandoff, err := s.RequestGoalHandoff(ctx, "reported-goal-handoff", goalForGoalHandoff.ID, requesterID, "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, goalHandoff.ID, goalForGoalHandoff.ID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.CompleteGoalHandoff(ctx, goalHandoff.ID, goalForGoalHandoff.ID, "goal report"); err != nil {
		t.Fatalf("CompleteGoalHandoff: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsReported) != 2 {
		t.Fatalf("reported handoffs = %#v, want task and goal reports", state.HandoffsReported)
	}
	want := map[string]ReportedHandoff{
		taskHandoff.ID: {ID: taskHandoff.ID, GoalID: goal.ID, TaskID: tasks[0].ID, CompleteReport: "task report"},
		goalHandoff.ID: {ID: goalHandoff.ID, GoalID: goalForGoalHandoff.ID, CompleteReport: "goal report"},
	}
	for _, reported := range state.HandoffsReported {
		expected, ok := want[reported.ID]
		if !ok {
			t.Fatalf("unexpected reported handoff = %+v", reported)
		}
		if reported.ID != expected.ID || reported.TaskID != expected.TaskID || reported.GoalID != expected.GoalID || reported.CompleteReport != expected.CompleteReport {
			t.Fatalf("reported handoff = %+v, want %+v", reported, expected)
		}
		delete(want, reported.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing reported handoffs = %#v", want)
	}
}

func TestHandoffIsDelegation(t *testing.T) {
	tests := []struct {
		name                    string
		requestedBy, receivedBy int64
		want                    bool
	}{
		{name: "self claim", requestedBy: 101, receivedBy: 101, want: false},
		{name: "delegation", requestedBy: 101, receivedBy: 102, want: true},
		{name: "unknown requester", requestedBy: 0, receivedBy: 102, want: true},
		{name: "unknown receiver", requestedBy: 101, receivedBy: 0, want: true},
		{name: "unknown requester and receiver", requestedBy: 0, receivedBy: 0, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := handoffIsDelegation(tt.requestedBy, tt.receivedBy); got != tt.want {
				t.Fatalf("handoffIsDelegation(%d, %d) = %t, want %t", tt.requestedBy, tt.receivedBy, got, tt.want)
			}
		})
	}
}

func addCompletedWakeupTaskHandoff(t *testing.T, s *Store, handoffID string, taskID, requestedBy, receivedBy int64) {
	t.Helper()
	addWakeupHandoffSession(t, s, requestedBy)
	addWakeupHandoffSession(t, s, receivedBy)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(context.Background(), `
		INSERT INTO task_handoffs (
			id, task_id, requested_by, requested_at, received_by, received_at,
			completed_report_at, complete_report
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, handoffID, taskID, nullWakeupHandoffSessionID(requestedBy), now, nullWakeupHandoffSessionID(receivedBy), now, now, "completed task handoff"); err != nil {
		t.Fatalf("insert completed task handoff %q: %v", handoffID, err)
	}
}

func addCompletedWakeupGoalHandoff(t *testing.T, s *Store, handoffID string, goalID, requestedBy, receivedBy int64) {
	t.Helper()
	addWakeupHandoffSession(t, s, requestedBy)
	addWakeupHandoffSession(t, s, receivedBy)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(context.Background(), `
		INSERT INTO goal_handoffs (
			id, goal_id, requested_by, requested_at, received_by, received_at,
			completed_report_at, complete_report
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, handoffID, goalID, nullWakeupHandoffSessionID(requestedBy), now, nullWakeupHandoffSessionID(receivedBy), now, now, "completed goal handoff"); err != nil {
		t.Fatalf("insert completed goal handoff %q: %v", handoffID, err)
	}
}

func addWakeupHandoffSession(t *testing.T, s *Store, sessionID int64) {
	t.Helper()
	if sessionID == 0 {
		return
	}
	if _, err := s.DB().ExecContext(context.Background(), `
		INSERT OR IGNORE INTO agent_sessions (id, registered_at)
		VALUES (?, ?)
	`, sessionID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert handoff session %d: %v", sessionID, err)
	}
}

func nullWakeupHandoffSessionID(sessionID int64) any {
	if sessionID == 0 {
		return nil
	}
	return sessionID
}

func TestDetectWakeupDoesNotReportSelfClaimTaskHandoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Self claim task handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "self-claim-task", []string{"Task"}, []string{"Task description"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	addCompletedWakeupTaskHandoff(t, s, "self-claim-task-handoff", tasks[0].ID, 101, 101)

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsReported) != 0 {
		t.Fatalf("reported handoffs = %#v, want self claim excluded", state.HandoffsReported)
	}
}

func TestDetectWakeupDoesNotReportSelfClaimGoalHandoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Self claim goal handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	addCompletedWakeupGoalHandoff(t, s, "self-claim-goal-handoff", goal.ID, 101, 101)

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsReported) != 0 {
		t.Fatalf("reported handoffs = %#v, want self claim excluded", state.HandoffsReported)
	}
}

func TestDetectWakeupReportsDelegatedTaskAndGoalHandoffs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	taskGoal, err := s.CreateGoal(ctx, project.ID, "Delegated task handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal task: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, taskGoal.ID, "agent", "delegated-task", []string{"Task"}, []string{"Task description"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	goalHandoffGoal, err := s.CreateGoal(ctx, project.ID, "Delegated goal handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal goal: %v", err)
	}
	addCompletedWakeupTaskHandoff(t, s, "delegated-task-handoff", tasks[0].ID, 101, 102)
	addCompletedWakeupGoalHandoff(t, s, "delegated-goal-handoff", goalHandoffGoal.ID, 101, 102)

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsReported) != 2 {
		t.Fatalf("reported handoffs = %#v, want delegated task and goal handoffs", state.HandoffsReported)
	}
}

func TestDetectWakeupReportsTaskAndGoalHandoffsWithUnknownIdentity(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	taskGoal, err := s.CreateGoal(ctx, project.ID, "Unknown task handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal task: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, taskGoal.ID, "agent", "unknown-task", []string{"Task"}, []string{"Task description"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	goalHandoffGoal, err := s.CreateGoal(ctx, project.ID, "Unknown goal handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal goal: %v", err)
	}
	addCompletedWakeupTaskHandoff(t, s, "unknown-task-handoff", tasks[0].ID, 0, 102)
	addCompletedWakeupGoalHandoff(t, s, "unknown-goal-handoff", goalHandoffGoal.ID, 101, 0)

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsReported) != 2 {
		t.Fatalf("reported handoffs = %#v, want handoffs with unknown identities retained", state.HandoffsReported)
	}
}

func TestDetectWakeupReportsOnlyDelegationsInCompletedHandoffBreakdown(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Completed handoff breakdown", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	const selfBefore = 38
	const delegationBefore = 36
	titles := make([]string, selfBefore+delegationBefore)
	descriptions := make([]string, len(titles))
	for i := range titles {
		titles[i] = fmt.Sprintf("Task %d", i)
		descriptions[i] = "Task description"
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "completed-handoff-breakdown", titles, descriptions)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	selfIDs := make(map[string]struct{}, selfBefore)
	delegationIDs := make(map[string]struct{}, delegationBefore)
	for i, task := range tasks {
		handoffID := fmt.Sprintf("completed-handoff-%d", i)
		if i < selfBefore {
			selfIDs[handoffID] = struct{}{}
			addCompletedWakeupTaskHandoff(t, s, handoffID, task.ID, 101, 101)
			continue
		}
		delegationIDs[handoffID] = struct{}{}
		addCompletedWakeupTaskHandoff(t, s, handoffID, task.ID, 101, 102)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	selfAfter := 0
	delegationAfter := 0
	for _, handoff := range state.HandoffsReported {
		if _, ok := selfIDs[handoff.ID]; ok {
			selfAfter++
		}
		if _, ok := delegationIDs[handoff.ID]; ok {
			delegationAfter++
		}
	}
	t.Logf("self claim completed: before %d -> after %d", selfBefore, selfAfter)
	t.Logf("delegation completed: before %d -> after %d", delegationBefore, delegationAfter)
	if selfAfter != 0 {
		t.Fatalf("self claim reports = %d, want 0", selfAfter)
	}
	if delegationAfter != delegationBefore {
		t.Fatalf("delegation reports = %d, want %d", delegationAfter, delegationBefore)
	}
}

func TestDetectWakeupDoesNotReportCompletedTaskClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Completed task claim", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "completed-claim", []string{"Task"}, []string{"Task description"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	sessionID := registerNamedTestAgentSession(t, s, "completed-claim-agent", os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, sessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, sessionID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, sessionID); err != nil {
		t.Fatalf("UpdateTask done: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsReported) != 0 {
		t.Fatalf("reported handoffs = %#v, want completed self claim excluded", state.HandoffsReported)
	}
}

func TestDetectWakeupReportsCompletedDelegatedTaskHandoff(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Completed delegated task", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "completed-delegation", []string{"Task"}, []string{"Task description"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	const requesterLabel = "completed-delegation-requester"
	const receiverLabel = "completed-delegation-receiver"
	addLiveParentGoalClaim(t, s, tasks[0].ID, requesterLabel)
	addTestAgentSession(t, s, receiverLabel)
	handoff, err := s.RequestTaskHandoff(ctx, "completed-delegated-task-handoff", tasks[0].ID, testSessionID(requesterLabel), "Implement the task")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, tasks[0].ID, testSessionID(receiverLabel)); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, handoff.ID, tasks[0].ID, "Implemented and tested the task"); err != nil {
		t.Fatalf("CompleteTaskHandoff: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsReported) != 1 || state.HandoffsReported[0].ID != handoff.ID {
		t.Fatalf("reported handoffs = %#v, want delegated handoff %q", state.HandoffsReported, handoff.ID)
	}
}

func TestDetectWakeupExcludesCommanderClaimAndKeepsUndelegatedExecutorClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	taskIDs := addTestTasks(t, s, 3)
	projectID := taskIDsProjectID(t, s, taskIDs[0])
	const commanderSessionLabel = "commander-session"
	const executorSessionLabel = "executor-session"
	commanderSessionID := testSessionID(commanderSessionLabel)
	executorSessionID := testSessionID(executorSessionLabel)

	registerNamedTestAgentSession(t, s, commanderSessionLabel, os.Getpid())
	addTestAgentSession(t, s, executorSessionLabel)
	if _, err := s.ClaimProject(ctx, projectID, commanderSessionID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, taskIDs[0], commanderSessionID); err != nil {
		t.Fatalf("ClaimTask commander: %v", err)
	}
	if _, err := s.ClaimTask(ctx, taskIDs[1], executorSessionID); err != nil {
		t.Fatalf("ClaimTask executor: %v", err)
	}
	addLiveParentGoalClaim(t, s, taskIDs[0], commanderSessionLabel)
	if _, err := s.RequestTaskHandoff(ctx, "commander-test-handoff", taskIDs[2], commanderSessionID, ""); err != nil {
		t.Fatalf("RequestTaskHandoff delegated: %v", err)
	}

	state, err := s.DetectWakeup(ctx, projectID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.UndelegatedClaims) != 1 || state.UndelegatedClaims[0].ID != taskIDs[1] {
		t.Fatalf("undelegated claims = %#v, want only executor task %d", state.UndelegatedClaims, taskIDs[1])
	}
}

func TestClassifyWakeupDecisionsUsesPendingPredicates(t *testing.T) {
	defaultAppliedAt := time.Now().UTC()
	appliedAt := time.Now().UTC()
	projectGoalIDs := map[int64]struct{}{2: {}}

	human, defaultApplied := classifyWakeupDecisions([]domain.Decision{
		{ID: 1, GoalID: 2, Status: domain.DecisionAnswered},
		{
			ID:               2,
			GoalID:           2,
			Status:           domain.DecisionAnswered,
			DefaultAppliedAt: &defaultAppliedAt,
		},
		{
			ID:        3,
			GoalID:    2,
			Status:    domain.DecisionAnswered,
			AppliedAt: &appliedAt,
		},
		{ID: 4, GoalID: 2, Status: domain.DecisionOpen},
		{ID: 5, GoalID: 3, Status: domain.DecisionAnswered},
	}, projectGoalIDs)

	if got := len(human); got != 1 || human[0].ID != 1 {
		t.Fatalf("human answered decisions = %#v, want only decision-human", human)
	}
	if got := len(defaultApplied); got != 1 || defaultApplied[0].ID != 2 {
		t.Fatalf("default-applied decisions = %#v, want only decision-default", defaultApplied)
	}
}

func TestDetectWakeupCollectsUnappliedDecisionsAndStaleClaims(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Detect stalled handoffs", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "wakeup-detections", []string{
		"human decision task",
		"default decision task",
		"stale claim task",
		"live claim task",
	}, []string{
		"Receive a human decision.",
		"Receive a default decision.",
		"Keep the stale claim visible.",
		"Keep the live claim hidden.",
	})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	humanDecision, err := s.AskDecision(ctx, AskInput{
		GoalID:   goal.ID,
		TaskID:   tasks[0].ID,
		Kind:     domain.KindDecision,
		Question: "human answer",
	})
	if err != nil {
		t.Fatalf("AskDecision human: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: humanDecision.ID, AnswerText: "answer"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}

	defaultAfterMs := int64(1)
	if _, err := s.AskDecision(ctx, AskInput{
		GoalID:         goal.ID,
		TaskID:         tasks[1].ID,
		Kind:           domain.KindDecision,
		Question:       "default answer",
		Options:        []domain.Option{{Label: "A"}},
		DefaultOption:  "A",
		DefaultAfterMs: &defaultAfterMs,
	}); err != nil {
		t.Fatalf("AskDecision default: %v", err)
	}
	if _, err := s.ApplyExpiredDefaults(ctx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("ApplyExpiredDefaults: %v", err)
	}

	addTestAgentSession(t, s, "missing-session")
	if _, err := s.ClaimTask(ctx, tasks[2].ID, testSessionID("missing-session")); err != nil {
		t.Fatalf("ClaimTask stale: %v", err)
	}
	liveSessionID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, liveSessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[3].ID, liveSessionID); err != nil {
		t.Fatalf("ClaimTask live: %v", err)
	}

	state, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if got := len(state.AnsweredUnappliedDecisions); got != 1 || state.AnsweredUnappliedDecisions[0].ID != humanDecision.ID {
		t.Fatalf("answered unapplied decisions = %#v, want %d", state.AnsweredUnappliedDecisions, humanDecision.ID)
	}
	if got := len(state.DefaultUnappliedDecisions); got != 1 {
		t.Fatalf("default unapplied decisions = %#v, want one", state.DefaultUnappliedDecisions)
	}
	if got := len(state.StaleClaims); got != 1 || state.StaleClaims[0].ID != tasks[2].ID {
		t.Fatalf("stale claims = %#v, want only %d", state.StaleClaims, tasks[2].ID)
	}
}

func taskIDsProjectID(t *testing.T, s *Store, taskID int64) int64 {
	t.Helper()
	projectID, err := s.ProjectIDForTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ProjectIDForTask: %v", err)
	}
	return projectID
}

func updateWakeupTask(t *testing.T, s *Store, taskID int64, status domain.TaskStatus) {
	t.Helper()
	if _, err := s.UpdateTask(context.Background(), taskID, status, 0); err != nil {
		t.Fatalf("UpdateTask %d: %v", taskID, err)
	}
}
