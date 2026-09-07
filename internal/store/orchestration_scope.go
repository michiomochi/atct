package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// OrchestrationScope is the lifecycle-owned identity a live monitor must
// refresh. Its key includes the handoff or claim generation, so a later
// process cannot make an earlier assignment appear live.
type OrchestrationScope struct {
	ScopeKey         string    `json:"scope_key"`
	ProjectID        int64     `json:"project_id"`
	GoalID           *int64    `json:"goal_id,omitempty"`
	TaskID           *int64    `json:"task_id,omitempty"`
	Role             string    `json:"role"`
	AgentSessionID   int64     `json:"agent_session_id"`
	AgentKey         string    `json:"agent_key,omitempty"`
	SourceGeneration string    `json:"source_generation"`
	Active           bool      `json:"active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ExpectedMonitorScope is kept as a domain-level name for callers that use
// the reconciliation contract terminology.
type ExpectedMonitorScope = OrchestrationScope

func ProjectOrchestrationScopeKey(projectID int64) string {
	return "project:" + strconv.FormatInt(projectID, 10) + ":commander"
}

func GoalOrchestrationScopeKey(goalID int64, handoffID string) string {
	return "goal:" + strconv.FormatInt(goalID, 10) + ":subcommander:" + strings.TrimSpace(handoffID)
}

func TaskOrchestrationScopeKey(taskID int64, handoffID string) string {
	return "task:" + strconv.FormatInt(taskID, 10) + ":executor:" + strings.TrimSpace(handoffID)
}

func orchestrationScopeFromRow(row sqlcgen.OrchestrationScope) (OrchestrationScope, error) {
	scope := OrchestrationScope{
		ScopeKey:         row.ScopeKey,
		ProjectID:        row.ProjectID,
		Role:             row.Role,
		AgentSessionID:   row.AgentSessionID,
		AgentKey:         row.AgentKey,
		SourceGeneration: row.SourceGeneration,
		Active:           row.Active != 0,
	}
	if row.GoalID.Valid {
		value := row.GoalID.Int64
		scope.GoalID = &value
	}
	if row.TaskID.Valid {
		value := row.TaskID.Int64
		scope.TaskID = &value
	}
	var err error
	if scope.CreatedAt, err = time.Parse(time.RFC3339Nano, row.CreatedAt); err != nil {
		return OrchestrationScope{}, fmt.Errorf("parse orchestration scope creation: %w", err)
	}
	if scope.UpdatedAt, err = time.Parse(time.RFC3339Nano, row.UpdatedAt); err != nil {
		return OrchestrationScope{}, fmt.Errorf("parse orchestration scope update: %w", err)
	}
	return scope, nil
}

func (s *Store) ListOrchestrationScopes(ctx context.Context, projectID int64) ([]OrchestrationScope, error) {
	if projectID <= 0 {
		return nil, errors.New("project_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListOrchestrationScopes(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list orchestration scopes: %w", err)
	}
	scopes := make([]OrchestrationScope, 0, len(rows))
	for _, row := range rows {
		scope, err := orchestrationScopeFromRow(row)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func (s *Store) ListActiveOrchestrationScopes(ctx context.Context, projectID int64) ([]OrchestrationScope, error) {
	if projectID <= 0 {
		return nil, errors.New("project_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListActiveOrchestrationScopes(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list active orchestration scopes: %w", err)
	}
	scopes := make([]OrchestrationScope, 0, len(rows))
	for _, row := range rows {
		scope, err := orchestrationScopeFromRow(row)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func (s *Store) ListExpectedMonitorScopes(ctx context.Context, projectID int64) ([]OrchestrationScope, error) {
	return s.ListActiveOrchestrationScopes(ctx, projectID)
}

func (s *Store) ListActiveExpectedMonitorScopes(ctx context.Context, projectID int64) ([]OrchestrationScope, error) {
	return s.ListActiveOrchestrationScopes(ctx, projectID)
}

func orchestrationScopeAgentKey(ctx context.Context, q *sqlcgen.Queries, agentSessionID int64) (string, error) {
	if agentSessionID <= 0 {
		return "", nil
	}
	key, err := q.GetAgentSessionKey(ctx, agentSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get agent session key %d: %w", agentSessionID, err)
	}
	return key, nil
}

func upsertOrchestrationScopeTx(ctx context.Context, q *sqlcgen.Queries, scopeKey string, projectID int64, goalID, taskID *int64, role string, agentSessionID int64, sourceGeneration, at string) error {
	if strings.TrimSpace(scopeKey) == "" {
		return errors.New("orchestration scope key is required")
	}
	if projectID <= 0 || strings.TrimSpace(role) == "" || strings.TrimSpace(sourceGeneration) == "" || strings.TrimSpace(at) == "" {
		return errors.New("orchestration scope identity is incomplete")
	}
	agentKey, err := orchestrationScopeAgentKey(ctx, q, agentSessionID)
	if err != nil {
		return err
	}
	if err := q.UpsertOrchestrationScope(ctx, sqlcgen.UpsertOrchestrationScopeParams{
		ScopeKey:         scopeKey,
		ProjectID:        projectID,
		GoalID:           nullableMonitorID(goalID),
		TaskID:           nullableMonitorID(taskID),
		Role:             role,
		AgentSessionID:   agentSessionID,
		AgentKey:         agentKey,
		SourceGeneration: sourceGeneration,
		CreatedAt:        at,
		UpdatedAt:        at,
	}); err != nil {
		return fmt.Errorf("upsert orchestration scope %q: %w", scopeKey, err)
	}
	return nil
}

func deactivateOrchestrationScopeTx(ctx context.Context, q *sqlcgen.Queries, scopeKey, at string) error {
	if strings.TrimSpace(scopeKey) == "" || strings.TrimSpace(at) == "" {
		return errors.New("orchestration scope release identity is required")
	}
	if _, err := q.DeactivateOrchestrationScope(ctx, sqlcgen.DeactivateOrchestrationScopeParams{UpdatedAt: at, ScopeKey: scopeKey}); err != nil {
		return fmt.Errorf("deactivate orchestration scope %q: %w", scopeKey, err)
	}
	return nil
}

func deactivateOrchestrationScopesForGoalTx(ctx context.Context, q *sqlcgen.Queries, goalID int64, at string) error {
	if goalID <= 0 || strings.TrimSpace(at) == "" {
		return errors.New("orchestration goal scope release identity is required")
	}
	if err := q.DeactivateOrchestrationScopesForGoal(ctx, sqlcgen.DeactivateOrchestrationScopesForGoalParams{UpdatedAt: at, GoalID: nullableMonitorID(&goalID)}); err != nil {
		return fmt.Errorf("deactivate orchestration scopes for goal %d: %w", goalID, err)
	}
	return nil
}

func deactivateOrchestrationScopesForTaskTx(ctx context.Context, q *sqlcgen.Queries, taskID int64, at string) error {
	if taskID <= 0 || strings.TrimSpace(at) == "" {
		return errors.New("orchestration task scope release identity is required")
	}
	if err := q.DeactivateOrchestrationScopesForTask(ctx, sqlcgen.DeactivateOrchestrationScopesForTaskParams{UpdatedAt: at, TaskID: nullableMonitorID(&taskID)}); err != nil {
		return fmt.Errorf("deactivate orchestration scopes for task %d: %w", taskID, err)
	}
	return nil
}

func sameOrchestrationSelector(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// MonitorHealthMatchesScope accepts health only when it identifies the exact
// active lifecycle scope and its rightful agent. Process records, legacy
// unscoped rows, foreign sessions, stopped rows, and expired rows therefore
// cannot satisfy an expected scope.
func MonitorHealthMatchesScope(health MonitorHealth, scope OrchestrationScope) bool {
	if !scope.Active || strings.TrimSpace(scope.ScopeKey) == "" || health.ScopeKey != scope.ScopeKey {
		return false
	}
	if health.ProjectID != scope.ProjectID || health.Role != scope.Role || !sameOrchestrationSelector(health.GoalID, scope.GoalID) || !sameOrchestrationSelector(health.TaskID, scope.TaskID) {
		return false
	}
	if health.StoppedAt != nil || health.LastSeenAt.IsZero() || time.Since(health.LastSeenAt) > monitorHealthLease {
		return false
	}
	if scope.AgentSessionID == 0 && strings.TrimSpace(scope.AgentKey) == "" {
		return false
	}
	sessionMatches := scope.AgentSessionID != 0 && health.AgentSessionID == scope.AgentSessionID
	keyMatches := strings.TrimSpace(scope.AgentKey) != "" && health.AgentKey == scope.AgentKey
	if !sessionMatches && !keyMatches {
		return false
	}
	if scope.AgentSessionID != 0 && health.AgentSessionID != 0 && health.AgentSessionID != scope.AgentSessionID {
		return false
	}
	if strings.TrimSpace(scope.AgentKey) != "" && strings.TrimSpace(health.AgentKey) != "" && health.AgentKey != scope.AgentKey {
		return false
	}
	expectedID := MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt, health.ScopeKey)
	return expectedID != "" && expectedID == health.MonitorID
}

// ListMatchingMonitorHealth returns only live health rows that match an
// active expected scope. It deliberately does not inspect the Codex process
// registry; that registry is process cleanup metadata, not lifecycle truth.
func (s *Store) ListMatchingMonitorHealth(ctx context.Context, scopes []OrchestrationScope) ([]MonitorHealth, error) {
	if len(scopes) == 0 {
		return []MonitorHealth{}, nil
	}
	healthByProject, err := s.listMonitorHealthForScopes(ctx, scopes)
	if err != nil {
		return nil, err
	}
	return matchingMonitorHealthForScopes(scopes, healthByProject), nil
}
