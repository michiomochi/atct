package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// EnableDevelopmentMode records the capability granted only by atct:dev:start.
func (s *Store) EnableDevelopmentMode(ctx context.Context, agentSessionID int64) error {
	if err := s.requireUndiscardedSession(ctx, agentSessionID); err != nil {
		return err
	}
	result, err := sqlcgen.New(s.db).EnableDevelopmentMode(ctx, agentSessionID)
	if err != nil {
		return fmt.Errorf("enable development mode: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("enable development mode rows affected: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DevelopmentModeEnabled(ctx context.Context, agentSessionID int64) (bool, error) {
	enabled, err := sqlcgen.New(s.db).DevelopmentModeEnabled(ctx, agentSessionID)
	if err != nil {
		return false, err
	}
	return enabled != 0, nil
}
