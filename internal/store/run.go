package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrAgentSessionNotRegistered = errors.New("agent session is not registered")
	ErrAgentSessionNotAssociated = errors.New("agent session is not associated with a project")
)

// ProjectIDForAgentSession returns the project assigned to a registered agent session.
func (s *Store) ProjectIDForAgentSession(ctx context.Context, agentSessionID string) (string, error) {
	agentSessionID, err := requireAgentSessionID(agentSessionID)
	if err != nil {
		return "", err
	}

	var projectID sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT project_id FROM agent_sessions WHERE id = ?`, agentSessionID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("agent session %q is not registered: %w", agentSessionID, ErrAgentSessionNotRegistered)
	}
	if err != nil {
		return "", fmt.Errorf("find project for agent session %q: %w", agentSessionID, err)
	}
	if !projectID.Valid || strings.TrimSpace(projectID.String) == "" {
		return "", fmt.Errorf("agent session %q is not associated with a project: %w", agentSessionID, ErrAgentSessionNotAssociated)
	}
	return projectID.String, nil
}

// ProjectIDForTask returns the project containing a task.
func (s *Store) ProjectIDForTask(ctx context.Context, taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	var projectID string
	err := s.db.QueryRowContext(ctx, `
		SELECT g.project_id
		FROM tasks AS t
		JOIN goals AS g ON g.id = t.goal_id
		WHERE t.id = ?
	`, taskID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("task %q is not found", taskID)
	}
	if err != nil {
		return "", fmt.Errorf("find project for task %q: %w", taskID, err)
	}
	if strings.TrimSpace(projectID) == "" {
		return "", fmt.Errorf("task %q is not associated with a project", taskID)
	}
	return projectID, nil
}
