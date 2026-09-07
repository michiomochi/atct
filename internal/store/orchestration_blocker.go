package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const (
	OrchestrationBlockerHumanDecision     = "human_decision"
	OrchestrationBlockerDependencyMerge   = "dependency_merge"
	OrchestrationBlockerOwnerCommander    = "commander"
	OrchestrationBlockerOwnerSubcommander = "subcommander"
)

var (
	ErrOrchestrationBlockerNotFound     = errors.New("orchestration blocker not found")
	ErrOrchestrationBlockerUnauthorized = errors.New("orchestration blocker operation is unauthorized")
	ErrOrchestrationBlockerInvalid      = errors.New("invalid orchestration blocker")
)

// OrchestrationBlocker is the canonical durable stop condition used by
// reconciliation. A resolved row remains in the table so a new generation
// cannot be confused with a previously settled blocker.
type OrchestrationBlocker struct {
	BlockerID   string     `json:"blocker_id"`
	ProjectID   int64      `json:"project_id"`
	GoalID      *int64     `json:"goal_id,omitempty"`
	TaskID      *int64     `json:"task_id,omitempty"`
	ScopeKey    string     `json:"scope_key"`
	Kind        string     `json:"kind"`
	SourceID    string     `json:"source_id"`
	Generation  string     `json:"generation"`
	OwnerRole   string     `json:"owner_role"`
	Instruction string     `json:"instruction"`
	OpenedAt    time.Time  `json:"opened_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

// OrchestrationBlockerReportInput is the explicit producer contract for a
// dependency/merge blocker. ScopeKey identifies the caller's lifecycle-owned
// scope; the recovery action is routed to the project's commander scope.
type OrchestrationBlockerReportInput struct {
	ProjectID      int64  `json:"project_id,omitempty"`
	GoalID         int64  `json:"goal_id,omitempty"`
	TaskID         int64  `json:"task_id,omitempty"`
	ScopeKey       string `json:"scope_key"`
	Kind           string `json:"kind"`
	SourceID       string `json:"source_id"`
	Generation     string `json:"generation"`
	OwnerRole      string `json:"owner_role"`
	Instruction    string `json:"instruction"`
	AgentSessionID int64  `json:"agent_session_id"`
}

// OrchestrationBlockerResolveInput identifies a previously reported blocker.
// The key fields are accepted as a convenience for clients that did not keep
// the response's blocker_id; blocker_id remains the preferred identity.
type OrchestrationBlockerResolveInput struct {
	BlockerID      string `json:"blocker_id,omitempty"`
	ScopeKey       string `json:"scope_key,omitempty"`
	Kind           string `json:"kind,omitempty"`
	SourceID       string `json:"source_id,omitempty"`
	Generation     string `json:"generation,omitempty"`
	AgentSessionID int64  `json:"agent_session_id"`
}

func OrchestrationBlockerID(kind, sourceID, generation string) string {
	kind = strings.TrimSpace(kind)
	sourceID = strings.TrimSpace(sourceID)
	generation = strings.TrimSpace(generation)
	if kind == "" || sourceID == "" || generation == "" {
		return ""
	}
	return "blocker:" + kind + ":" + sourceID + ":" + generation
}

func orchestrationBlockerFromRow(row sqlcgen.OrchestrationBlocker) (OrchestrationBlocker, error) {
	blocker := OrchestrationBlocker{
		BlockerID:   row.BlockerID,
		ProjectID:   row.ProjectID,
		ScopeKey:    row.ScopeKey,
		Kind:        row.Kind,
		SourceID:    row.SourceID,
		Generation:  row.Generation,
		OwnerRole:   row.OwnerRole,
		Instruction: row.Instruction,
	}
	if row.GoalID.Valid {
		value := row.GoalID.Int64
		blocker.GoalID = &value
	}
	if row.TaskID.Valid {
		value := row.TaskID.Int64
		blocker.TaskID = &value
	}
	var err error
	if blocker.OpenedAt, err = time.Parse(time.RFC3339Nano, row.OpenedAt); err != nil {
		return OrchestrationBlocker{}, fmt.Errorf("parse orchestration blocker opened_at: %w", err)
	}
	if row.ResolvedAt.Valid {
		resolved, err := time.Parse(time.RFC3339Nano, row.ResolvedAt.String)
		if err != nil {
			return OrchestrationBlocker{}, fmt.Errorf("parse orchestration blocker resolved_at: %w", err)
		}
		blocker.ResolvedAt = &resolved
	}
	return blocker, nil
}

func (s *Store) GetOrchestrationBlocker(ctx context.Context, blockerID string) (OrchestrationBlocker, error) {
	blockerID = strings.TrimSpace(blockerID)
	if blockerID == "" {
		return OrchestrationBlocker{}, fmt.Errorf("%w: blocker_id is required", ErrOrchestrationBlockerInvalid)
	}
	row, err := sqlcgen.New(s.db).GetOrchestrationBlocker(ctx, blockerID)
	if errors.Is(err, sql.ErrNoRows) {
		return OrchestrationBlocker{}, fmt.Errorf("%w: %s", ErrOrchestrationBlockerNotFound, blockerID)
	}
	if err != nil {
		return OrchestrationBlocker{}, fmt.Errorf("get orchestration blocker: %w", err)
	}
	return orchestrationBlockerFromRow(row)
}

func (s *Store) ListOrchestrationBlockers(ctx context.Context, projectID int64) ([]OrchestrationBlocker, error) {
	if projectID <= 0 {
		return nil, errors.New("project_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListOrchestrationBlockers(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list orchestration blockers: %w", err)
	}
	return convertOrchestrationBlockerRows(rows)
}

func (s *Store) ListOpenOrchestrationBlockers(ctx context.Context, projectID int64) ([]OrchestrationBlocker, error) {
	if projectID <= 0 {
		return nil, errors.New("project_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListOpenOrchestrationBlockers(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list open orchestration blockers: %w", err)
	}
	return convertOrchestrationBlockerRows(rows)
}

func convertOrchestrationBlockerRows(rows []sqlcgen.OrchestrationBlocker) ([]OrchestrationBlocker, error) {
	blockers := make([]OrchestrationBlocker, 0, len(rows))
	for _, row := range rows {
		blocker, err := orchestrationBlockerFromRow(row)
		if err != nil {
			return nil, err
		}
		blockers = append(blockers, blocker)
	}
	return blockers, nil
}

// ReportOrchestrationBlocker is the only external producer for dependency
// blockers. It requires a current lifecycle scope owned by the reporting
// session, so an executor or an observation of pane/Git state cannot create a
// canonical blocker.
func (s *Store) ReportOrchestrationBlocker(ctx context.Context, in OrchestrationBlockerReportInput) (OrchestrationBlocker, bool, error) {
	if err := validateOrchestrationBlockerReportInput(in); err != nil {
		return OrchestrationBlocker{}, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrchestrationBlocker{}, false, fmt.Errorf("begin orchestration blocker report tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	scope, err := orchestrationBlockerOwnerScope(ctx, q, in.ScopeKey, in.AgentSessionID, in.OwnerRole)
	if err != nil {
		return OrchestrationBlocker{}, false, err
	}
	if in.ProjectID != 0 && in.ProjectID != scope.ProjectID {
		return OrchestrationBlocker{}, false, fmt.Errorf("%w: scope %q belongs to project %d, not %d", ErrOrchestrationBlockerInvalid, in.ScopeKey, scope.ProjectID, in.ProjectID)
	}
	projectID := scope.ProjectID
	goalID, taskID, err := blockerSelectorIDs(in, scope)
	if err != nil {
		return OrchestrationBlocker{}, false, err
	}
	if err := validateBlockerSelectorScope(ctx, q, projectID, goalID, taskID); err != nil {
		return OrchestrationBlocker{}, false, err
	}
	blockerID := OrchestrationBlockerID(in.Kind, in.SourceID, in.Generation)
	existing, err := q.GetOrchestrationBlockerByKey(ctx, sqlcgen.GetOrchestrationBlockerByKeyParams{
		Kind: in.Kind, SourceID: in.SourceID, Generation: in.Generation,
	})
	existingFound := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return OrchestrationBlocker{}, false, fmt.Errorf("find orchestration blocker retry: %w", err)
	}
	if existingFound {
		if existing.ScopeKey != in.ScopeKey || existing.OwnerRole != in.OwnerRole || existing.ProjectID != projectID {
			return OrchestrationBlocker{}, false, fmt.Errorf("%w: blocker generation is owned by another scope", ErrOrchestrationBlockerUnauthorized)
		}
		if existing.ResolvedAt.Valid {
			if err := tx.Commit(); err != nil {
				return OrchestrationBlocker{}, false, fmt.Errorf("commit settled orchestration blocker retry: %w", err)
			}
			blocker, err := orchestrationBlockerFromRow(existing)
			return blocker, false, err
		}
	}

	goal := nullableBlockerID(goalID)
	task := nullableBlockerID(taskID)
	if existingFound {
		if err := q.UpdateOrchestrationBlocker(ctx, sqlcgen.UpdateOrchestrationBlockerParams{
			ProjectID: projectID, GoalID: goal, TaskID: task, ScopeKey: in.ScopeKey,
			OwnerRole: in.OwnerRole, Instruction: in.Instruction,
			Kind: in.Kind, SourceID: in.SourceID, Generation: in.Generation,
		}); err != nil {
			return OrchestrationBlocker{}, false, fmt.Errorf("update orchestration blocker: %w", err)
		}
	} else if err := q.InsertOrchestrationBlocker(ctx, sqlcgen.InsertOrchestrationBlockerParams{
		BlockerID: blockerID, ProjectID: projectID, GoalID: goal, TaskID: task,
		ScopeKey: in.ScopeKey, Kind: in.Kind, SourceID: in.SourceID,
		Generation: in.Generation, OwnerRole: in.OwnerRole, Instruction: in.Instruction,
		OpenedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return OrchestrationBlocker{}, false, fmt.Errorf("insert orchestration blocker: %w", err)
	}
	row, err := q.GetOrchestrationBlockerByKey(ctx, sqlcgen.GetOrchestrationBlockerByKeyParams{
		Kind: in.Kind, SourceID: in.SourceID, Generation: in.Generation,
	})
	if err != nil {
		return OrchestrationBlocker{}, false, fmt.Errorf("read reported orchestration blocker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OrchestrationBlocker{}, false, fmt.Errorf("commit orchestration blocker report: %w", err)
	}
	blocker, err := orchestrationBlockerFromRow(row)
	return blocker, !existingFound, err
}

// ResolveOrchestrationBlocker is idempotent for the owning commander or
// subcommander. The caller is required to identify the owning scope again;
// a resolved row cannot be used to bypass that authorization check.
func (s *Store) ResolveOrchestrationBlocker(ctx context.Context, in OrchestrationBlockerResolveInput) (OrchestrationBlocker, error) {
	if in.AgentSessionID <= 0 {
		return OrchestrationBlocker{}, fmt.Errorf("%w: agent_session_id is required", ErrOrchestrationBlockerUnauthorized)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrchestrationBlocker{}, fmt.Errorf("begin orchestration blocker resolve tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	row, err := findOrchestrationBlockerTx(ctx, q, in)
	if errors.Is(err, sql.ErrNoRows) {
		return OrchestrationBlocker{}, fmt.Errorf("%w: blocker identity", ErrOrchestrationBlockerNotFound)
	}
	if err != nil {
		return OrchestrationBlocker{}, err
	}
	if row.Kind != OrchestrationBlockerDependencyMerge {
		return OrchestrationBlocker{}, fmt.Errorf("%w: human decision blockers settle through decision transactions", ErrOrchestrationBlockerInvalid)
	}
	if _, err := orchestrationBlockerOwnerScope(ctx, q, row.ScopeKey, in.AgentSessionID, row.OwnerRole); err != nil {
		return OrchestrationBlocker{}, err
	}
	if row.ResolvedAt.Valid {
		if err := tx.Commit(); err != nil {
			return OrchestrationBlocker{}, fmt.Errorf("commit settled orchestration blocker lookup: %w", err)
		}
		return orchestrationBlockerFromRow(row)
	}
	result, err := q.ResolveOrchestrationBlocker(ctx, sqlcgen.ResolveOrchestrationBlockerParams{
		ResolvedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true},
		BlockerID:  row.BlockerID,
	})
	if err != nil {
		return OrchestrationBlocker{}, fmt.Errorf("resolve orchestration blocker: %w", err)
	}
	if _, err := result.RowsAffected(); err != nil {
		return OrchestrationBlocker{}, fmt.Errorf("inspect orchestration blocker resolution: %w", err)
	}
	row, err = q.GetOrchestrationBlocker(ctx, row.BlockerID)
	if err != nil {
		return OrchestrationBlocker{}, fmt.Errorf("read resolved orchestration blocker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OrchestrationBlocker{}, fmt.Errorf("commit orchestration blocker resolve: %w", err)
	}
	return orchestrationBlockerFromRow(row)
}

func validateOrchestrationBlockerReportInput(in OrchestrationBlockerReportInput) error {
	if in.Kind != OrchestrationBlockerDependencyMerge {
		return fmt.Errorf("%w: only %q may be reported", ErrOrchestrationBlockerInvalid, OrchestrationBlockerDependencyMerge)
	}
	if in.AgentSessionID <= 0 {
		return fmt.Errorf("%w: agent_session_id is required", ErrOrchestrationBlockerUnauthorized)
	}
	if strings.TrimSpace(in.ScopeKey) == "" || strings.TrimSpace(in.SourceID) == "" || strings.TrimSpace(in.Generation) == "" || strings.TrimSpace(in.Instruction) == "" {
		return fmt.Errorf("%w: scope_key, source_id, generation, and instruction are required", ErrOrchestrationBlockerInvalid)
	}
	if in.OwnerRole != OrchestrationBlockerOwnerCommander && in.OwnerRole != OrchestrationBlockerOwnerSubcommander {
		return fmt.Errorf("%w: owner_role must be commander or subcommander", ErrOrchestrationBlockerInvalid)
	}
	return nil
}

func orchestrationBlockerOwnerScope(ctx context.Context, q *sqlcgen.Queries, scopeKey string, sessionID int64, ownerRole string) (OrchestrationScope, error) {
	row, err := q.GetOrchestrationDeliveryScope(ctx, strings.TrimSpace(scopeKey))
	if errors.Is(err, sql.ErrNoRows) {
		return OrchestrationScope{}, fmt.Errorf("%w: scope %q is not active", ErrOrchestrationBlockerUnauthorized, scopeKey)
	}
	if err != nil {
		return OrchestrationScope{}, fmt.Errorf("find orchestration blocker owner scope: %w", err)
	}
	scope, err := orchestrationScopeFromRow(row)
	if err != nil {
		return OrchestrationScope{}, err
	}
	if !scope.Active || scope.Role != ownerRole || scope.AgentSessionID == 0 || scope.AgentSessionID != sessionID {
		return OrchestrationScope{}, fmt.Errorf("%w: caller %d does not own scope %q as %s", ErrOrchestrationBlockerUnauthorized, sessionID, scopeKey, ownerRole)
	}
	return scope, nil
}

func blockerSelectorIDs(in OrchestrationBlockerReportInput, scope OrchestrationScope) (*int64, *int64, error) {
	goalID, taskID := in.GoalID, in.TaskID
	if scope.GoalID != nil {
		if goalID != 0 && goalID != *scope.GoalID {
			return nil, nil, fmt.Errorf("%w: goal_id does not match owner scope", ErrOrchestrationBlockerInvalid)
		}
		goalID = *scope.GoalID
	}
	if scope.TaskID != nil {
		if taskID != 0 && taskID != *scope.TaskID {
			return nil, nil, fmt.Errorf("%w: task_id does not match owner scope", ErrOrchestrationBlockerInvalid)
		}
		taskID = *scope.TaskID
	}
	if taskID != 0 && goalID == 0 {
		return nil, nil, fmt.Errorf("%w: task_id requires goal_id", ErrOrchestrationBlockerInvalid)
	}
	return nullableBlockerPointer(goalID), nullableBlockerPointer(taskID), nil
}

func nullableBlockerPointer(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func nullableBlockerID(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func validateBlockerSelectorScope(ctx context.Context, q *sqlcgen.Queries, projectID int64, goalID, taskID *int64) error {
	if goalID != nil {
		goalProjectID, err := q.GetGoalProjectID(ctx, *goalID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: goal_id %d does not exist", ErrOrchestrationBlockerInvalid, *goalID)
		}
		if err != nil {
			return fmt.Errorf("lookup blocker goal %d: %w", *goalID, err)
		}
		if goalProjectID != projectID {
			return fmt.Errorf("%w: goal_id %d belongs to project %d, not %d", ErrOrchestrationBlockerInvalid, *goalID, goalProjectID, projectID)
		}
	}
	if taskID != nil {
		taskGoalID, err := q.GetTaskGoalID(ctx, *taskID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task_id %d does not exist", ErrOrchestrationBlockerInvalid, *taskID)
		}
		if err != nil {
			return fmt.Errorf("lookup blocker task %d: %w", *taskID, err)
		}
		if goalID == nil || taskGoalID != *goalID {
			return fmt.Errorf("%w: task_id %d does not belong to goal %d", ErrOrchestrationBlockerInvalid, *taskID, valueOrZeroBlockerID(goalID))
		}
	}
	return nil
}

func valueOrZeroBlockerID(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func findOrchestrationBlockerTx(ctx context.Context, q *sqlcgen.Queries, in OrchestrationBlockerResolveInput) (sqlcgen.OrchestrationBlocker, error) {
	if strings.TrimSpace(in.BlockerID) != "" {
		return q.GetOrchestrationBlocker(ctx, strings.TrimSpace(in.BlockerID))
	}
	if strings.TrimSpace(in.Kind) == "" || strings.TrimSpace(in.SourceID) == "" || strings.TrimSpace(in.Generation) == "" {
		return sqlcgen.OrchestrationBlocker{}, fmt.Errorf("%w: blocker_id or kind, source_id, and generation are required", ErrOrchestrationBlockerInvalid)
	}
	return q.GetOrchestrationBlockerByKey(ctx, sqlcgen.GetOrchestrationBlockerByKeyParams{
		Kind: in.Kind, SourceID: in.SourceID, Generation: in.Generation,
	})
}

func upsertHumanDecisionBlockerTx(ctx context.Context, q *sqlcgen.Queries, decision domain.Decision) error {
	projectID, err := q.GetGoalProjectID(ctx, decision.GoalID)
	if err != nil {
		return fmt.Errorf("find project for human decision blocker: %w", err)
	}
	generation := decision.CreatedAt.UTC().Format(time.RFC3339Nano)
	sourceID := strconv.FormatInt(decision.ID, 10)
	goalID := decision.GoalID
	var taskID *int64
	if decision.TaskID != 0 {
		taskID = &decision.TaskID
	}
	instruction := fmt.Sprintf("answer human decision %d for goal %d before resuming: %s", decision.ID, decision.GoalID, strings.TrimSpace(decision.Question))
	if instruction == fmt.Sprintf("answer human decision %d for goal %d before resuming: ", decision.ID, decision.GoalID) {
		instruction = fmt.Sprintf("answer human decision %d for goal %d before resuming", decision.ID, decision.GoalID)
	}
	return upsertHumanDecisionBlockerValuesTx(ctx, q, OrchestrationBlocker{
		BlockerID:   OrchestrationBlockerID(OrchestrationBlockerHumanDecision, sourceID, generation),
		ProjectID:   projectID,
		GoalID:      &goalID,
		TaskID:      taskID,
		ScopeKey:    ProjectOrchestrationScopeKey(projectID),
		Kind:        OrchestrationBlockerHumanDecision,
		SourceID:    sourceID,
		Generation:  generation,
		OwnerRole:   OrchestrationBlockerOwnerCommander,
		Instruction: instruction,
		OpenedAt:    decision.CreatedAt.UTC(),
	})
}

func upsertHumanDecisionBlockerValuesTx(ctx context.Context, q *sqlcgen.Queries, blocker OrchestrationBlocker) error {
	existing, err := q.GetOrchestrationBlockerByKey(ctx, sqlcgen.GetOrchestrationBlockerByKeyParams{
		Kind: blocker.Kind, SourceID: blocker.SourceID, Generation: blocker.Generation,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find human decision blocker: %w", err)
	}
	if err == nil && existing.ResolvedAt.Valid {
		return nil
	}
	goalID := nullableBlockerID(blocker.GoalID)
	taskID := nullableBlockerID(blocker.TaskID)
	if err == nil {
		if err := q.UpdateOrchestrationBlocker(ctx, sqlcgen.UpdateOrchestrationBlockerParams{
			ProjectID: blocker.ProjectID, GoalID: goalID, TaskID: taskID, ScopeKey: blocker.ScopeKey,
			OwnerRole: blocker.OwnerRole, Instruction: blocker.Instruction,
			Kind: blocker.Kind, SourceID: blocker.SourceID, Generation: blocker.Generation,
		}); err != nil {
			return fmt.Errorf("update human decision blocker: %w", err)
		}
		return nil
	}
	if err := q.InsertOrchestrationBlocker(ctx, sqlcgen.InsertOrchestrationBlockerParams{
		BlockerID: blocker.BlockerID, ProjectID: blocker.ProjectID, GoalID: goalID, TaskID: taskID,
		ScopeKey: blocker.ScopeKey, Kind: blocker.Kind, SourceID: blocker.SourceID,
		Generation: blocker.Generation, OwnerRole: blocker.OwnerRole, Instruction: blocker.Instruction,
		OpenedAt: blocker.OpenedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return fmt.Errorf("insert human decision blocker: %w", err)
	}
	return nil
}

func resolveHumanDecisionBlockerTx(ctx context.Context, q *sqlcgen.Queries, decisionID int64, resolvedAt time.Time) error {
	result, err := q.ResolveHumanDecisionBlocker(ctx, sqlcgen.ResolveHumanDecisionBlockerParams{
		ResolvedAt: sql.NullString{String: resolvedAt.UTC().Format(time.RFC3339Nano), Valid: true},
		SourceID:   strconv.FormatInt(decisionID, 10),
	})
	if err != nil {
		return fmt.Errorf("resolve human decision blocker: %w", err)
	}
	if _, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("inspect human decision blocker resolution: %w", err)
	}
	return nil
}
