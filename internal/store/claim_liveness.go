package store

import (
	"context"
	"database/sql"
	"fmt"
	"syscall"

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
	if agentSessionID == 0 {
		return false
	}
	session, err := sqlcgen.New(s.db).GetAgentSessionLiveness(ctx, agentSessionID)
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
	session, err := sqlcgen.New(s.db).GetAgentSessionLiveness(ctx, agentSessionID)
	if err != nil || session.Pid == 0 || session.StartedAt == "" {
		return false
	}

	pid := int(session.Pid)
	if err := syscall.Kill(pid, 0); err != nil {
		return true
	}
	actualStartedAt, err := processStartedAt(pid)
	return err == nil && actualStartedAt != session.StartedAt
}
