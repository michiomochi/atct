package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// MonitorLiveScope is the role scope a Monitor covers: one per project/role,
// and per goal or task where the role is scoped to one.
type MonitorLiveScope struct {
	ProjectID int64
	Role      string
	GoalID    *int64
}

// HasLiveMonitorForScope reports whether the scope still has a Monitor inside
// the health lease. It uses the same window as ListMonitorHealth: a row that
// stopped, or stopped heartbeating, is not live.
//
// The scope columns are matched instead of monitor_health.agent_session_id
// because monitors do not record that column; it is 0 for every row.
func (s *Store) HasLiveMonitorForScope(ctx context.Context, scope MonitorLiveScope) (bool, error) {
	if scope.ProjectID <= 0 {
		return false, errors.New("project_id is required")
	}
	if scope.Role == "" {
		return false, errors.New("role is required")
	}
	cutoff := time.Now().UTC().Add(-MonitorHealthLease).Format(time.RFC3339Nano)
	count, err := sqlcgen.New(s.db).CountLiveMonitorsForScope(ctx, sqlcgen.CountLiveMonitorsForScopeParams{
		ProjectID:  scope.ProjectID,
		Role:       scope.Role,
		GoalID:     nullableMonitorID(scope.GoalID),
		LastSeenAt: cutoff,
	})
	if err != nil {
		return false, fmt.Errorf("count live monitors: %w", err)
	}
	return count > 0, nil
}
