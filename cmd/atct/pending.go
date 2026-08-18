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
	atctRunIDEnv          = "ATCT_RUN_ID"
	unfinishedClaimMarker = "Unfinished claimed tasks:"
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

	decisions, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		return "", fmt.Errorf("list unapplied decisions: %w", err)
	}

	unfinishedTasks := make([]domain.Task, 0)
	if runID := currentRunID(); runID != "" {
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
	for _, decision := range decisions {
		if decision.Status != domain.DecisionAnswered || decision.AppliedAt != nil {
			continue
		}
		if _, ok := projectGoalIDs[decision.GoalID]; !ok {
			continue
		}
		fmt.Fprintf(&output, "- %s (decision_id: %s)\n", oneLine(decision.Question), decision.ID)
	}
	if len(unfinishedTasks) > 0 {
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(unfinishedClaimMarker)
		output.WriteByte('\n')
		for _, task := range unfinishedTasks {
			fmt.Fprintf(&output, "- %s (task_id: %s)\n", oneLine(task.Title), task.ID)
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
