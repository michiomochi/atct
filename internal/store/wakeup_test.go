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
	if state.ActiveGoalCount != 1 || state.UnstartedTaskCount != 1 || len(state.Tasks) != 0 {
		t.Fatalf("wakeup state = %+v, want one counted but non-actionable task", state)
	}
	if state.WaitingAnswerCount != 1 {
		t.Fatalf("waiting answer count = %d, want 1", state.WaitingAnswerCount)
	}
	if state.WaitingAnswerTaskCount != 1 || state.WorkingTaskCount != 0 || state.UntouchedTaskCount != 0 {
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
	if state.ActiveGoalCount != 0 || state.UnstartedTaskCount != 0 || len(state.Tasks) != 0 {
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
	if state.ActiveGoalCount != 1 || state.UnstartedTaskCount != 1 || len(state.Tasks) != 1 || state.Tasks[0].ID != tasks[1].ID {
		t.Fatalf("wakeup state = %+v, want one counted and actionable sibling task", state)
	}
	if state.WaitingAnswerTaskCount != 0 || state.WorkingTaskCount != 0 || state.UntouchedTaskCount != 1 {
		t.Fatalf("wakeup task breakdown = %+v, want one untouched task", state)
	}
	if state.UnstartedTaskCount != state.WaitingAnswerTaskCount+state.WorkingTaskCount+state.UntouchedTaskCount {
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
	if err := s.RegisterAgentSession(ctx, "wakeup-task-level-session", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "wakeup-task-level-session", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "wakeup-task-level-session"); err != nil {
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
		t.Fatalf("actionable tasks = %#v, want only available sibling %s", state.Tasks, tasks[2].ID)
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
	const runningSessionID = "wakeup-breakdown-running-session"
	if err := s.RegisterAgentSession(ctx, runningSessionID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, runningSessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	claim := func(taskID string) {
		t.Helper()
		if _, err := s.ClaimTask(ctx, taskID, runningSessionID); err != nil {
			t.Fatalf("ClaimTask %s: %v", taskID, err)
		}
	}
	ask := func(goalID, taskID, question string) {
		t.Helper()
		if _, err := s.AskDecision(ctx, AskInput{
			GoalID: goalID, TaskID: taskID,
			Kind: domain.KindDecision, Question: question,
		}); err != nil {
			t.Fatalf("AskDecision %s: %v", goalID, err)
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
	if state.ActiveGoalCount != 6 {
		t.Fatalf("active goal count = %d, want 6", state.ActiveGoalCount)
	}
	if state.UnstartedTaskCount != 6 {
		t.Fatalf("unstarted task count = %d, want 6", state.UnstartedTaskCount)
	}
	if state.WaitingAnswerTaskCount != 1 {
		t.Fatalf("waiting-answer task count = %d, want 1", state.WaitingAnswerTaskCount)
	}
	if state.WorkingTaskCount != 0 {
		t.Fatalf("working task count = %d, want 0", state.WorkingTaskCount)
	}
	if state.UntouchedTaskCount != 5 {
		t.Fatalf("untouched task count = %d, want 5", state.UntouchedTaskCount)
	}
	if state.UnstartedTaskCount != state.WaitingAnswerTaskCount+state.WorkingTaskCount+state.UntouchedTaskCount {
		t.Fatalf("unstarted task breakdown = %+v, want total to equal category sum", state)
	}
	wantTaskIDs := map[string]struct{}{
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
		t.Fatalf("undeclared goals = %#v, want proposed goal %s excluded", state.UndeclaredGoals, proposedEmptyGoal.ID)
	}
	if len(state.CommitlessGoals) != 0 {
		t.Fatalf("commitless goals = %#v, want proposed goal %s excluded", state.CommitlessGoals, proposedDoneGoal.ID)
	}
}

func TestDetectWakeupCollectsStalledHandoffCandidates(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	taskIDs := addTestTasks(t, s, 5)
	const claimSessionID = "stalled-handoff-claim"
	addLiveTaskClaim(t, s, taskIDs[0], claimSessionID)
	for _, taskID := range taskIDs[1:] {
		if _, err := s.ClaimTask(ctx, taskID, claimSessionID); err != nil {
			t.Fatalf("ClaimTask %s: %v", taskID, err)
		}
	}
	if err := s.RegisterAgentSession(ctx, "stalled-handoff-requester", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession requester: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "stalled-handoff-receiver", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession receiver: %v", err)
	}

	unreceived, err := s.RequestTaskHandoff(ctx, "handoff-unreceived", taskIDs[0], "stalled-handoff-requester")
	if err != nil {
		t.Fatalf("RequestTaskHandoff unreceived: %v", err)
	}
	unreported, err := s.RequestTaskHandoff(ctx, "handoff-unreported", taskIDs[1], "stalled-handoff-requester")
	if err != nil {
		t.Fatalf("RequestTaskHandoff unreported: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, unreported.ID, taskIDs[1], "stalled-handoff-receiver"); err != nil {
		t.Fatalf("ReceiveTaskHandoff unreported: %v", err)
	}
	completed, err := s.RequestTaskHandoff(ctx, "handoff-completed", taskIDs[2], "stalled-handoff-requester")
	if err != nil {
		t.Fatalf("RequestTaskHandoff completed: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, completed.ID, taskIDs[2], "stalled-handoff-receiver"); err != nil {
		t.Fatalf("ReceiveTaskHandoff completed: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, completed.ID, taskIDs[2]); err != nil {
		t.Fatalf("CompleteTaskHandoff completed: %v", err)
	}
	requestedClaim, err := s.RequestTaskHandoff(ctx, "handoff-requested-claim", taskIDs[4], "stalled-handoff-requester")
	if err != nil {
		t.Fatalf("RequestTaskHandoff requested claim: %v", err)
	}

	state, err := s.DetectWakeup(ctx, taskIDsProjectID(t, s, taskIDs[0]))
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.HandoffsAwaitingReceipt) != 2 {
		t.Fatalf("handoffs awaiting receipt = %#v, want %s and %s", state.HandoffsAwaitingReceipt, unreceived.ID, requestedClaim.ID)
	}
	if len(state.HandoffsAwaitingReport) != 1 || state.HandoffsAwaitingReport[0].ID != unreported.ID {
		t.Fatalf("handoffs awaiting report = %#v, want only %s", state.HandoffsAwaitingReport, unreported.ID)
	}
	if len(state.UndelegatedClaims) != 1 || state.UndelegatedClaims[0].ID != taskIDs[3] {
		t.Fatalf("undelegated claims = %#v, want only %s", state.UndelegatedClaims, taskIDs[3])
	}
	for _, handoff := range state.HandoffsAwaitingReceipt {
		if handoff.ID == completed.ID {
			t.Fatalf("completed handoff %s was reported as awaiting receipt", completed.ID)
		}
	}
	for _, handoff := range state.HandoffsAwaitingReport {
		if handoff.ID == completed.ID {
			t.Fatalf("completed handoff %s was reported as awaiting report", completed.ID)
		}
	}
}

func taskIDsProjectID(t *testing.T, s *Store, taskID string) string {
	t.Helper()
	projectID, err := s.ProjectIDForTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ProjectIDForTask: %v", err)
	}
	return projectID
}

func updateWakeupTask(t *testing.T, s *Store, taskID string, status domain.TaskStatus) {
	t.Helper()
	if _, err := s.UpdateTask(context.Background(), taskID, status, ""); err != nil {
		t.Fatalf("UpdateTask %s: %v", taskID, err)
	}
}
