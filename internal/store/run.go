package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

var (
	ErrAgentSessionNotRegistered = errors.New("agent session is not registered")
	ErrAgentSessionNotAssociated = errors.New("agent session is not associated with a project")
)

// ProjectIDForAgentSession returns the project assigned to a registered agent session.
func (s *Store) ProjectIDForAgentSession(ctx context.Context, agentSessionID int64) (int64, error) {
	agentSessionID, err := requireAgentSessionID(agentSessionID)
	if err != nil {
		return 0, err
	}

	var projectID sql.NullInt64
	projectID, err = sqlcgen.New(s.db).GetAgentSessionProjectID(ctx, agentSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("agent session %d is not registered: %w", agentSessionID, ErrAgentSessionNotRegistered)
	}
	if err != nil {
		return 0, fmt.Errorf("find project for agent session %d: %w", agentSessionID, err)
	}
	if !projectID.Valid || projectID.Int64 == 0 {
		return 0, fmt.Errorf("agent session %d is not associated with a project: %w", agentSessionID, ErrAgentSessionNotAssociated)
	}
	return projectID.Int64, nil
}

// ProjectIDForTask returns the project containing a task.
func (s *Store) ProjectIDForTask(ctx context.Context, taskID int64) (int64, error) {
	var (
		projectID int64
		err       error
	)
	projectID, err = sqlcgen.New(s.db).GetTaskProjectID(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("task %d is not found", taskID)
	}
	if err != nil {
		return 0, fmt.Errorf("find project for task %d: %w", taskID, err)
	}
	if projectID == 0 {
		return 0, fmt.Errorf("task %d is not associated with a project", taskID)
	}
	return projectID, nil
}
