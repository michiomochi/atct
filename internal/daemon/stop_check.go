package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
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
		if goal.Status != domain.GoalActive {
			continue
		}

		goalHandoffs, err := d.store.ListGoalHandoffs(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list goal handoffs for goal %d: %w", goal.ID, err)
		}
		planHandoffs, err := d.store.ListPlanHandoffs(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list plan handoffs for goal %d: %w", goal.ID, err)
		}
		decisions, err := d.store.ListDecisionsForGoal(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list decisions for goal %d: %w", goal.ID, err)
		}

		var latestGoalReview domain.Decision
		hasGoalReview := false
		hasOpenDecision := false
		hasApprovedGoalReview := false
		for _, decision := range decisions {
			if decision.Status == domain.DecisionOpen {
				hasOpenDecision = true
			}
			if decision.Kind == domain.KindGoalReview {
				if decision.Status == domain.DecisionApplied && decision.AnswerLabel == "approve" {
					hasApprovedGoalReview = true
				}
				latestGoalReview = decision
				hasGoalReview = true
			}
		}
		if hasApprovedGoalReview {
			return fmt.Sprintf("commander has approved goal review for active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
		}
		if hasGoalReview && latestGoalReview.Status == domain.DecisionAnswered && latestGoalReview.AnswerLabel == "reject" {
			for _, handoff := range goalHandoffs {
				if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
					return fmt.Sprintf("commander must return rejected goal review for active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
				}
			}
		}
		for _, handoff := range goalHandoffs {
			if handoff.RequestedAt != nil && handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt == nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				return fmt.Sprintf("commander has unreceived goal review for active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
			}
		}
		for _, handoff := range planHandoffs {
			if handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt == nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil {
				return fmt.Sprintf("commander has unreceived plan review for active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
			}
		}
		if hasOpenDecision {
			continue
		}

		tasks, err := d.store.ListTasks(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list tasks for goal %d: %w", goal.ID, err)
		}
		hasExecutorWork := false
		for _, task := range tasks {
			handoffs, err := d.store.ListTaskHandoffs(ctx, task.ID)
			if err != nil {
				return "", fmt.Errorf("list task handoffs for task %d: %w", task.ID, err)
			}
			for _, handoff := range handoffs {
				if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
					hasExecutorWork = true
					break
				}
			}
			if hasExecutorWork {
				break
			}
		}
		if hasExecutorWork {
			continue
		}

		hasDelegatedGoalHandoff := false
		for _, handoff := range goalHandoffs {
			if handoff.RequestedAt != nil && handoff.RequestedBy != handoff.ReceivedBy && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				hasDelegatedGoalHandoff = true
				break
			}
		}
		if !hasDelegatedGoalHandoff {
			return fmt.Sprintf("commander has active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
		}
	}
	return "", nil
}

func (d *Daemon) stopCheckSubcommander(ctx context.Context, agentSessionID, goalID int64) (string, error) {
	goal, err := d.store.GetGoal(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("get goal: %w", err)
	}
	if err := d.store.EnsureProjectActive(ctx, goal.ProjectID); errors.Is(err, store.ErrProjectArchived) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	goalHandoffs, err := d.store.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list goal handoffs: %w", err)
	}
	waiting := d.subcommanderWaiting(ctx, agentSessionID, goal, goalHandoffs)
	for _, handoff := range goalHandoffs {
		if handoff.ReceivedBy == agentSessionID && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
			if waiting {
				continue
			}
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
			if handoff.RequestedAt != nil && handoff.ReceivedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				return fmt.Sprintf("subcommander has unreceived task handoff %s for task %d", handoff.ID, task.ID), nil
			}
			if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				return fmt.Sprintf("subcommander has task review handoff %s for task %d", handoff.ID, task.ID), nil
			}
		}
	}
	return "", nil
}

// subcommanderWaiting reports whether the only thing a subcommander holds is
// an answer it is waiting for: a live Monitor on its goal scope, and either a
// plan review or a goal review the commander has not answered, or a task an
// executor is working on. The answer arrives through the Monitor, so stopping
// costs nothing. When in doubt (no Monitor, a lookup error) it returns false
// and the caller keeps blocking.
func (d *Daemon) subcommanderWaiting(ctx context.Context, agentSessionID int64, goal domain.Goal, goalHandoffs []store.GoalHandoff) bool {
	goalID := goal.ID
	live, err := d.store.HasLiveMonitorForScope(ctx, store.MonitorLiveScope{ProjectID: goal.ProjectID, Role: "subcommander", GoalID: &goalID})
	if err != nil || !live {
		return false
	}
	for _, handoff := range goalHandoffs {
		if handoff.ReceivedBy == agentSessionID && handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
			return true
		}
	}
	planHandoffs, err := d.store.ListPlanHandoffs(ctx, goalID)
	if err != nil {
		return false
	}
	for _, handoff := range planHandoffs {
		if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil {
			return true
		}
	}
	tasks, err := d.store.ListTasks(ctx, goalID)
	if err != nil {
		return false
	}
	for _, task := range tasks {
		handoffs, err := d.store.ListTaskHandoffs(ctx, task.ID)
		if err != nil {
			return false
		}
		for _, handoff := range handoffs {
			if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil &&
				(handoff.ReviewRequestedAt == nil || handoff.ReviewRejectedAt != nil) {
				return true
			}
		}
	}
	return false
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
					// Review requested and not rejected: the subcommander's answer
					// arrives through the Monitor, so a live one of this task's means waiting only.
					if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil {
						if live, err := d.store.HasLiveExecutorMonitorForTask(ctx, goal.ProjectID, goal.ID, task.ID); err == nil && live {
							continue
						}
					}
					return fmt.Sprintf("executor has open task handoff %s for task %d", handoff.ID, task.ID), nil
				}
			}
		}
	}
	return "", nil
}
