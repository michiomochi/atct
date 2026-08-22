package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

func TestPendingCommandReturnsExitOneWithoutAnswers(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if output != "" {
		t.Fatalf("pendingCommand output = %q, want empty", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandReportsActiveGoalWithoutTasks(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Break this goal into tasks", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{
		"An active goal has no tasks declared.",
		"Undeclared active goals:",
		domain.Headline(goal.Content),
		"goal_id: " + goal.ID,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("pendingCommand output does not contain %q: %q", want, output)
		}
	}
}

func TestPendingCommandKeepsMultilineGoalOnOneLine(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Break this goal into tasks\n\nDetails must stay out of pending output.", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, domain.Headline(goal.Content)) {
		t.Fatalf("pendingCommand output does not contain goal headline %q: %q", domain.Headline(goal.Content), output)
	}
	if strings.Contains(output, domain.Body(goal.Content)) {
		t.Fatalf("pendingCommand output included goal body: %q", output)
	}
}

func TestPendingCommandExcludesProposedGoal(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Await approval before pending work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, goal.ID, "agent", "proposed-pending", []string{"task awaiting approval"}, []string{"Wait for approval before claiming this task."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	// This state can only exist in databases created before the declaration gate.
	db, err := sql.Open("sqlite", filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE goals SET status = ? WHERE id = ?", string(domain.GoalProposed), goal.ID); err != nil {
		_ = db.Close()
		t.Fatalf("set goal proposed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close test db: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if output != "" {
		t.Fatalf("pendingCommand output = %q, want empty for proposed goal %s", output, goal.ID)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandReportsGoalAfterTaskDeclarationUntilTaskDone(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Declare work for this goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-goal", []string{"first task"}, []string{"Complete the first task declared for the goal."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{pendingWakeupReason, "Unstarted tasks:", "first task", tasks[0].ID} {
		if !strings.Contains(output, want) {
			t.Fatalf("pendingCommand output does not contain %q: %q", want, output)
		}
	}

	s = openPendingStore(t, dir)
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close after UpdateTask: %v", err)
	}

	output, exitCode, err = pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand after UpdateTask: %v", err)
	}
	for _, want := range []string{pendingCompletedGoalReason, domain.Headline(goal.Content), goal.ID} {
		if !strings.Contains(output, want) {
			t.Fatalf("pendingCommand output after UpdateTask does not contain %q: %q", want, output)
		}
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code after UpdateTask = %d, want 0", exitCode)
	}
}

func TestPendingCommandExcludesGoalWaitingForHumanAnswerFromWakeup(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Wait for a human answer", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "waiting-answer", []string{"blocked task"}, []string{"Continue after the human answers."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID,
		Kind: domain.KindDecision, Question: "Which path should the agent take?",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if output != "" {
		t.Fatalf("pendingCommand output = %q, want empty while waiting for a human answer", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandIncludesAllPendingReasons(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	undeclaredGoal, err := s.CreateGoal(ctx, project.ID, "Declare the missing goal work", "human")
	if err != nil {
		t.Fatalf("CreateGoal undeclared: %v", err)
	}
	claimedGoal, err := s.CreateGoal(ctx, project.ID, "Continue the claimed goal work", "human")
	if err != nil {
		t.Fatalf("CreateGoal claimed: %v", err)
	}
	claimedTasks, err := s.DeclareTasks(ctx, claimedGoal.ID, "agent", "run-all", []string{"unfinished claimed work"}, []string{"Complete the claimed work before selecting another goal."})
	if err != nil {
		t.Fatalf("DeclareTasks claimed: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-all", 0); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "run-all", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, claimedTasks[0].ID, "run-all"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	decisionGoal, err := s.CreateGoal(ctx, project.ID, "Wait for the answered decision", "human")
	if err != nil {
		t.Fatalf("CreateGoal decision: %v", err)
	}
	decisionTasks, err := s.DeclareTasks(ctx, decisionGoal.ID, "agent", "run-decision", []string{"blocked decision work"}, []string{"Complete the blocked work after its decision is answered."})
	if err != nil {
		t.Fatalf("DeclareTasks decision: %v", err)
	}
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: decisionGoal.ID, TaskID: decisionTasks[0].ID, Kind: domain.KindDecision,
		Question: "Which pending reason should remain?", AgentSessionID: "run-decision",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{DecisionID: decision.ID, AnswerText: "all of them"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-all")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{
		"A human answered a decision you parked.",
		pendingClaimReason,
		"An active goal has no tasks declared.",
		decision.ID,
		"unfinished claimed work",
		claimedTasks[0].ID,
		domain.Headline(undeclaredGoal.Content),
		undeclaredGoal.ID,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("pendingCommand output does not contain %q: %q", want, output)
		}
	}
}

func TestPendingCommandReportsStaleClaimSeparatelyFromOwnClaim(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	ownGoal, err := s.CreateGoal(ctx, project.ID, "Continue my work", "human")
	if err != nil {
		t.Fatalf("CreateGoal own: %v", err)
	}
	ownTasks, err := s.DeclareTasks(ctx, ownGoal.ID, "agent", "own-claim", []string{"my unfinished task"}, []string{"Continue the task claimed by the current agent session."})
	if err != nil {
		t.Fatalf("DeclareTasks own: %v", err)
	}
	if _, err := s.ClaimTask(ctx, ownTasks[0].ID, "own-run"); err != nil {
		t.Fatalf("ClaimTask own: %v", err)
	}

	staleGoal, err := s.CreateGoal(ctx, project.ID, "Recover abandoned work", "human")
	if err != nil {
		t.Fatalf("CreateGoal stale: %v", err)
	}
	staleTasks, err := s.DeclareTasks(ctx, staleGoal.ID, "agent", "stale-claim", []string{"abandoned task"}, []string{"Recover the task left by an agent session that stopped."})
	if err != nil {
		t.Fatalf("DeclareTasks stale: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "stale-run", 0); err != nil {
		t.Fatalf("RegisterAgentSession stale: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "stale-run", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject stale: %v", err)
	}
	if _, err := s.ClaimTask(ctx, staleTasks[0].ID, "stale-run"); err != nil {
		t.Fatalf("ClaimTask stale: %v", err)
	}
	otherProject, err := s.CreateProject(ctx, "other", filepath.Join(t.TempDir(), "other-project"))
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, otherProject.ID, "Leave another project alone", "human")
	if err != nil {
		t.Fatalf("CreateGoal other: %v", err)
	}
	otherTasks, err := s.DeclareTasks(ctx, otherGoal.ID, "agent", "other-stale-claim", []string{"other project task"}, []string{"Do not report this task for the selected project."})
	if err != nil {
		t.Fatalf("DeclareTasks other: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "other-stale-run", 0); err != nil {
		t.Fatalf("RegisterAgentSession other: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "other-stale-run", otherProject.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject other: %v", err)
	}
	if _, err := s.ClaimTask(ctx, otherTasks[0].ID, "other-stale-run"); err != nil {
		t.Fatalf("ClaimTask other: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "own-run")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{
		pendingClaimReason,
		"my unfinished task",
		pendingStaleClaimReason,
		"abandoned task",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("pendingCommand output does not contain %q: %q", want, output)
		}
	}
	if strings.Count(output, "my unfinished task") != 1 {
		t.Fatalf("pendingCommand repeated own claim in stale section: %q", output)
	}
	if strings.Contains(output, "other project task") {
		t.Fatalf("pendingCommand reported a claim from another project: %q", output)
	}
}

func TestPendingCommandReturnsDecisionIDAndQuestion(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Wait for a human", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	// An active decision has to name the task it is holding up.
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "blocked-1", []string{"blocked task"}, []string{"Complete the blocked task after the human chooses a release channel."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision, Question: "Which release channel should we use?", AgentSessionID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{
		DecisionID: decision.ID, AnswerText: "stable",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, "decision_id: "+decision.ID) {
		t.Fatalf("pendingCommand output does not contain decision ID %q: %q", decision.ID, output)
	}
	if !strings.Contains(output, "Which release channel should we use?") {
		t.Fatalf("pendingCommand output does not contain question: %q", output)
	}
}

func TestPendingCommandDoesNotCallDefaultAnswerHuman(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Wait for defaults", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	addPendingTestDecision(t, s, ctx, goal.ID, "default-one", "Which default should be used first?", true)
	addPendingTestDecision(t, s, ctx, goal.ID, "default-two", "Which default should be used second?", true)
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if strings.Contains(output, "A human answered") {
		t.Fatalf("default-applied decisions were reported as human-answered: %q", output)
	}
	if !strings.Contains(output, "No one answered a decision you parked, so its default was applied.") {
		t.Fatalf("pendingCommand did not report default application: %q", output)
	}
}

func TestPendingCommandKeepsHumanAnswerReason(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Wait for human answers", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	addPendingTestDecision(t, s, ctx, goal.ID, "human-one", "Which human answer should be used first?", false)
	addPendingTestDecision(t, s, ctx, goal.ID, "human-two", "Which human answer should be used second?", false)
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, pendingDecisionReason) {
		t.Fatalf("pendingCommand did not report a human answer: %q", output)
	}
	if strings.Contains(output, "No one answered a decision you parked") {
		t.Fatalf("human-answered decisions were reported as default-applied: %q", output)
	}
}

func TestPendingCommandListsHumanReasonBeforeDefaultReason(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Wait for either answer", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	addPendingTestDecision(t, s, ctx, goal.ID, "mixed-human", "Which human answer is pending?", false)
	addPendingTestDecision(t, s, ctx, goal.ID, "mixed-default", "Which default answer is pending?", true)
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	humanIndex := strings.Index(output, pendingDecisionReason)
	defaultIndex := strings.Index(output, "No one answered a decision you parked, so its default was applied.")
	if humanIndex < 0 || defaultIndex < 0 {
		t.Fatalf("pendingCommand did not include both decision reasons: %q", output)
	}
	if humanIndex > defaultIndex {
		t.Fatalf("pendingCommand listed default reason before human reason: %q", output)
	}
}

func TestPendingCommandFiltersDecisionsFromOtherProject(t *testing.T) {
	dir := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "current-project")
	otherRoot := filepath.Join(t.TempDir(), "other-project")
	s := openPendingStore(t, dir)
	ctx := context.Background()
	_, err := s.CreateProject(ctx, "current", projectRoot)
	if err != nil {
		t.Fatalf("CreateProject current: %v", err)
	}
	other, err := s.CreateProject(ctx, "other", otherRoot)
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	goal, err := s.CreateGoal(ctx, other.ID, "Other goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	// An active decision has to name the task it is holding up.
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "blocked-other", []string{"blocked task"}, []string{"Complete the blocked task in the other project."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision, Question: "Question from another project", AgentSessionID: "run-other",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{
		DecisionID: decision.ID, AnswerText: "ignore",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if output != "" {
		t.Fatalf("pendingCommand output = %q, want empty", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandReturnsExitOneForUnregisteredCWD(t *testing.T) {
	dir := t.TempDir()
	registeredRoot := filepath.Join(t.TempDir(), "registered-project")
	unregisteredRoot := filepath.Join(t.TempDir(), "unregistered-project")
	s := openPendingStore(t, dir)
	if _, err := s.CreateProject(context.Background(), "registered", registeredRoot); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, unregisteredRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if output != "" {
		t.Fatalf("pendingCommand output = %q, want empty", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandUsesLatestProjectAgentSessionWithoutEnv(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Resume the claimed work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "latest-run", []string{"unfinished task"}, []string{"Complete the unfinished task when the latest run resumes."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-latest", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "run-latest", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-latest"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, unfinishedClaimMarker) || !strings.Contains(output, "unfinished task") {
		t.Fatalf("pendingCommand did not report the latest project's unfinished claim: %q", output)
	}
}

func TestPendingCommandPrefersExplicitAgentSessionIDOverLatestProjectAgentSession(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Use the selected run", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "run-selection", []string{"latest task", "explicit task"}, []string{"Complete the latest task selected for the run.", "Complete the explicitly selected task after it."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-latest", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession latest: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "run-latest", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject latest: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-latest"); err != nil {
		t.Fatalf("ClaimTask latest: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-explicit", 0); err != nil {
		t.Fatalf("RegisterAgentSession explicit: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[1].ID, "run-explicit"); err != nil {
		t.Fatalf("ClaimTask explicit: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-explicit")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, "explicit task") {
		t.Fatalf("pendingCommand did not report the explicitly selected run: %q", output)
	}
	if strings.Contains(output, "latest task") {
		t.Fatalf("pendingCommand reported the latest agent session despite ATCT_AGENT_SESSION_ID override: %q", output)
	}
}

func TestPendingCommandDoesNotReportRunningAnotherAgentSessionsClaim(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Do not steal another run's work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "other-run", []string{"other run task"}, []string{"Complete the task owned by the other run without stealing it."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-latest", 0); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "run-latest", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-other", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession other: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "run-other", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject other: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-other"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-latest")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if output != "" {
		t.Fatalf("pendingCommand reported another run's claim: %q", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandReportsActiveGoalAfterAllTasksDone(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Report completed work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "completed-work", []string{"finished task"}, []string{"Report the finished work."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{
		"All tasks are done but the active goal has no completion report.",
		"Call `atct_goal_complete`",
		domain.Headline(goal.Content),
		goal.ID,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("pendingCommand output does not contain %q: %q", want, output)
		}
	}
}

func TestPendingCommandDoesNotReportGoalWithCompletionReport(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Already reported completed work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "reported-completion", []string{"reported task"}, []string{"Report the completed task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := s.LinkTaskCommit(ctx, tasks[0].ID, domain.TaskCommit{}); err != nil {
		t.Fatalf("LinkTaskCommit: %v", err)
	}
	if _, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, Kind: "completion", Question: "Approve this goal as complete?",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if strings.Contains(output, pendingCompletedGoalReason) {
		t.Fatalf("pendingCommand reported a goal with a completion report: %q", output)
	}
	if strings.Contains(output, domain.Headline(goal.Content)) {
		t.Fatalf("pendingCommand reported the completed goal: %q", output)
	}
	if output != "" {
		t.Fatalf("pendingCommand output = %q, want empty", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandUsesSeparateReasonForAllDroppedGoal(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Close withdrawn work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "withdrawn-work", []string{"withdrawn task"}, []string{"Close the withdrawn work."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDropped, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, "All tasks in an active goal were dropped.") {
		t.Fatalf("pendingCommand output does not report the dropped goal: %q", output)
	}
	if strings.Contains(output, "All tasks are done but the active goal has no completion report.") {
		t.Fatalf("pendingCommand reported the dropped goal as completed: %q", output)
	}
	for _, want := range []string{domain.Headline(goal.Content), goal.ID, "atct_goal_complete", "atct_task_declare"} {
		if !strings.Contains(strings.ToLower(output), strings.ToLower(want)) {
			t.Fatalf("pendingCommand output does not contain %q: %q", want, output)
		}
	}
}

func TestPendingCommandDoesNotReportCommitlessGoalWhenAnyTaskHasLinkedCommit(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Report linked work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "linked-work", []string{"linked task", "unlinked task"}, []string{"Record the linked work.", "Record the unlinked work."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	for _, task := range tasks {
		if _, err := s.UpdateTask(ctx, task.ID, domain.TaskDone, ""); err != nil {
			t.Fatalf("UpdateTask: %v", err)
		}
	}
	if err := s.LinkTaskCommit(ctx, tasks[0].ID, domain.TaskCommit{}); err != nil {
		t.Fatalf("LinkTaskCommit: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, _, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if strings.Contains(output, commitlessGoalMarker) {
		t.Fatalf("pendingCommand reported a goal with a linked commit as commitless: %q", output)
	}
}

func TestPendingCommandDoesNotReportCommitlessGoalWhenTodoTaskRemains(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Continue unfinished work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, goal.ID, "agent", "unfinished-work", []string{"unfinished task"}, []string{"Continue the unfinished work."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, _, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if strings.Contains(output, commitlessGoalMarker) {
		t.Fatalf("pendingCommand reported a goal with a todo task as commitless: %q", output)
	}
}

func TestPendingCommandDoesNotReportCommitlessGoalWhenAllTasksDropped(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Withdraw unfinished work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "withdrawn-work", []string{"withdrawn task"}, []string{"Withdraw the unfinished work."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDropped, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, _, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if strings.Contains(output, commitlessGoalMarker) {
		t.Fatalf("pendingCommand reported an all-dropped goal as commitless: %q", output)
	}
}

func TestPendingCommandDoesNotReportCommitlessGoalWhenNoTasksExist(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if _, err := s.CreateGoal(ctx, project.ID, "Declare work before reporting", "human"); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, _, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if strings.Contains(output, commitlessGoalMarker) {
		t.Fatalf("pendingCommand reported a goal with no tasks as commitless: %q", output)
	}
}

func TestPendingCommandReportsCommitlessGoalWhenAllTasksDoneWithoutLinkedCommit(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Link the completed work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "completed-unlinked-work", []string{"completed task"}, []string{"Link the completed work."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{
		commitlessGoalMarker,
		domain.Headline(goal.Content),
		goal.ID,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("pendingCommand output does not contain %q: %q", want, output)
		}
	}
}

func TestPendingCommandDoesNotReportCommitlessGoalWhenOpenCompletionDecisionExists(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Await completion approval", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "await-completion-approval", []string{"completed task"}, []string{"Await completion approval."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if _, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, Kind: domain.KindCompletion, Question: "Approve this goal as complete?",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if strings.Contains(output, commitlessGoalMarker) {
		t.Fatalf("pendingCommand reported a goal with an open completion decision as commitless: %q", output)
	}
	if strings.Contains(output, domain.Headline(goal.Content)) {
		t.Fatalf("pendingCommand reported the goal with an open completion decision: %q", output)
	}
	if output != "" {
		t.Fatalf("pendingCommand output = %q, want empty", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandReportsDoingTaskWithoutClaimUntilReturnedToTodo(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Recover an unclaimed task", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "unclaimed-doing", []string{"unclaimed doing task"}, []string{"Return the task to todo."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, ""); err != nil {
		t.Fatalf("UpdateTask to doing: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, "A task is doing without a work lock.") {
		t.Fatalf("pendingCommand output does not report the unclaimed doing task: %q", output)
	}
	if !strings.Contains(output, tasks[0].ID) {
		t.Fatalf("pendingCommand output does not include task ID %q: %q", tasks[0].ID, output)
	}

	s = openPendingStore(t, dir)
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskTodo, ""); err != nil {
		t.Fatalf("UpdateTask to todo: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close after UpdateTask: %v", err)
	}

	output, exitCode, err = pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand after UpdateTask: %v", err)
	}
	if strings.Contains(output, "A task is doing without a work lock.") {
		t.Fatalf("pendingCommand kept the unclaimed doing reason after returning to todo: %q", output)
	}
}

func TestPendingCommandFiltersNewConditionsFromOtherProject(t *testing.T) {
	dir := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "current-project")
	otherRoot := filepath.Join(t.TempDir(), "other-project")
	s := openPendingStore(t, dir)
	ctx := context.Background()
	current, err := s.CreateProject(ctx, "current", projectRoot)
	if err != nil {
		t.Fatalf("CreateProject current: %v", err)
	}
	other, err := s.CreateProject(ctx, "other", otherRoot)
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}

	doneGoal, err := s.CreateGoal(ctx, other.ID, "Other completed goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal done: %v", err)
	}
	doneTasks, err := s.DeclareTasks(ctx, doneGoal.ID, "agent", "other-done", []string{"other done task"}, []string{"Complete the other task."})
	if err != nil {
		t.Fatalf("DeclareTasks done: %v", err)
	}
	if _, err := s.UpdateTask(ctx, doneTasks[0].ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask done: %v", err)
	}

	droppedGoal, err := s.CreateGoal(ctx, other.ID, "Other withdrawn goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal dropped: %v", err)
	}
	droppedTasks, err := s.DeclareTasks(ctx, droppedGoal.ID, "agent", "other-dropped", []string{"other dropped task"}, []string{"Withdraw the other task."})
	if err != nil {
		t.Fatalf("DeclareTasks dropped: %v", err)
	}
	if _, err := s.UpdateTask(ctx, droppedTasks[0].ID, domain.TaskDropped, ""); err != nil {
		t.Fatalf("UpdateTask dropped: %v", err)
	}

	doingGoal, err := s.CreateGoal(ctx, other.ID, "Other unclaimed goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal doing: %v", err)
	}
	doingTasks, err := s.DeclareTasks(ctx, doingGoal.ID, "agent", "other-doing", []string{"other doing task"}, []string{"Return the other task to todo."})
	if err != nil {
		t.Fatalf("DeclareTasks doing: %v", err)
	}
	if _, err := s.UpdateTask(ctx, doingTasks[0].ID, domain.TaskDoing, ""); err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}
	if current.ID == "" {
		t.Fatal("current project has no ID")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if output != "" {
		t.Fatalf("pendingCommand reported reasons from another project: %q", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestReleaseTaskReturnsDoingTaskToTodo(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Release stale work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "release-stale", []string{"stale task"}, []string{"Release the stale task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "stale-run"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, "stale-run"); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	released, err := s.ReleaseTask(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}
	if released.Status != domain.TaskTodo {
		t.Fatalf("released task status = %s, want %s", released.Status, domain.TaskTodo)
	}
	if released.ClaimedBy != "" {
		t.Fatalf("released task claimed_by = %q, want empty", released.ClaimedBy)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
}

func TestPendingCommandPutsUnstartedTasksBeforeOwnClaim(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	claimedGoal, err := s.CreateGoal(ctx, project.ID, "Continue the held work", "human")
	if err != nil {
		t.Fatalf("CreateGoal claimed: %v", err)
	}
	claimedTasks, err := s.DeclareTasks(ctx, claimedGoal.ID, "agent", "held-work", []string{"held task"}, []string{"Continue the held task."})
	if err != nil {
		t.Fatalf("DeclareTasks claimed: %v", err)
	}
	if _, err := s.ClaimTask(ctx, claimedTasks[0].ID, "run-lock"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	availableGoal, err := s.CreateGoal(ctx, project.ID, "Take available work", "human")
	if err != nil {
		t.Fatalf("CreateGoal available: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, availableGoal.ID, "agent", "available-work", []string{"available one", "available two"}, []string{"Take the first available task.", "Take the second available task."}); err != nil {
		t.Fatalf("DeclareTasks available: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-lock")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	wantReason := "You hold 1 work locks. 2 unstarted tasks in active goals (waiting for an answer: 0 / working: 0 / untouched: 2). 2 tasks in active goals have no work lock.\nIf you are waiting on a human, take one of those instead of stopping."
	if !strings.Contains(output, wantReason) {
		t.Fatalf("pendingCommand output does not contain %q: %q", wantReason, output)
	}
	if strings.Index(output, "Unstarted tasks:") > strings.Index(output, unfinishedClaimMarker) {
		t.Fatalf("pendingCommand listed held work before unstarted work: %q", output)
	}
}

func TestPendingCommandReportsUnstartedTaskBreakdown(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	waitingGoal, err := s.CreateGoal(ctx, project.ID, "Wait for a human answer", "human")
	if err != nil {
		t.Fatalf("CreateGoal waiting: %v", err)
	}
	waitingTasks, err := s.DeclareTasks(ctx, waitingGoal.ID, "agent", "breakdown-waiting", []string{"waiting task"}, []string{"Continue after the human answers."})
	if err != nil {
		t.Fatalf("DeclareTasks waiting: %v", err)
	}
	for _, question := range []string{"Which waiting path should be taken first?", "Which waiting path should be taken second?"} {
		if _, err := s.AskDecision(ctx, store.AskInput{
			GoalID: waitingGoal.ID, TaskID: waitingTasks[0].ID, Kind: domain.KindDecision,
			Question: question, AgentSessionID: "run-breakdown",
		}); err != nil {
			t.Fatalf("AskDecision: %v", err)
		}
	}

	workingGoal, err := s.CreateGoal(ctx, project.ID, "Continue work already in progress", "human")
	if err != nil {
		t.Fatalf("CreateGoal working: %v", err)
	}
	workingTasks, err := s.DeclareTasks(ctx, workingGoal.ID, "agent", "breakdown-working", []string{"claimed task", "working task"}, []string{"Claim the first task.", "Continue the second task."})
	if err != nil {
		t.Fatalf("DeclareTasks working: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "run-breakdown", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "run-breakdown", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, workingTasks[0].ID, "run-breakdown"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	untouchedGoal, err := s.CreateGoal(ctx, project.ID, "Take untouched work", "human")
	if err != nil {
		t.Fatalf("CreateGoal untouched: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, untouchedGoal.ID, "agent", "breakdown-untouched", []string{"untouched task"}, []string{"Take the untouched task."}); err != nil {
		t.Fatalf("DeclareTasks untouched: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-breakdown")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	for _, want := range []string{
		"You are waiting on a human for 2 decisions with no default. That does not\nblock the 1 tasks below.",
		"You hold 1 work locks. 3 unstarted tasks in active goals (waiting for an answer: 1 / working: 1 / untouched: 1). 1 tasks in active goals have no work lock.\nIf you are waiting on a human, take one of those instead of stopping.",
		"Unstarted tasks:",
		"untouched task",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("pendingCommand output does not contain %q: %q", want, output)
		}
	}
	if strings.Contains(output, "3 tasks in active goals have no work lock") {
		t.Fatalf("pendingCommand used the total as the claimable count: %q", output)
	}
}

func TestPendingCommandOmitsAvailableWorkTailWhenCountIsZero(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Continue only held work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "held-only", []string{"held task"}, []string{"Continue the held task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-held-only"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-held-only")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, "You hold 1 work locks.") {
		t.Fatalf("pendingCommand output does not contain lock count: %q", output)
	}
	if strings.Contains(output, "tasks in active goals have no work lock") || strings.Contains(output, "If you are waiting on a human") {
		t.Fatalf("pendingCommand reported an available-work tail with no available work: %q", output)
	}
}

func TestPendingCommandCombinesOpenNoDefaultDecisionWithUnstartedWork(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	decisionGoal, err := s.CreateGoal(ctx, project.ID, "Wait for human choice", "human")
	if err != nil {
		t.Fatalf("CreateGoal decision: %v", err)
	}
	decisionTasks, err := s.DeclareTasks(ctx, decisionGoal.ID, "agent", "no-default", []string{"waiting task"}, []string{"Continue after the human answers."})
	if err != nil {
		t.Fatalf("DeclareTasks decision: %v", err)
	}
	if _, err := s.AskDecision(ctx, store.AskInput{
		GoalID: decisionGoal.ID, TaskID: decisionTasks[0].ID, Kind: domain.KindDecision,
		Question: "Which path should be taken?", AgentSessionID: "run-no-default",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	availableGoal, err := s.CreateGoal(ctx, project.ID, "Take work while waiting", "human")
	if err != nil {
		t.Fatalf("CreateGoal available: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, availableGoal.ID, "agent", "available-while-waiting", []string{"available task"}, []string{"Take the available task."}); err != nil {
		t.Fatalf("DeclareTasks available: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-no-default")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	wantReason := "You are waiting on a human for 1 decisions with no default. That does not\nblock the 1 tasks below."
	if !strings.Contains(output, wantReason) {
		t.Fatalf("pendingCommand output does not contain %q: %q", wantReason, output)
	}
}

func TestPendingCommandDoesNotCombineDefaultedDecisionWithUnstartedWork(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	decisionGoal, err := s.CreateGoal(ctx, project.ID, "Use the default later", "human")
	if err != nil {
		t.Fatalf("CreateGoal decision: %v", err)
	}
	decisionTasks, err := s.DeclareTasks(ctx, decisionGoal.ID, "agent", "with-default", []string{"defaulted waiting task"}, []string{"Continue after the default."})
	if err != nil {
		t.Fatalf("DeclareTasks decision: %v", err)
	}
	defaultAfterMs := time.Hour.Milliseconds()
	if _, err := s.AskDecision(ctx, store.AskInput{
		GoalID: decisionGoal.ID, TaskID: decisionTasks[0].ID, Kind: domain.KindDecision,
		Question: "Which default path should be taken?", AgentSessionID: "run-with-default",
		Options: []domain.Option{{Label: "default"}}, DefaultOption: "default", DefaultAfterMs: &defaultAfterMs,
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	availableGoal, err := s.CreateGoal(ctx, project.ID, "Take work with a default pending", "human")
	if err != nil {
		t.Fatalf("CreateGoal available: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, availableGoal.ID, "agent", "available-with-default", []string{"available task"}, []string{"Take the available task."}); err != nil {
		t.Fatalf("DeclareTasks available: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-with-default")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if strings.Contains(output, "You are waiting on a human for") {
		t.Fatalf("pendingCommand reported a decision with a default: %q", output)
	}
}

func TestPendingCommandDoesNotCombineNoDefaultDecisionWithoutUnstartedWork(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Wait without available work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "no-default-no-work", []string{"waiting task"}, []string{"Continue after the human answers."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision,
		Question: "Which path should be taken?", AgentSessionID: "run-no-default-no-work",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-no-default-no-work")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if strings.Contains(output, "You are waiting on a human for") {
		t.Fatalf("pendingCommand reported a no-default decision without unstarted work: %q", output)
	}
	if exitCode != 1 {
		t.Fatalf("pendingCommand exit code = %d, want 1", exitCode)
	}
}

func TestPendingCommandCountsUnstartedTasksOnlyInSelectedProject(t *testing.T) {
	dir := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "current-project")
	otherRoot := filepath.Join(t.TempDir(), "other-project")
	s := openPendingStore(t, dir)
	ctx := context.Background()
	current, err := s.CreateProject(ctx, "current", projectRoot)
	if err != nil {
		t.Fatalf("CreateProject current: %v", err)
	}
	other, err := s.CreateProject(ctx, "other", otherRoot)
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	claimedGoal, err := s.CreateGoal(ctx, current.ID, "Hold current work", "human")
	if err != nil {
		t.Fatalf("CreateGoal claimed: %v", err)
	}
	claimedTasks, err := s.DeclareTasks(ctx, claimedGoal.ID, "agent", "current-held", []string{"current held task"}, []string{"Continue current held work."})
	if err != nil {
		t.Fatalf("DeclareTasks claimed: %v", err)
	}
	if _, err := s.ClaimTask(ctx, claimedTasks[0].ID, "run-current"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	currentAvailableGoal, err := s.CreateGoal(ctx, current.ID, "Take current work", "human")
	if err != nil {
		t.Fatalf("CreateGoal current available: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, currentAvailableGoal.ID, "agent", "current-available", []string{"current available task"}, []string{"Take current available work."}); err != nil {
		t.Fatalf("DeclareTasks current available: %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, other.ID, "Leave other work alone", "human")
	if err != nil {
		t.Fatalf("CreateGoal other: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, otherGoal.ID, "agent", "other-available", []string{"other task one", "other task two"}, []string{"Leave the first other task alone.", "Leave the second other task alone."}); err != nil {
		t.Fatalf("DeclareTasks other: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctAgentSessionIDEnv, "run-current")

	output, exitCode, err := pendingCommand(dir, projectRoot)
	if err != nil {
		t.Fatalf("pendingCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("pendingCommand exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(output, "You hold 1 work locks. 1 unstarted tasks in active goals (waiting for an answer: 0 / working: 0 / untouched: 1). 1 tasks in active goals have no work lock.") {
		t.Fatalf("pendingCommand did not count only the selected project's task: %q", output)
	}
	if strings.Contains(output, "3 tasks in active goals have no work lock") {
		t.Fatalf("pendingCommand counted another project's tasks: %q", output)
	}
}

func newPendingFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	s := openPendingStore(t, dir)
	if _, err := s.CreateProject(context.Background(), "project", projectRoot); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	return dir, projectRoot
}

func openPendingStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return s
}

func addPendingTestDecision(t *testing.T, s *store.Store, ctx context.Context, goalID, taskKey, question string, applyDefault bool) domain.Decision {
	t.Helper()
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", taskKey, []string{"blocked task"}, []string{"Complete the blocked task after its decision is settled."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	input := store.AskInput{
		GoalID: goalID, TaskID: tasks[0].ID, Kind: domain.KindDecision,
		Question: question, AgentSessionID: "run-" + taskKey,
	}
	if applyDefault {
		zero := int64(0)
		input.Options = []domain.Option{{Label: "default"}}
		input.DefaultOption = "default"
		input.DefaultAfterMs = &zero
	}
	decision, err := s.AskDecision(ctx, input)
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if applyDefault {
		if _, err := s.ApplyExpiredDefaults(ctx, time.Now().Add(time.Second)); err != nil {
			t.Fatalf("ApplyExpiredDefaults: %v", err)
		}
		decision, err = s.GetDecision(ctx, decision.ID)
		if err != nil {
			t.Fatalf("GetDecision: %v", err)
		}
		return decision
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{DecisionID: decision.ID, AnswerText: "human answer"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	decision, err = s.GetDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	return decision
}
