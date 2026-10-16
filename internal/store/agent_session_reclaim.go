package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const (
	// agentSessionIdleAfter is how long a session may go without a heartbeat before
	// the reclaim considers it. A live monitor renews the lease every few seconds.
	agentSessionIdleAfter = 24 * time.Hour
	// reclaimMonitorStoppedAfter is how long a stopped monitor row is kept.
	reclaimMonitorStoppedAfter = 7 * 24 * time.Hour
	// reclaimRowBudget caps the rows one tick touches (discarded or deleted), so a
	// large reclaim is spread over several ticks and never holds the one connection long.
	// It is small because each deleted session costs one scan per referencing table
	// (the foreign key check on delete): about 8 ms per session on a 20k-row copy. Add
	// indexes on the referencing columns and this can rise to the 5,000 in the spec.
	reclaimRowBudget = 50
)

// sessionRef is one column that holds an agent_sessions id.
type sessionRef struct{ table, column string }

// protectedSessionRefs hold a session that is still in use: a handoff it takes
// part in (a goal claim is a goal handoff), a project claim, or a handoff entry it
// wrote. A session behind one of these is neither discarded nor deleted.
var protectedSessionRefs = []sessionRef{
	{"task_handoffs", "requested_by"},
	{"task_handoffs", "received_by"},
	{"task_handoffs", "review_requested_by"},
	{"task_handoffs", "review_received_by"},
	{"task_handoffs", "review_rejection_received_by"},
	{"goal_handoffs", "requested_by"},
	{"goal_handoffs", "received_by"},
	{"goal_handoffs", "review_requested_by"},
	{"goal_handoffs", "review_received_by"},
	{"goal_handoffs", "review_rejection_received_by"},
	{"plan_handoffs", "review_requested_by"},
	{"plan_handoffs", "review_received_by"},
	{"plan_handoffs", "review_rejection_received_by"},
	{"task_create_handoffs", "requested_by"},
	{"task_create_handoffs", "received_by"},
	{"task_create_handoffs", "completed_by"},
	{"task_handoff_entries", "author_session_id"},
	{"goal_handoff_entries", "author_session_id"},
	{"plan_handoff_entries", "author_session_id"},
	{"projects", "claimed_by"},
}

// heldSessionRefs name a session in a row that does not keep the session in use.
// Such a session is discarded, not deleted, so the row keeps a real session.
var heldSessionRefs = []sessionRef{
	{"decisions", "agent_session_id"},
	{"monitor_bindings", "agent_session_id"},
	{"monitor_health", "agent_session_id"},
	{"agent_sessions", "discarded_by"},
}

// ReclaimResult counts what one reclaim tick did.
type ReclaimResult struct {
	SessionsDiscarded int64
	SessionsDeleted   int64
	MonitorHealth     int64
	MonitorBindings   int64
}

// Total is every row the tick touched.
func (r ReclaimResult) Total() int64 {
	return r.SessionsDiscarded + r.SessionsDeleted + r.MonitorHealth + r.MonitorBindings
}

// ReclaimAgentSessions discards or deletes idle agent sessions and the monitor rows
// that no longer point at a live session. It runs in one transaction and touches at
// most reclaimRowBudget rows. The statements are in queries/agent_session_reclaim.sql.
func (s *Store) ReclaimAgentSessions(ctx context.Context, now time.Time) (ReclaimResult, error) {
	return s.reclaimAgentSessionsWithBudget(ctx, now, reclaimRowBudget)
}

func (s *Store) reclaimAgentSessionsWithBudget(ctx context.Context, now time.Time, budget int64) (ReclaimResult, error) {
	var result ReclaimResult
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin agent session reclaim: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(s.db).WithTx(tx)

	idleBefore := sql.NullString{String: formatTimestamp(now.Add(-agentSessionIdleAfter)), Valid: true}
	leaseAfter := formatTimestamp(now.Add(-RuntimeLeaseDuration))
	weekBefore := formatTimestamp(now.Add(-reclaimMonitorStoppedAfter))

	discarded, err := q.ReclaimDiscardReferencedSessions(ctx, sqlcgen.ReclaimDiscardReferencedSessionsParams{
		DiscardedAt:   sql.NullString{String: formatTimestamp(now), Valid: true},
		DiscardReason: "reclaimed: idle with no heartbeat, still referenced",
		IdleBefore:    idleBefore,
		LeaseAfter:    leaseAfter,
		RowLimit:      budget,
	})
	if err != nil {
		return result, fmt.Errorf("discard referenced idle sessions: %w", err)
	}
	result.SessionsDiscarded = discarded
	budget -= discarded

	deleted, err := q.ReclaimDeleteUnreferencedSessions(ctx, sqlcgen.ReclaimDeleteUnreferencedSessionsParams{
		IdleBefore: idleBefore,
		LeaseAfter: leaseAfter,
		RowLimit:   budget,
	})
	if err != nil {
		return result, fmt.Errorf("delete unreferenced idle sessions: %w", err)
	}
	result.SessionsDeleted = deleted
	budget -= deleted

	monitorHealth, err := q.ReclaimDeleteDeadMonitorHealth(ctx, sqlcgen.ReclaimDeleteDeadMonitorHealthParams{
		LeaseAfter: leaseAfter,
		WeekBefore: sql.NullString{String: weekBefore, Valid: true},
		RowLimit:   budget,
	})
	if err != nil {
		return result, fmt.Errorf("delete dead monitor health rows: %w", err)
	}
	result.MonitorHealth = monitorHealth
	budget -= monitorHealth

	monitorBindings, err := q.ReclaimDeleteDeadMonitorBindings(ctx, sqlcgen.ReclaimDeleteDeadMonitorBindingsParams{
		WeekBefore: weekBefore,
		RowLimit:   budget,
	})
	if err != nil {
		return result, fmt.Errorf("delete dead monitor bindings: %w", err)
	}
	result.MonitorBindings = monitorBindings

	if err := tx.Commit(); err != nil {
		return ReclaimResult{}, fmt.Errorf("commit agent session reclaim: %w", err)
	}
	return result, nil
}
