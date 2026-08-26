package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrLegacyIDRemoved explains how to recover IDs that still use the removed
// UUID-style representation.
var ErrLegacyIDRemoved = errors.New("id must be a number; UUID-style ids were removed in 0020.\nsee doc/specs/2026-08-27-uuid-to-integer-mapping.md")

// resolveID accepts canonical numeric IDs. Legacy UUID prefixes were removed
// by migration 0020, so report a migration-specific error instead of looking
// them up as missing rows.
func resolveID(table, value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%s id is required", table)
	}
	if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
		return id, nil
	}
	return 0, ErrLegacyIDRemoved
}

// ResolveProjectID, ResolveGoalID, ResolveTaskID, and ResolveDecisionID keep
// accepting numeric strings at the transport boundary while exposing the
// canonical integer primary key to callers.
func (s *Store) ResolveProjectID(_ context.Context, value string) (int64, error) {
	return resolveID("projects", value)
}
func (s *Store) ResolveGoalID(_ context.Context, value string) (int64, error) {
	return resolveID("goals", value)
}
func (s *Store) ResolveTaskID(_ context.Context, value string) (int64, error) {
	return resolveID("tasks", value)
}
func (s *Store) ResolveDecisionID(_ context.Context, value string) (int64, error) {
	return resolveID("decisions", value)
}
