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

type contextGoal struct {
	Goal  domain.Goal
	Tasks []domain.Task
}

type contextSnapshot struct {
	goals     []contextGoal
	decisions []domain.Decision
}

var errNoContextWork = errors.New("no context work")

// contextText reads the local store directly. The context command is used by
// SessionStart, so it must not start, stop, or upgrade a daemon just to print
// the state that is already persisted in the database.
func contextText(dir, cwd string) (string, error) {
	snapshot, err := loadContextSnapshot(dir, cwd)
	if err != nil {
		return "", err
	}
	if len(snapshot.goals) == 0 {
		return "", nil
	}
	return renderContext(snapshot.goals, snapshot.decisions), nil
}

func loadContextSnapshot(dir, cwd string) (contextSnapshot, error) {
	dbPath := filepath.Join(dir, "atct.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return contextSnapshot{}, nil
		}
		return contextSnapshot{}, fmt.Errorf("stat store: %w", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		return contextSnapshot{}, fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	project, err := s.ResolveProject(ctx, cwd)
	if errors.Is(err, store.ErrProjectNotFound) {
		return contextSnapshot{}, nil
	}
	if err != nil {
		return contextSnapshot{}, fmt.Errorf("resolve project: %w", err)
	}

	goals, err := s.ListGoals(ctx, project.ID)
	if err != nil {
		return contextSnapshot{}, fmt.Errorf("list goals: %w", err)
	}
	active := make([]contextGoal, 0, len(goals))
	activeIDs := make(map[string]bool)
	for _, goal := range goals {
		if goal.Status != domain.GoalActive {
			continue
		}
		tasks, err := s.ListTasks(ctx, goal.ID)
		if err != nil {
			return contextSnapshot{}, fmt.Errorf("list tasks for goal %s: %w", goal.ID, err)
		}
		active = append(active, contextGoal{Goal: goal, Tasks: tasks})
		activeIDs[goal.ID] = true
	}
	if len(active) == 0 {
		return contextSnapshot{}, nil
	}

	decisions, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		return contextSnapshot{}, fmt.Errorf("list unapplied decisions: %w", err)
	}
	filteredDecisions := make([]domain.Decision, 0, len(decisions))
	for _, decision := range decisions {
		if activeIDs[decision.GoalID] && decision.Status == domain.DecisionAnswered && decision.AppliedAt == nil {
			filteredDecisions = append(filteredDecisions, decision)
		}
	}

	return contextSnapshot{goals: active, decisions: filteredDecisions}, nil
}

func contextCommand(dir, cwd string) (string, int, error) {
	snapshot, err := loadContextSnapshot(dir, cwd)
	if err != nil {
		return "", 0, err
	}
	if len(snapshot.goals) == 0 {
		return "", 1, nil
	}

	output := renderContext(snapshot.goals, snapshot.decisions)
	if contextNeedsWakeup(snapshot) {
		return output, 0, nil
	}
	return output, 1, nil
}

func contextNeedsWakeup(snapshot contextSnapshot) bool {
	for _, decision := range snapshot.decisions {
		if decision.Status == domain.DecisionAnswered && decision.AppliedAt == nil {
			return true
		}
	}
	for _, item := range snapshot.goals {
		if len(item.Tasks) == 0 {
			return true
		}
		for _, task := range item.Tasks {
			if task.Status == domain.TaskTodo && strings.TrimSpace(task.ClaimedBy) == "" {
				return true
			}
		}
	}
	return false
}

func runContext(dir string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	output, err := contextText(dir, cwd)
	if err != nil {
		return err
	}
	if output != "" {
		_, err = fmt.Fprint(os.Stdout, output)
	}
	return err
}

func runContextCheck(dir string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	_, exitCode, err := contextCommand(dir, cwd)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return errNoContextWork
	}
	return nil
}

func renderContextLegacy(goals []contextGoal, decisions []domain.Decision) string {
	active := make([]contextGoal, 0, len(goals))
	activeIDs := make(map[string]bool)
	for _, goal := range goals {
		if goal.Goal.Status != domain.GoalActive {
			continue
		}
		active = append(active, goal)
		activeIDs[goal.Goal.ID] = true
	}
	if len(active) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("ATCT context\n")
	declareTasks := false
	claimTasks := false
	for _, item := range active {
		fmt.Fprintf(&b, "Goal: %s\n", oneLine(item.Goal.Title))
		fmt.Fprintf(&b, "Description: %s\n", oneLine(item.Goal.Description))
		fmt.Fprintf(&b, "goal_id: %s\n", oneLine(item.Goal.ID))
		b.WriteString("Tasks:\n")
		listed := 0
		for _, task := range item.Tasks {
			if task.Status != domain.TaskTodo && task.Status != domain.TaskDoing {
				continue
			}
			fmt.Fprintf(&b, "- [%s] %s (task_id: %s)\n", task.Status, oneLine(task.Title), oneLine(task.ID))
			listed++
			if task.Status == domain.TaskTodo {
				claimTasks = true
			}
		}
		switch {
		case len(item.Tasks) == 0:
			b.WriteString("- no tasks declared\n")
			declareTasks = true
		case listed == 0:
			b.WriteString("- no todo or doing tasks\n")
		}
	}

	filteredDecisions := make([]domain.Decision, 0, len(decisions))
	for _, decision := range decisions {
		if !activeIDs[decision.GoalID] || decision.Status != domain.DecisionAnswered || decision.AppliedAt != nil {
			continue
		}
		filteredDecisions = append(filteredDecisions, decision)
	}
	if len(filteredDecisions) > 0 {
		b.WriteString("Unapplied decisions:\n")
		for _, decision := range filteredDecisions {
			fmt.Fprintf(&b, "- %s (decision_id: %s)\n", oneLine(decision.Question), oneLine(decision.ID))
			answer := strings.TrimSpace(strings.Join([]string{decision.AnswerLabel, decision.AnswerText}, " - "))
			if answer != "-" && answer != "" {
				fmt.Fprintf(&b, "  answer: %s\n", oneLine(answer))
			}
		}
	}

	nextTools := make([]string, 0, 3)
	if declareTasks {
		nextTools = append(nextTools, "atct_task_declare")
	}
	if claimTasks {
		nextTools = append(nextTools, "atct_task_claim")
	}
	if len(filteredDecisions) > 0 {
		nextTools = append(nextTools, "atct_decision_poll")
	}
	if len(nextTools) > 0 {
		fmt.Fprintf(&b, "Next tools: %s\n", strings.Join(nextTools, ", "))
	}
	return b.String()
}

