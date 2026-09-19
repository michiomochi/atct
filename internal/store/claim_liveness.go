package store

import (
	"context"
	"database/sql"
	"fmt"
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

func claimIsRunningWithQueries(ctx context.Context, q *sqlcgen.Queries, agentSessionID int64) bool {
	return agentSessionLiveInQueries(ctx, q, agentSessionID, time.Now())
}

// claimIsDefinitelyDead is the lease read the other way round. There is no
// longer a weaker and a stronger answer: a lapsed lease is proof on its own,
// where a missing pid only ever meant "cannot tell".
func claimIsDefinitelyDead(ctx context.Context, s *Store, agentSessionID int64) bool {
	return !s.AgentSessionLive(ctx, agentSessionID, time.Now())
}
