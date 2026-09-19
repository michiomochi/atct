package daemon

import (
	"context"
	"fmt"

	"github.com/michiomochi/atct/internal/domain"
)

type stopCheckResponse struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func (d *Daemon) stopCheck(ctx context.Context, sessionKey string) (stopCheckResponse, error) {
	agentSessionID, err := d.store.AgentSessionIDByKey(ctx, sessionKey)
	if err != nil {
		return stopCheckResponse{}, fmt.Errorf("resolve session key: %w", err)
	}
	role, err := d.deriveSessionRole(ctx, agentSessionID)
	if err != nil {
		return stopCheckResponse{}, fmt.Errorf("derive session role: %w", err)
	}

	var detail string
	switch role.Role {
	case "commander":
		detail, err = d.stopCheckCommander(ctx, role.ProjectID)
	case "subcommander":
		detail, err = d.stopCheckSubcommander(ctx, agentSessionID, role.GoalID)
	default:
		detail, err = d.stopCheckExecutor(ctx, agentSessionID)
	}
	if err != nil {
		return stopCheckResponse{}, err
	}
	if detail == "" {
		return stopCheckResponse{}, nil
	}
	return stopCheckResponse{Decision: "block", Reason: "ATCT work remains: " + detail}, nil
}

func (d *Daemon) stopCheckCommander(ctx context.Context, projectID int64) (string, error) {
	goals, err := d.store.ListGoals(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("list project goals: %w", err)
	}
	for _, goal := range goals {
		if goal.Status == domain.GoalActive {
			return fmt.Sprintf("commander has active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
		}
	}
	return "", nil
}

func (d *Daemon) stopCheckSubcommander(ctx context.Context, agentSessionID, goalID int64) (string, error) {
	goalHandoffs, err := d.store.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list goal handoffs: %w", err)
	}
	for _, handoff := range goalHandoffs {
		if handoff.ReceivedBy == agentSessionID && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
			return fmt.Sprintf("subcommander has open goal handoff %s", handoff.ID), nil
		}
	}
	planHandoffs, err := d.store.ListPlanHandoffs(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list plan handoffs: %w", err)
	}
	for _, handoff := range planHandoffs {
		if handoff.ReviewRejectionReceivedBy == agentSessionID && handoff.CompletedReportAt == nil {
			return fmt.Sprintf("subcommander has rejected plan handoff %s", handoff.ID), nil
		}
	}
	createHandoffs, err := d.store.ListTaskCreateHandoffs(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list task-create handoffs: %w", err)
	}
	for _, handoff := range createHandoffs {
		if handoff.ReceivedBy == agentSessionID && handoff.CompletedAt == nil && handoff.RecoveredAt == nil {
			return fmt.Sprintf("subcommander has open task-create handoff %s", handoff.ID), nil
		}
	}
	tasks, err := d.store.ListTasks(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list goal tasks: %w", err)
	}
	for _, task := range tasks {
		handoffs, err := d.store.ListTaskHandoffs(ctx, task.ID)
		if err != nil {
			return "", fmt.Errorf("list task handoffs for task %d: %w", task.ID, err)
		}
		for _, handoff := range handoffs {
			if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				return fmt.Sprintf("subcommander has task review handoff %s for task %d", handoff.ID, task.ID), nil
			}
		}
	}
	return "", nil
}

func (d *Daemon) stopCheckExecutor(ctx context.Context, agentSessionID int64) (string, error) {
	goals, err := d.store.ListAllGoals(ctx)
	if err != nil {
		return "", fmt.Errorf("list goals: %w", err)
	}
	for _, goal := range goals {
		tasks, err := d.store.ListTasks(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list goal tasks: %w", err)
		}
		for _, task := range tasks {
			handoffs, err := d.store.ListTaskHandoffs(ctx, task.ID)
			if err != nil {
				return "", fmt.Errorf("list task handoffs for task %d: %w", task.ID, err)
			}
			for _, handoff := range handoffs {
				if handoff.ReceivedBy == agentSessionID && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
					return fmt.Sprintf("executor has open task handoff %s for task %d", handoff.ID, task.ID), nil
				}
			}
		}
	}
	return "", nil
}
