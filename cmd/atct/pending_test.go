package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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

func TestPendingCommandReturnsDecisionIDAndQuestion(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Wait for a human", "")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	// An active decision has to name the task it is holding up.
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "blocked-1", []string{"blocked task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision, Question: "Which release channel should we use?", RunID: "run-1",
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
	goal, err := s.CreateGoal(ctx, other.ID, "Other goal", "")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	// An active decision has to name the task it is holding up.
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "blocked-other", []string{"blocked task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision, Question: "Question from another project", RunID: "run-other",
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

func TestPendingCommandUsesLatestProjectRunWithoutEnv(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Resume the claimed work", "")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "latest-run", []string{"unfinished task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterRun(ctx, "run-latest"); err != nil {
		t.Fatalf("RegisterRun: %v", err)
	}
	if err := s.AssociateRunWithProject(ctx, "run-latest", project.ID); err != nil {
		t.Fatalf("AssociateRunWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-latest"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctRunIDEnv, "")

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

func TestPendingCommandPrefersExplicitRunIDOverLatestProjectRun(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Use the selected run", "")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "run-selection", []string{"latest task", "explicit task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterRun(ctx, "run-latest"); err != nil {
		t.Fatalf("RegisterRun latest: %v", err)
	}
	if err := s.AssociateRunWithProject(ctx, "run-latest", project.ID); err != nil {
		t.Fatalf("AssociateRunWithProject latest: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-latest"); err != nil {
		t.Fatalf("ClaimTask latest: %v", err)
	}
	if err := s.RegisterRun(ctx, "run-explicit"); err != nil {
		t.Fatalf("RegisterRun explicit: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[1].ID, "run-explicit"); err != nil {
		t.Fatalf("ClaimTask explicit: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctRunIDEnv, "run-explicit")

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
		t.Fatalf("pendingCommand reported the latest run despite ATCT_RUN_ID override: %q", output)
	}
}

func TestPendingCommandDoesNotReportAnotherRunsClaim(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Do not steal another run's work", "")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "other-run", []string{"other run task"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterRun(ctx, "run-latest"); err != nil {
		t.Fatalf("RegisterRun: %v", err)
	}
	if err := s.AssociateRunWithProject(ctx, "run-latest", project.ID); err != nil {
		t.Fatalf("AssociateRunWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "run-other"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close: %v", err)
	}
	t.Setenv(atctRunIDEnv, "")

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
