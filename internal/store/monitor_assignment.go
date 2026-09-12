package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

var ErrMonitorBindingNotFound = errors.New("monitor binding not found")

// MonitorScope is one event-delivery boundary derived from a canonical session.
type MonitorScope struct {
	ProjectID int64 `json:"project_id"`
	GoalID    int64 `json:"goal_id,omitempty"`
	TaskID    int64 `json:"task_id,omitempty"`
}

// MonitorAssignment is the server-derived delivery assignment for one session.
type MonitorAssignment struct {
	Role      string         `json:"role"`
	ProjectID int64          `json:"project_id,omitempty"`
	GoalID    int64          `json:"goal_id,omitempty"`
	Tasks     []MonitorScope `json:"tasks,omitempty"`
}

// MonitorBinding is a monitor's current server-derived delivery assignment.
type MonitorBinding struct {
	Pending    bool              `json:"pending"`
	Assignment MonitorAssignment `json:"assignment"`
}

// BindMonitorToken records the canonical session that owns a monitor token.
func (s *Store) BindMonitorToken(ctx context.Context, token string, agentSessionID int64) error {
	token = strings.TrimSpace(token)
	if token == "" || agentSessionID <= 0 {
		return fmt.Errorf("monitor token and agent session id are required")
	}
	err := sqlcgen.New(s.db).BindMonitorToken(ctx, sqlcgen.BindMonitorTokenParams{
		Token:          token,
		AgentSessionID: agentSessionID,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("bind monitor token: %w", err)
	}
	return nil
}

// MonitorBinding derives the assignment from the token's canonical session.
func (s *Store) MonitorBinding(ctx context.Context, token string) (MonitorBinding, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return MonitorBinding{}, fmt.Errorf("monitor token is required")
	}
	agentSessionID, err := sqlcgen.New(s.db).GetMonitorBindingAgentSessionID(ctx, token)
	if err != nil {
		if err == sql.ErrNoRows {
			return MonitorBinding{}, ErrMonitorBindingNotFound
		}
		return MonitorBinding{}, fmt.Errorf("find monitor binding: %w", err)
	}
	assignment, err := s.MonitorAssignment(ctx, agentSessionID)
	if err != nil {
		return MonitorBinding{}, fmt.Errorf("derive monitor assignment: %w", err)
	}
	return MonitorBinding{Assignment: assignment}, nil
}

// MonitorAssignment derives the same role precedence used for authorization.
// An executor retains every received, incomplete task handoff as a scope.
func (s *Store) MonitorAssignment(ctx context.Context, agentSessionID int64) (MonitorAssignment, error) {
	assignment := MonitorAssignment{Role: "executor"}
	if agentSessionID == 0 {
		return assignment, nil
	}
	queries := sqlcgen.New(s.db)
	projectID, err := queries.GetMonitorCommanderProjectID(ctx, agentSessionID)
	if err == nil {
		return MonitorAssignment{Role: "commander", ProjectID: projectID}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MonitorAssignment{}, fmt.Errorf("find commander assignment: %w", err)
	}
	sessionID := sql.NullInt64{Int64: agentSessionID, Valid: true}
	subcommander, err := queries.GetMonitorSubcommanderAssignment(ctx, sessionID)
	if err == nil {
		return MonitorAssignment{Role: "subcommander", ProjectID: subcommander.ProjectID, GoalID: subcommander.GoalID}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MonitorAssignment{}, fmt.Errorf("find subcommander assignment: %w", err)
	}
	tasks, err := queries.ListMonitorExecutorAssignments(ctx, sessionID)
	if err != nil {
		return MonitorAssignment{}, fmt.Errorf("find executor assignments: %w", err)
	}
	for _, task := range tasks {
		assignment.Tasks = append(assignment.Tasks, MonitorScope{ProjectID: task.ProjectID, GoalID: task.GoalID, TaskID: task.TaskID})
	}
	if len(assignment.Tasks) > 0 {
		assignment.ProjectID = assignment.Tasks[0].ProjectID
	}
	return assignment, nil
}
