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
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, Kind: domain.KindDecision, Question: "Which release channel should we use?", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{
		DecisionID: decision.ID, AnswerText: "stable", AnsweredBy: "human",
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
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, Kind: domain.KindDecision, Question: "Question from another project", RunID: "run-other",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{
		DecisionID: decision.ID, AnswerText: "ignore", AnsweredBy: "human",
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
