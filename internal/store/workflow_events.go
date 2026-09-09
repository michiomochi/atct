package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// WorkflowEventQuery scopes canonical reconciliation to one project and
// optionally one goal or task.
type WorkflowEventQuery struct {
	ProjectID int64
	GoalID    int64
	TaskID    int64
}

// WorkflowReconciliation is a point-in-time canonical snapshot. It contains
// the scoped state needed after a watcher restarts or a live signal is missed.
type WorkflowReconciliation struct {
	Goals              []domain.Goal       `json:"goals"`
	Tasks              []domain.Task       `json:"tasks"`
	Decisions          []WorkflowDecision  `json:"decisions"`
	GoalHandoffs       []GoalHandoff       `json:"goal_handoffs"`
	PlanHandoffs       []PlanHandoff       `json:"plan_handoffs"`
	TaskHandoffs       []TaskHandoff       `json:"task_handoffs"`
	TaskCreateHandoffs []TaskCreateHandoff `json:"task_create_handoffs"`
}

// WorkflowDecision carries the live delivery role of the session that owns a Decision.
type WorkflowDecision struct {
	domain.Decision
	TargetRole string `json:"target_role"`
}

func (s *Store) decisionTargetRole(ctx context.Context, agentSessionID int64) (string, error) {
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	for _, project := range projects {
		if agentSessionID != 0 && project.ClaimedBy == agentSessionID {
			return "commander", nil
		}
	}
	goals, err := s.ListAllGoals(ctx)
	if err != nil {
		return "", err
	}
	handoffs, err := s.ListOpenGoalHandoffs(ctx)
	if err != nil {
		return "", err
	}
	for _, goal := range goals {
		if handoff := handoffs[goal.ID]; handoff != nil && handoff.ReceivedAt != nil && handoff.ReceivedBy == agentSessionID {
			return "subcommander", nil
		}
	}
	return "executor", nil
}

func workflowDecisionEvent(name string, row sqlcgen.Decision) (DecisionEvent, error) {
	decision, err := decisionFromRow(decisionRowFromSQLC(row))
	if err != nil {
		return DecisionEvent{}, err
	}
	return DecisionEvent{Name: name, Data: decision, OccurredAt: decision.CreatedAt}, nil
}

func (s *Store) publishWorkflowEvents(events []DecisionEvent) {
	for _, event := range events {
		if event.Name != "" {
			s.notify.publishEvent(event)
		}
	}
}

func (s *Store) ReconcileWorkflow(ctx context.Context, query WorkflowEventQuery) (WorkflowReconciliation, error) {
	projectID := query.ProjectID
	if query.GoalID != 0 {
		goal, err := s.GetGoal(ctx, query.GoalID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		if projectID == 0 {
			projectID = goal.ProjectID
		} else if goal.ProjectID != projectID {
			return WorkflowReconciliation{}, fmt.Errorf("goal %d does not belong to project %d", query.GoalID, projectID)
		}
	}
	if projectID <= 0 {
		return WorkflowReconciliation{}, errors.New("workflow reconciliation project_id is required")
	}
	if query.TaskID != 0 {
		taskGoalID, err := sqlcgen.New(s.db).GetTaskGoalID(ctx, query.TaskID)
		if err != nil {
			return WorkflowReconciliation{}, fmt.Errorf("find task for workflow reconciliation: %w", err)
		}
		if query.GoalID == 0 {
			query.GoalID = taskGoalID
		} else if query.GoalID != taskGoalID {
			return WorkflowReconciliation{}, fmt.Errorf("task %d does not belong to goal %d", query.TaskID, query.GoalID)
		}
	}
	goals, err := s.ListGoals(ctx, projectID)
	if err != nil {
		return WorkflowReconciliation{}, err
	}
	if query.GoalID != 0 {
		filtered := goals[:0]
		for _, goal := range goals {
			if goal.ID == query.GoalID {
				filtered = append(filtered, goal)
			}
		}
		goals = filtered
	}
	if query.TaskID != 0 && len(goals) == 0 {
		return WorkflowReconciliation{}, fmt.Errorf("task %d is outside project %d", query.TaskID, projectID)
	}
	reconciliation := WorkflowReconciliation{
		Goals:              append([]domain.Goal(nil), goals...),
		Decisions:          make([]WorkflowDecision, 0),
		GoalHandoffs:       make([]GoalHandoff, 0),
		PlanHandoffs:       make([]PlanHandoff, 0),
		TaskHandoffs:       make([]TaskHandoff, 0),
		TaskCreateHandoffs: make([]TaskCreateHandoff, 0),
		Tasks:              make([]domain.Task, 0),
	}
	for _, goal := range goals {
		tasks, err := s.ListTasks(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		for _, task := range tasks {
			if query.TaskID == 0 || task.ID == query.TaskID {
				reconciliation.Tasks = append(reconciliation.Tasks, task)
				handoffs, err := s.ListTaskHandoffs(ctx, task.ID)
				if err != nil {
					return WorkflowReconciliation{}, err
				}
				for _, handoff := range handoffs {
					reconciliation.TaskHandoffs = append(reconciliation.TaskHandoffs, handoff)
				}
			}
		}
		goalHandoffs, err := s.ListGoalHandoffs(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		reconciliation.GoalHandoffs = append(reconciliation.GoalHandoffs, goalHandoffs...)
		planHandoffs, err := s.ListPlanHandoffs(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		reconciliation.PlanHandoffs = append(reconciliation.PlanHandoffs, planHandoffs...)
		taskCreateHandoffs, err := s.ListTaskCreateHandoffs(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		reconciliation.TaskCreateHandoffs = append(reconciliation.TaskCreateHandoffs, taskCreateHandoffs...)
		decisions, err := s.ListDecisionsForGoal(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		for _, decision := range decisions {
			if query.TaskID == 0 || decision.TaskID == query.TaskID {
				targetRole, err := s.decisionTargetRole(ctx, decision.AgentSessionID)
				if err != nil {
					return WorkflowReconciliation{}, err
				}
				reconciliation.Decisions = append(reconciliation.Decisions, WorkflowDecision{Decision: decision, TargetRole: targetRole})
			}
		}
	}
	return reconciliation, nil
}

func (query WorkflowEventQuery) ProjectIDOr(fallback int64) int64 {
	if query.ProjectID != 0 {
		return query.ProjectID
	}
	return fallback
}

func (s *Store) ReconcileWorkflowEvents(ctx context.Context, query WorkflowEventQuery) (WorkflowReconciliation, error) {
	return s.ReconcileWorkflow(ctx, query)
}
