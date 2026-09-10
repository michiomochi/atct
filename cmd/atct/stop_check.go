package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

type stopCheckScope struct {
	Role      string
	ProjectID int64
	GoalID    int64
	TaskID    int64
}

type stopCheckResponse struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func stopCheckScopeFromConfig(config cliConfig) stopCheckScope {
	projectID, _ := strconv.ParseInt(config.stopCheckProjectID, 10, 64)
	goalID, _ := strconv.ParseInt(config.stopCheckGoalID, 10, 64)
	taskID, _ := strconv.ParseInt(config.stopCheckTaskID, 10, 64)
	return stopCheckScope{Role: config.stopCheckRole, ProjectID: projectID, GoalID: goalID, TaskID: taskID}
}

func validateStopCheckScope(scope stopCheckScope) error {
	return scope.validate()
}

func runStopCheck(config cliConfig, dir string) error {
	_, err := fmt.Fprint(os.Stdout, stopCheckResult(dir, stopCheckScopeFromConfig(config)))
	return err
}

func stopCheckResult(dir string, scope stopCheckScope) string {
	output, err := stopCheckText(dir, scope)
	if err == nil {
		return output
	}
	encoded, marshalErr := json.Marshal(stopCheckResponse{Decision: "block", Reason: "ATCT stop-check failed: " + err.Error()})
	if marshalErr != nil {
		return `{"decision":"block","reason":"ATCT stop-check failed"}`
	}
	return string(encoded)
}

func (s stopCheckScope) validate() error {
	if s.ProjectID <= 0 {
		return fmt.Errorf("stop-check requires a project ID")
	}
	switch s.Role {
	case "commander":
		if s.GoalID != 0 || s.TaskID != 0 {
			return fmt.Errorf("commander stop-check does not accept goal or task")
		}
	case "subcommander":
		if s.GoalID <= 0 || s.TaskID != 0 {
			return fmt.Errorf("subcommander stop-check requires a goal and no task")
		}
	case "executor":
		if s.GoalID <= 0 || s.TaskID <= 0 {
			return fmt.Errorf("executor stop-check requires a goal and task")
		}
	default:
		return fmt.Errorf("invalid stop-check role %q", s.Role)
	}
	return nil
}

func stopCheckText(dir string, scope stopCheckScope) (string, error) {
	if err := scope.validate(); err != nil {
		return "", err
	}
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		return "", fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	if scope.Role == "commander" {
		return stopCheckCommander(ctx, s, scope)
	}
	goal, err := s.GetGoal(ctx, scope.GoalID)
	if err != nil {
		return "", fmt.Errorf("get scoped goal: %w", err)
	}
	if goal.ProjectID != scope.ProjectID {
		return "", fmt.Errorf("scoped goal %d does not belong to project %d", scope.GoalID, scope.ProjectID)
	}
	if scope.Role == "subcommander" {
		return stopCheckSubcommander(ctx, s, scope)
	}
	return stopCheckExecutor(ctx, s, scope)
}

func stopCheckCommander(ctx context.Context, s *store.Store, scope stopCheckScope) (string, error) {
	goals, err := s.ListGoals(ctx, scope.ProjectID)
	if err != nil {
		return "", fmt.Errorf("list scoped project goals: %w", err)
	}
	for _, goal := range goals {
		if goal.Status == domain.GoalActive {
			return stopCheckBlock(fmt.Sprintf("commander has active goal %d: %s", goal.ID, domain.Headline(goal.Content)))
		}
	}
	return "", nil
}

func stopCheckSubcommander(ctx context.Context, s *store.Store, scope stopCheckScope) (string, error) {
	goalHandoffs, err := s.ListGoalHandoffs(ctx, scope.GoalID)
	if err != nil {
		return "", fmt.Errorf("list scoped goal handoffs: %w", err)
	}
	for _, handoff := range goalHandoffs {
		if handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
			return stopCheckBlock(fmt.Sprintf("subcommander has open goal handoff %s", handoff.ID))
		}
	}
	planHandoffs, err := s.ListPlanHandoffs(ctx, scope.GoalID)
	if err != nil {
		return "", fmt.Errorf("list scoped plan handoffs: %w", err)
	}
	for _, handoff := range planHandoffs {
		if handoff.CompletedReportAt == nil {
			return stopCheckBlock(fmt.Sprintf("subcommander has open plan handoff %s", handoff.ID))
		}
	}
	createHandoffs, err := s.ListTaskCreateHandoffs(ctx, scope.GoalID)
	if err != nil {
		return "", fmt.Errorf("list scoped task-create handoffs: %w", err)
	}
	for _, handoff := range createHandoffs {
		if handoff.CompletedAt == nil && handoff.RecoveredAt == nil {
			return stopCheckBlock(fmt.Sprintf("subcommander has open task-create handoff %s", handoff.ID))
		}
	}
	return "", nil
}

func stopCheckExecutor(ctx context.Context, s *store.Store, scope stopCheckScope) (string, error) {
	goalID, err := s.GetTaskGoalID(ctx, scope.TaskID)
	if err != nil {
		return "", fmt.Errorf("get scoped task goal: %w", err)
	}
	if goalID != scope.GoalID {
		return "", fmt.Errorf("scoped task %d does not belong to goal %d", scope.TaskID, scope.GoalID)
	}
	handoffs, err := s.ListTaskHandoffs(ctx, scope.TaskID)
	if err != nil {
		return "", fmt.Errorf("list scoped task handoffs: %w", err)
	}
	for _, handoff := range handoffs {
		if handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
			return stopCheckBlock(fmt.Sprintf("executor has open task handoff %s for task %d", handoff.ID, scope.TaskID))
		}
	}
	return "", nil
}

func stopCheckBlock(detail string) (string, error) {
	response, err := json.Marshal(stopCheckResponse{Decision: "block", Reason: "ATCT work remains: " + detail})
	if err != nil {
		return "", fmt.Errorf("marshal stop response: %w", err)
	}
	return string(response), nil
}
