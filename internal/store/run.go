package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrRunNotRegistered = errors.New("run is not registered")
	ErrRunNotAssociated = errors.New("run is not associated with a project")
)

// ProjectIDForRun returns the project assigned to a registered run.
func (s *Store) ProjectIDForRun(ctx context.Context, runID string) (string, error) {
	runID, err := requireRunID(runID)
	if err != nil {
		return "", err
	}

	var projectID sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT project_id FROM runs WHERE id = ?`, runID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("run %q is not registered: %w", runID, ErrRunNotRegistered)
	}
	if err != nil {
		return "", fmt.Errorf("find project for run %q: %w", runID, err)
	}
	if !projectID.Valid || strings.TrimSpace(projectID.String) == "" {
		return "", fmt.Errorf("run %q is not associated with a project: %w", runID, ErrRunNotAssociated)
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