func renderContext(goals []contextGoal, decisions []domain.Decision) string {
	const (
		maxGoals = 3
		maxTasks = 5
	)

	active := make([]contextGoal, 0, len(goals))
	activeIDs := make(map[string]struct{}, len(goals))
	for _, item := range goals {
		if item.Goal.Status != domain.GoalActive {
			continue
		}
		active = append(active, item)
		activeIDs[item.Goal.ID] = struct{}{}
	}
	if len(active) == 0 {
		return ""
	}

	actionableTasks := func(tasks []domain.Task) []domain.Task {
		actionable := make([]domain.Task, 0, len(tasks))
		for _, task := range tasks {
			if task.Status == domain.TaskTodo || task.Status == domain.TaskDoing {
				actionable = append(actionable, task)
			}
		}
		return actionable
	}

	hasTodo := false
	hasNoTasks := false
	for _, item := range active {
		if len(item.Tasks) == 0 {
			hasNoTasks = true
		}
		for _, task := range item.Tasks {
			if task.Status == domain.TaskTodo {
				hasTodo = true
			}
		}
	}

	var b strings.Builder
	b.WriteString("ATCT context\n")
	displayed := active
	if len(displayed) > maxGoals {
		displayed = displayed[:maxGoals]
	}
	for _, item := range displayed {
		fmt.Fprintf(&b, "Goal: %s\n", oneLine(item.Goal.Title))
		if description := oneLine(item.Goal.Description); description != "" {
			fmt.Fprintf(&b, "Description: %s\n", description)
		}
		fmt.Fprintf(&b, "goal_id: %s\n", item.Goal.ID)
		b.WriteString("Tasks:\n")

		actionable := actionableTasks(item.Tasks)
		listed := len(actionable)
		if listed > maxTasks {
			listed = maxTasks
		}
		for _, task := range actionable[:listed] {
			status := string(task.Status)
			if strings.TrimSpace(task.ClaimedBy) != "" {
				status = "claimed"
			}
			fmt.Fprintf(&b, "- [%s] %s (task_id: %s)\n", status, oneLine(task.Title), task.ID)
		}
		if len(item.Tasks) == 0 {
			b.WriteString("- no tasks declared\n")
		} else if len(actionable) == 0 {
			b.WriteString("- no todo or doing tasks\n")
		}
		if len(actionable) > maxTasks {
			fmt.Fprintf(&b, "- ... and %d more tasks\n", len(actionable)-maxTasks)
		}
	}
	if omitted := len(active) - len(displayed); omitted > 0 {
		fmt.Fprintf(&b, "... and %d more goals\n", omitted)
	}

	filteredDecisions := make([]domain.Decision, 0, len(decisions))
	for _, decision := range decisions {
		if decision.Status != domain.DecisionAnswered || decision.AppliedAt != nil {
			continue
		}
		if _, ok := activeIDs[decision.GoalID]; !ok {
			continue
		}
		filteredDecisions = append(filteredDecisions, decision)
	}
	if len(filteredDecisions) > 0 {
		b.WriteString("Unapplied decisions:\n")
		for _, decision := range filteredDecisions {
			fmt.Fprintf(&b, "- %s (decision_id: %s)\n", oneLine(decision.Question), decision.ID)
			fmt.Fprintf(&b, "  answer: %s - %s\n", oneLine(decision.AnswerLabel), oneLine(decision.AnswerText))
		}
	}

	nextTools := make([]string, 0, 3)
	if hasNoTasks {
		nextTools = append(nextTools, "atct_task_declare")
	}
	if hasTodo {
		nextTools = append(nextTools, "atct_task_claim")
	}
	if len(filteredDecisions) > 0 {
		nextTools = append(nextTools, "atct_decision_poll")
	}
	if len(nextTools) > 0 {
		fmt.Fprintf(&b, "Next tools: %s\n", strings.Join(nextTools, ", "))
	}
	return b.String()
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
