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

func pendingCommand(dir, cwd string) (string, int, error) {
	output, err := pendingText(dir, cwd)
	if err != nil {
		return "", 0, err
	}
	if output == "" {
		return "", 1, nil
	}
	return output, 0, nil
}

func pendingText(dir, cwd string) (string, error) {
	dbPath := filepath.Join(dir, "atct.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
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
	project, err := s.ResolveProject(ctx, cwd)
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
	return output.String(), nil
}

func runPending(dir string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	output, exitCode, err := pendingCommand(dir, cwd)
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
