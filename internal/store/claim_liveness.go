package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// ClaimLiveness separates claimed tasks whose recorded process is still the
// process that owns the claim from claims that can no longer be verified.
func ClaimLiveness(ctx context.Context, s *Store, projectID int64) (running []domain.Task, stale []domain.Task, err error) {
	if projectID == 0 {
		return nil, nil, fmt.Errorf("project id is required")
	}

	claims, err := sqlcgen.New(s.db).ListOpenTaskHandoffClaims(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	for _, claim := range claims {
		task, err := taskFromFields(
			claim.ID,
			claim.GoalID,
			claim.Title,
			claim.Description,
			claim.Status,
			claim.Agent,
			claim.SortOrder,
			claim.DeclareKey,
			claim.SnoozedUntil,
			claim.CreatedAt,
			claim.UpdatedAt,
		)
		if err != nil {
			return nil, nil, err
		}
		agentSessionID := nullableClaimInt64(claim.ReceivedBy)
		if agentSessionID == 0 {
			// Until receipt, requested_by is the only session identity available.
			agentSessionID = nullableClaimInt64(claim.RequestedBy)
		}
		if claimIsRunning(ctx, s, agentSessionID) {
			running = append(running, task)
		} else {
			stale = append(stale, task)
		}
	}
	return running, stale, nil
}

// GoalClaimLiveness separates claimed goals whose recorded process is still
// the process that owns the claim from claims that can no longer be verified.
// It deliberately uses the same claimIsRunning check as ClaimLiveness.
func GoalClaimLiveness(ctx context.Context, s *Store, projectID int64) (running []domain.Goal, stale []domain.Goal, err error) {
	if projectID == 0 {
		return nil, nil, fmt.Errorf("project id is required")
	}

	claims, err := sqlcgen.New(s.db).ListOpenGoalHandoffClaims(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	for _, claim := range claims {
		goal, err := goalFromFields(
			claim.ID,
			claim.ProjectID,
			claim.DerivedFromGoalID,
			claim.Content,
			"",
			"",
			claim.Status,
			claim.Creator,
			claim.ResultSummary,
			claim.WorkDone,
			claim.NowPossible,
			claim.HowToVerify,
			claim.Surprises,
			claim.NeedsReview,
			claim.NextSteps,
			claim.CreatedAt,
			claim.UpdatedAt,
		)
		if err != nil {
			return nil, nil, err
		}
		agentSessionID := nullableClaimInt64(claim.ReceivedBy)
		if agentSessionID == 0 {
			// Until receipt, requested_by is the only session identity available.
			agentSessionID = nullableClaimInt64(claim.RequestedBy)
		}
		if claimIsRunning(ctx, s, agentSessionID) {
			running = append(running, goal)
		} else {
			stale = append(stale, goal)
		}
	}
	return running, stale, nil
}

func nullableClaimInt64(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func claimIsRunning(ctx context.Context, s *Store, agentSessionID int64) bool {
	return claimIsRunningWithQueries(ctx, sqlcgen.New(s.db), agentSessionID)
}

// monitorLiveness reports the session's liveness through its Monitor, and
// whether a Monitor is the right thing to ask.
//
// Over the HTTP MCP transport the session has no process of its own: the
// daemon serves every session, so agent_sessions.pid names the daemon, which
// is always alive and can never be disproven. The Monitor does have a process
// of its own, one per session, and it dies with the pane. Its heartbeat is
// already the liveness signal everywhere else, so use it here too.
//
// A session that never reported a Monitor keeps the old rule; only a session
// that has one is judged by it.
func monitorLiveness(ctx context.Context, q *sqlcgen.Queries, agentSessionID int64) (live bool, monitored bool) {
	monitors, err := q.CountMonitorsForAgentSession(ctx, agentSessionID)
	if err != nil || monitors == 0 {
		return false, false
	}
	cutoff := time.Now().UTC().Add(-MonitorHealthLease).Format(time.RFC3339Nano)
	liveCount, err := q.CountLiveMonitorsForAgentSession(ctx, sqlcgen.CountLiveMonitorsForAgentSessionParams{
		AgentSessionID: agentSessionID,
		LastSeenAt:     cutoff,
	})
	if err != nil {
		return false, false
	}
	return liveCount > 0, true
}

func claimIsRunningWithQueries(ctx context.Context, q *sqlcgen.Queries, agentSessionID int64) bool {
	if agentSessionID == 0 {
		return false
	}
	if live, monitored := monitorLiveness(ctx, q, agentSessionID); monitored {
		return live
	}
	session, err := q.GetAgentSessionLiveness(ctx, agentSessionID)
	if err != nil {
		return false
	}
	pid := int(session.Pid)
	startedAt := session.StartedAt
	if pid == 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}

	actualStartedAt, err := processStartedAt(pid)
	return err == nil && actualStartedAt == startedAt
}

// claimIsDefinitelyDead is intentionally stricter than claimIsRunning. A
// session registered without process identity cannot be proven dead, so an
// open handoff owned by it must not be reclaimed by a concurrent claimant.
func claimIsDefinitelyDead(ctx context.Context, s *Store, agentSessionID int64) bool {
	if agentSessionID == 0 {
		return false
	}
	queries := sqlcgen.New(s.db)
	if live, monitored := monitorLiveness(ctx, queries, agentSessionID); monitored {
		return !live
	}
	session, err := queries.GetAgentSessionLiveness(ctx, agentSessionID)
	if err != nil || session.Pid == 0 || session.StartedAt == "" {
		return false
	}

	pid := int(session.Pid)
	if err := syscall.Kill(pid, 0); err != nil {
		return errors.Is(err, syscall.ESRCH)
	}
	actualStartedAt, err := processStartedAt(pid)
	return err == nil && actualStartedAt != session.StartedAt
}
