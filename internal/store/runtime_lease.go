package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// RuntimeLeaseDuration is how long a session stays live on one heartbeat. The
// monitors renew well inside it, so a lease only lapses when the process that
// was renewing it is gone.
const RuntimeLeaseDuration = 30 * time.Second

// HeartbeatAgentSession renews one session's lease. The caller supplies the
// clock so tests and the daemon share the same predicate.
func (s *Store) HeartbeatAgentSession(ctx context.Context, agentSessionID int64, now time.Time) error {
	agentSessionID, err := requireAgentSessionID(agentSessionID)
	if err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}
	result, err := sqlcgen.New(s.db).HeartbeatAgentSession(ctx, sqlcgen.HeartbeatAgentSessionParams{
		LastHeartbeatAt: sql.NullString{String: now.UTC().Format(time.RFC3339Nano), Valid: true},
		ID:              agentSessionID,
	})
	if err != nil {
		return fmt.Errorf("heartbeat agent session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check heartbeat agent session: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("agent session %d is not registered: %w", agentSessionID, ErrAgentSessionNotRegistered)
	}
	return nil
}

// AgentSessionLive answers from the lease alone.
//
// It deliberately ignores the recorded pid. A session used to have a process
// of its own, so asking the operating system about that pid answered the
// question. Over the HTTP transport the daemon serves every session and the
// recorded pid is the daemon's: alive as long as ATCT is running, which made
// every session look live forever and left dead sessions holding their claims.
func (s *Store) AgentSessionLive(ctx context.Context, agentSessionID int64, now time.Time) bool {
	if agentSessionID <= 0 {
		return false
	}
	return agentSessionLiveInQueries(ctx, sqlcgen.New(s.db), agentSessionID, now)
}

func agentSessionLiveInQueries(ctx context.Context, q *sqlcgen.Queries, agentSessionID int64, now time.Time) bool {
	if agentSessionID <= 0 {
		return false
	}
	row, err := q.GetAgentSessionLiveness(ctx, agentSessionID)
	if err != nil {
		return false
	}
	return agentSessionLiveAt(row, now)
}

func agentSessionLiveAt(row sqlcgen.GetAgentSessionLivenessRow, now time.Time) bool {
	if !row.LastHeartbeatAt.Valid || strings.TrimSpace(row.LastHeartbeatAt.String) == "" {
		return false
	}
	heartbeat, err := time.Parse(time.RFC3339Nano, row.LastHeartbeatAt.String)
	if err != nil {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	return !heartbeat.Before(now.UTC().Add(-RuntimeLeaseDuration))
}

// RenewMonitorLease renews the lease of the session a monitor token is bound
// to. Writes are throttled to a third of the lease: the monitor polls far more
// often than the lease needs, and every renewal is a write that blocks readers.
func (s *Store) RenewMonitorLease(ctx context.Context, token string) error {
	agentSessionID, err := s.MonitorBindingAgentSessionID(ctx, token)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	threshold := now.Add(-RuntimeLeaseDuration / 3).Format(time.RFC3339Nano)
	if _, err := sqlcgen.New(s.db).RenewAgentSessionLease(ctx, sqlcgen.RenewAgentSessionLeaseParams{
		LastHeartbeatAt:  sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
		ID:               agentSessionID,
		LastHeartbeatAt2: sql.NullString{String: threshold, Valid: true},
	}); err != nil {
		return fmt.Errorf("renew monitor lease: %w", err)
	}
	return nil
}

// leaseCutoff is the oldest heartbeat that still counts as held, for the
// queries that decide whether a receiver is still there.
func leaseCutoff() sql.NullString {
	return sql.NullString{
		String: time.Now().UTC().Add(-RuntimeLeaseDuration).Format(time.RFC3339Nano),
		Valid:  true,
	}
}

// hasLiveExecutorMonitor reports whether the executor scope still has a
// monitor inside the health lease.
func (s *Store) hasLiveExecutorMonitor(ctx context.Context, projectID, goalID, taskID int64) (bool, error) {
	cutoff := time.Now().UTC().Add(-MonitorHealthLease).Format(time.RFC3339Nano)
	count, err := sqlcgen.New(s.db).CountLiveMonitorsForTaskScope(ctx, sqlcgen.CountLiveMonitorsForTaskScopeParams{
		ProjectID:  projectID,
		GoalID:     sql.NullInt64{Int64: goalID, Valid: true},
		TaskID:     sql.NullInt64{Int64: taskID, Valid: true},
		LastSeenAt: cutoff,
	})
	if err != nil {
		return false, fmt.Errorf("count live executor monitors: %w", err)
	}
	return count > 0, nil
}
