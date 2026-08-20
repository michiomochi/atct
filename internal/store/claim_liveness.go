package store

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/michiomochi/atct/internal/domain"
)

// ClaimLiveness separates claimed tasks whose recorded process is still the
// process that owns the claim from claims that can no longer be verified.
func ClaimLiveness(ctx context.Context, s *Store, projectID string) (running []domain.Task, stale []domain.Task, err error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil, fmt.Errorf("project id is required")
	}

	goals, err := s.ListGoals(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	for _, goal := range goals {
		tasks, err := s.ListTasks(ctx, goal.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, task := range tasks {
			if strings.TrimSpace(task.ClaimedBy) == "" {
				continue
			}
			if claimIsRunning(ctx, s, task.ClaimedBy) {
				running = append(running, task)
			} else {
				stale = append(stale, task)
			}
		}
	}
	return running, stale, nil
}

func claimIsRunning(ctx context.Context, s *Store, agentSessionID string) bool {
	var pid int
	var startedAt string
	if err := s.db.QueryRowContext(ctx, `
		SELECT pid, started_at
		FROM agent_sessions
		WHERE id = ?
	`, strings.TrimSpace(agentSessionID)).Scan(&pid, &startedAt); err != nil {
		return false
	}
	if pid == 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}

	actualStartedAt, err := processStartedAt(pid)
	return err == nil && actualStartedAt == startedAt
}
