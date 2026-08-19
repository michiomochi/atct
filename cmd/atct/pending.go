package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

var errNoPendingDecisions = errors.New("no unapplied decisions")

const (
	atctRunIDEnv                 = "ATCT_RUN_ID"
	unfinishedClaimMarker        = "Unfinished claimed tasks:"
	undeclaredGoalMarker         = "Undeclared active goals:"
	pendingDecisionReason        = "A human answered a decision you parked. Call `atct_decision_poll` with each decision_id below, then continue the work that was waiting on it."
	pendingDefaultDecisionReason = "No one answered a decision you parked, so its default was applied. Call `atct_decision_poll` with each decision_id below, then continue the work that was waiting on it."
	pendingClaimReason           = "A task claimed by this run is still open. If you forgot to close it, close it; if you are still working on it, continue."
	pendingUndeclaredGoalReason  = "An active goal has no tasks declared. Call `atct_task_declare` for each goal below, then continue the work."
)

func currentRunID() string {
	return strings.TrimSpace(os.Getenv(atctRunIDEnv))
}

func pendingCommand(dir, cwd string) (string, int, error) {
	return pendingCommandForProject(dir, cwd, "", false)
}

func pendingCommandForProject(dir, cwd, projectName string, projectSpecified bool) (string, int, error) {
	output, err := pendingTextForProject(dir, cwd, projectName, projectSpecified)
	if err != nil {
		return "", 0, err
	}
	if output == "" {
		return "", 1, nil
	}
	return output, 0, nil
}

func pendingText(dir, cwd string) (string, error) {
	return pendingTextForProject(dir, cwd, "", false)
}

func pendingTextForProject(dir, cwd, projectName string, projectSpecified bool) (string, error) {
	dbPath := filepath.Join(dir, "atct.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if projectSpecified {
				return "", fmt.Errorf("project %q not found", projectName)
			}
			return "", nil
		}
		return "", fmt.Errorf("stat store: %w", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	project, err := resolveProjectSelection(ctx, s, cwd, projectName, projectSpecified)
	if errors.Is(err, store.ErrProjectNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve project: %w", err)
	}

	goals, err := s.ListGoals(ctx, project.ID)
	if err != nil {
		return "", fmt.Errorf("list goals: %w", err)
	}
	projectGoalIDs := make(map[string]struct{}, len(goals))
	for _, goal := range goals {
		projectGoalIDs[goal.ID] = struct{}{}
	}
	undeclaredGoals := make([]domain.Goal, 0)
	for _, goal := range goals {
		if goal.Status != domain.GoalActive {
			continue
		}
		tasks, err := s.ListTasks(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list tasks for goal %s: %w", goal.ID, err)
		}
		if len(tasks) == 0 {
			undeclaredGoals = append(undeclaredGoals, goal)
		}
	}

	decisions, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		return "", fmt.Errorf("list unapplied decisions: %w", err)
	}

	unfinishedTasks := make([]domain.Task, 0)
	runID := currentRunID()
	if runID == "" {
		runID, err = s.LatestRunID(ctx, project.ID)
		if err != nil {
			return "", fmt.Errorf("find latest run: %w", err)
		}
	}
	if runID != "" {
		for _, goal := range goals {
			if goal.Status != domain.GoalActive {
				continue
			}
			tasks, err := s.ListOpenTasksClaimedBy(ctx, goal.ID, runID)
			if err != nil {
				return "", fmt.Errorf("list claimed tasks for goal %s: %w", goal.ID, err)
			}
			unfinishedTasks = append(unfinishedTasks, tasks...)
		}
	}

	var output strings.Builder
	humanAnsweredDecisions := make([]domain.Decision, 0)
	defaultAppliedDecisions := make([]domain.Decision, 0)
	for _, decision := range decisions {
		if decision.Status != domain.DecisionAnswered || decision.AppliedAt != nil {
			continue
		}
		if _, ok := projectGoalIDs[decision.GoalID]; !ok {
			continue
		}
		if decision.DefaultAppliedAt != nil {
			defaultAppliedDecisions = append(defaultAppliedDecisions, decision)
			continue
		}
		humanAnsweredDecisions = append(humanAnsweredDecisions, decision)
	}
	writeDecisionSection := func(reason string, section []domain.Decision) {
		if len(section) == 0 {
			return
		}
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(reason)
		output.WriteString("\n\n")
		for _, decision := range section {
			fmt.Fprintf(&output, "- %s (decision_id: %s)\n", oneLine(decision.Question), decision.ID)
		}
	}
	writeDecisionSection(pendingDecisionReason, humanAnsweredDecisions)
	writeDecisionSection(pendingDefaultDecisionReason, defaultAppliedDecisions)
	if len(unfinishedTasks) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingClaimReason)
		output.WriteString("\n\n")
		output.WriteString(unfinishedClaimMarker)
		output.WriteByte('\n')
		for _, task := range unfinishedTasks {
			fmt.Fprintf(&output, "- %s (task_id: %s)\n", oneLine(task.Title), task.ID)
		}
	}
	if len(undeclaredGoals) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingUndeclaredGoalReason)
		output.WriteString("\n\n")
		output.WriteString(undeclaredGoalMarker)
		output.WriteByte('\n')
		for _, goal := range undeclaredGoals {
			fmt.Fprintf(&output, "- %s (goal_id: %s)\n", oneLine(goal.Title), goal.ID)
		}
	}
	return output.String(), nil
}

func runPending(dir string) error {
	return runPendingForProject(dir, "", false)
}

func runPendingForProject(dir, projectName string, projectSpecified bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	output, exitCode, err := pendingCommandForProject(dir, cwd, projectName, projectSpecified)
	if err != nil {
		return err
	}
	if output != "" {
		if _, err := fmt.Fprint(os.Stdout, output); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return errNoPendingDecisions
	}
	return nil
}
