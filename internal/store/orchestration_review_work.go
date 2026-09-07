package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const (
	OrchestrationReviewWorkStateRequested = "requested"
	OrchestrationReviewWorkStateReceived  = "received"
	OrchestrationReviewWorkStateRejected  = "rejected"
	OrchestrationReviewWorkStateCompleted = "completed"
)

// OrchestrationReviewWork is the durable lifecycle record for one review
// generation. Settled rows remain so a restart can distinguish old review
// work from a new request; active is reserved for reviewer work that still
// needs an explicit receive or settlement operation.
type OrchestrationReviewWork struct {
	ReviewWorkID              string     `json:"review_work_id"`
	ProjectID                 int64      `json:"project_id"`
	GoalID                    int64      `json:"goal_id"`
	TaskID                    *int64     `json:"task_id,omitempty"`
	Kind                      string     `json:"kind"`
	HandoffID                 string     `json:"handoff_id"`
	RequesterSessionID        int64      `json:"requester_session_id"`
	RequesterScopeKey         string     `json:"requester_scope_key"`
	ExpectedReviewerRole      string     `json:"expected_reviewer_role"`
	ReviewerScopeKey          string     `json:"reviewer_scope_key"`
	ReviewerSessionID         int64      `json:"reviewer_session_id,omitempty"`
	State                     string     `json:"state"`
	ReviewRequestedGeneration string     `json:"review_requested_generation"`
	ReviewReceivedGeneration  string     `json:"review_received_generation,omitempty"`
	SettlementGeneration      string     `json:"settlement_generation,omitempty"`
	Active                    bool       `json:"active"`
	ActionRole                string     `json:"action_role,omitempty"`
	ActionScopeKey            string     `json:"action_scope_key,omitempty"`
	ActionTaskID              *int64     `json:"action_task_id,omitempty"`
	ActionInstruction         string     `json:"action_instruction,omitempty"`
	OpenedAt                  time.Time  `json:"opened_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
	ResolvedAt                *time.Time `json:"resolved_at,omitempty"`
}

func OrchestrationReviewWorkID(kind, handoffID, generation string) string {
	kind = strings.TrimSpace(kind)
	handoffID = strings.TrimSpace(handoffID)
	generation = strings.TrimSpace(generation)
	if kind == "" || handoffID == "" || generation == "" {
		return ""
	}
	return "review_work:" + kind + ":" + handoffID + ":" + generation
}

func orchestrationReviewWorkFromRow(row sqlcgen.OrchestrationReviewWork) (OrchestrationReviewWork, error) {
	work := OrchestrationReviewWork{
		ReviewWorkID:         row.ReviewWorkID,
		ProjectID:            row.ProjectID,
		GoalID:               row.GoalID,
		Kind:                 row.Kind,
		HandoffID:            row.HandoffID,
		RequesterSessionID:   row.RequesterSessionID,
		RequesterScopeKey:    row.RequesterScopeKey,
		ExpectedReviewerRole: row.ExpectedReviewerRole,
		ReviewerScopeKey:     row.ReviewerScopeKey,
		State:                row.State,
		Active:               row.Active != 0,
	}
	if row.TaskID.Valid {
		value := row.TaskID.Int64
		work.TaskID = &value
	}
	if row.ReviewerSessionID.Valid {
		work.ReviewerSessionID = row.ReviewerSessionID.Int64
	}
	work.ReviewRequestedGeneration = row.ReviewRequestedGeneration
	if row.ReviewReceivedGeneration.Valid {
		work.ReviewReceivedGeneration = row.ReviewReceivedGeneration.String
	}
	if row.SettlementGeneration.Valid {
		work.SettlementGeneration = row.SettlementGeneration.String
	}
	if row.ActionRole.Valid {
		work.ActionRole = row.ActionRole.String
	}
	if row.ActionScopeKey.Valid {
		work.ActionScopeKey = row.ActionScopeKey.String
	}
	if row.ActionTaskID.Valid {
		value := row.ActionTaskID.Int64
		work.ActionTaskID = &value
	}
	if row.ActionInstruction.Valid {
		work.ActionInstruction = row.ActionInstruction.String
	}
	var err error
	if work.OpenedAt, err = time.Parse(time.RFC3339Nano, row.OpenedAt); err != nil {
		return OrchestrationReviewWork{}, fmt.Errorf("parse orchestration review work opened_at: %w", err)
	}
	if work.UpdatedAt, err = time.Parse(time.RFC3339Nano, row.UpdatedAt); err != nil {
		return OrchestrationReviewWork{}, fmt.Errorf("parse orchestration review work updated_at: %w", err)
	}
	if row.ResolvedAt.Valid {
		resolved, err := time.Parse(time.RFC3339Nano, row.ResolvedAt.String)
		if err != nil {
			return OrchestrationReviewWork{}, fmt.Errorf("parse orchestration review work resolved_at: %w", err)
		}
		work.ResolvedAt = &resolved
	}
	return work, nil
}

func convertOrchestrationReviewWorkRows(rows []sqlcgen.OrchestrationReviewWork) ([]OrchestrationReviewWork, error) {
	work := make([]OrchestrationReviewWork, 0, len(rows))
	for _, row := range rows {
		item, err := orchestrationReviewWorkFromRow(row)
		if err != nil {
			return nil, err
		}
		work = append(work, item)
	}
	return work, nil
}

func (s *Store) ListOrchestrationReviewWork(ctx context.Context, projectID int64) ([]OrchestrationReviewWork, error) {
	if projectID <= 0 {
		return nil, errors.New("project_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListOrchestrationReviewWork(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list orchestration review work: %w", err)
	}
	return convertOrchestrationReviewWorkRows(rows)
}

func (s *Store) ListPendingOrchestrationReviewWork(ctx context.Context, projectID int64) ([]OrchestrationReviewWork, error) {
	if projectID <= 0 {
		return nil, errors.New("project_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListPendingOrchestrationReviewWork(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list pending orchestration review work: %w", err)
	}
	return convertOrchestrationReviewWorkRows(rows)
}

func reviewWorkGeneration(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

func parseReviewWorkTime(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}

// goalScopeKeyForSessionTx identifies the lifecycle-owned subcommander scope
// without creating scope state. Empty is retained for legacy rows whose
// monitor scope predates the durable review-work migration.
func goalScopeKeyForSessionTx(ctx context.Context, tx *sql.Tx, goalID, sessionID int64) string {
	handoffID, err := sqlcgen.New(tx).GetOpenGoalHandoffIDForReceiver(ctx, sqlcgen.GetOpenGoalHandoffIDForReceiverParams{
		GoalID: goalID, ReceivedBy: sql.NullInt64{Int64: sessionID, Valid: sessionID != 0},
	})
	if err != nil {
		return ""
	}
	return GoalOrchestrationScopeKey(goalID, handoffID)
}

type orchestrationReviewWorkRequest struct {
	Kind                      string
	ProjectID                 int64
	GoalID                    int64
	TaskID                    sql.NullInt64
	HandoffID                 string
	RequesterSessionID        int64
	RequesterScopeKey         string
	ExpectedReviewerRole      string
	ReviewerScopeKey          string
	ReviewRequestedGeneration string
	OpenedAt                  string
}

func insertOrchestrationReviewWorkTx(ctx context.Context, tx *sql.Tx, in orchestrationReviewWorkRequest) error {
	if OrchestrationReviewWorkID(in.Kind, in.HandoffID, in.ReviewRequestedGeneration) == "" {
		return errors.New("review work identity is required")
	}
	if in.ProjectID <= 0 || in.GoalID <= 0 || in.RequesterSessionID == 0 || in.ExpectedReviewerRole == "" {
		return errors.New("review work ownership is incomplete")
	}
	if in.OpenedAt == "" {
		return errors.New("review work opened_at is required")
	}
	return sqlcgen.New(tx).InsertOrchestrationReviewWork(ctx, sqlcgen.InsertOrchestrationReviewWorkParams{
		ReviewWorkID:              OrchestrationReviewWorkID(in.Kind, in.HandoffID, in.ReviewRequestedGeneration),
		ProjectID:                 in.ProjectID,
		GoalID:                    in.GoalID,
		TaskID:                    in.TaskID,
		Kind:                      in.Kind,
		HandoffID:                 in.HandoffID,
		RequesterSessionID:        in.RequesterSessionID,
		RequesterScopeKey:         in.RequesterScopeKey,
		ExpectedReviewerRole:      in.ExpectedReviewerRole,
		ReviewerScopeKey:          in.ReviewerScopeKey,
		ReviewRequestedGeneration: in.ReviewRequestedGeneration,
		OpenedAt:                  in.OpenedAt,
		UpdatedAt:                 in.OpenedAt,
	})
}

func recordTaskReviewWorkTx(ctx context.Context, tx *sql.Tx, projectID, goalID, taskID, requesterID, reviewerID int64, handoffID string, requestedAt *time.Time) error {
	generation := reviewWorkGeneration(requestedAt)
	return insertOrchestrationReviewWorkTx(ctx, tx, orchestrationReviewWorkRequest{
		Kind:                      "task",
		ProjectID:                 projectID,
		GoalID:                    goalID,
		TaskID:                    sql.NullInt64{Int64: taskID, Valid: true},
		HandoffID:                 handoffID,
		RequesterSessionID:        requesterID,
		RequesterScopeKey:         TaskOrchestrationScopeKey(taskID, handoffID),
		ExpectedReviewerRole:      "subcommander",
		ReviewerScopeKey:          goalScopeKeyForSessionTx(ctx, tx, goalID, reviewerID),
		ReviewRequestedGeneration: generation,
		OpenedAt:                  generation,
	})
}

func recordGoalReviewWorkTx(ctx context.Context, tx *sql.Tx, projectID, goalID, requesterID int64, handoffID string, requestedAt *time.Time) error {
	generation := reviewWorkGeneration(requestedAt)
	return insertOrchestrationReviewWorkTx(ctx, tx, orchestrationReviewWorkRequest{
		Kind:                      "goal",
		ProjectID:                 projectID,
		GoalID:                    goalID,
		HandoffID:                 handoffID,
		RequesterSessionID:        requesterID,
		RequesterScopeKey:         GoalOrchestrationScopeKey(goalID, handoffID),
		ExpectedReviewerRole:      "commander",
		ReviewerScopeKey:          ProjectOrchestrationScopeKey(projectID),
		ReviewRequestedGeneration: generation,
		OpenedAt:                  generation,
	})
}

func recordPlanReviewWorkTx(ctx context.Context, tx *sql.Tx, projectID, goalID, requesterID int64, handoffID string, requestedAt *time.Time) error {
	generation := reviewWorkGeneration(requestedAt)
	return insertOrchestrationReviewWorkTx(ctx, tx, orchestrationReviewWorkRequest{
		Kind:                      "plan",
		ProjectID:                 projectID,
		GoalID:                    goalID,
		HandoffID:                 handoffID,
		RequesterSessionID:        requesterID,
		RequesterScopeKey:         goalScopeKeyForSessionTx(ctx, tx, goalID, requesterID),
		ExpectedReviewerRole:      "commander",
		ReviewerScopeKey:          ProjectOrchestrationScopeKey(projectID),
		ReviewRequestedGeneration: generation,
		OpenedAt:                  generation,
	})
}

func receiveOrchestrationReviewWorkTx(ctx context.Context, tx *sql.Tx, kind, handoffID string, requestedAt *time.Time, receivedBy int64, receivedAt string) error {
	generation := reviewWorkGeneration(requestedAt)
	result, err := sqlcgen.New(tx).ReceiveOrchestrationReviewWork(ctx, sqlcgen.ReceiveOrchestrationReviewWorkParams{
		ReviewerSessionID:         sql.NullInt64{Int64: receivedBy, Valid: receivedBy != 0},
		ReviewReceivedGeneration:  sql.NullString{String: receivedAt, Valid: receivedAt != ""},
		UpdatedAt:                 receivedAt,
		Kind:                      kind,
		HandoffID:                 handoffID,
		ReviewRequestedGeneration: generation,
	})
	if err != nil {
		return fmt.Errorf("receive orchestration review work: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("receive orchestration review work rows affected: %w", err)
	} else if affected == 0 {
		return fmt.Errorf("orchestration review work %s/%s is not requested", kind, handoffID)
	}
	return nil
}

func settleOrchestrationReviewWorkTx(ctx context.Context, tx *sql.Tx, kind, handoffID string, requestedAt *time.Time, state, settledAt, actionRole, actionScopeKey string, actionTaskID sql.NullInt64, instruction string) error {
	generation := reviewWorkGeneration(requestedAt)
	result, err := sqlcgen.New(tx).SettleOrchestrationReviewWork(ctx, sqlcgen.SettleOrchestrationReviewWorkParams{
		State:                     state,
		SettlementGeneration:      sql.NullString{String: settledAt, Valid: settledAt != ""},
		ActionRole:                sql.NullString{String: actionRole, Valid: actionRole != ""},
		ActionScopeKey:            sql.NullString{String: actionScopeKey, Valid: actionScopeKey != ""},
		ActionTaskID:              actionTaskID,
		ActionInstruction:         sql.NullString{String: instruction, Valid: instruction != ""},
		UpdatedAt:                 settledAt,
		ResolvedAt:                sql.NullString{String: settledAt, Valid: settledAt != ""},
		Kind:                      kind,
		HandoffID:                 handoffID,
		ReviewRequestedGeneration: generation,
	})
	if err != nil {
		return fmt.Errorf("settle orchestration review work: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("settle orchestration review work rows affected: %w", err)
	} else if affected == 0 {
		return fmt.Errorf("orchestration review work %s/%s is not active", kind, handoffID)
	}
	return nil
}

func nextTodoTaskIDTx(ctx context.Context, tx *sql.Tx, goalID int64) (int64, error) {
	taskID, err := sqlcgen.New(tx).GetNextTodoTaskID(ctx, goalID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find next todo task: %w", err)
	}
	return taskID, nil
}

func settleTaskReviewWorkTx(ctx context.Context, tx *sql.Tx, goalID, taskID int64, handoffID string, requestedAt *time.Time, state, settledAt string) error {
	actionRole := ""
	actionScopeKey := ""
	actionTaskID := sql.NullInt64{}
	instruction := ""
	if state == OrchestrationReviewWorkStateCompleted {
		nextTaskID, err := nextTodoTaskIDTx(ctx, tx, goalID)
		if err != nil {
			return err
		}
		if nextTaskID != 0 {
			reviewerScopeKey, err := sqlcgen.New(tx).GetOrchestrationReviewWorkReviewerScopeKey(ctx, sqlcgen.GetOrchestrationReviewWorkReviewerScopeKeyParams{
				Kind: "task", HandoffID: handoffID, ReviewRequestedGeneration: reviewWorkGeneration(requestedAt),
			})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("find task review owner scope: %w", err)
			}
			actionRole = "subcommander"
			actionScopeKey = reviewerScopeKey
			actionTaskID = sql.NullInt64{Int64: nextTaskID, Valid: true}
			instruction = fmt.Sprintf("task handoff %s completed; hand off declared todo task %d to an executor, then launch its atct codex monitor --role executor --task %d -- after session and role validation; leave the task todo until the executor receives it", handoffID, nextTaskID, nextTaskID)
		}
	}
	return settleOrchestrationReviewWorkTx(ctx, tx, "task", handoffID, requestedAt, state, settledAt, actionRole, actionScopeKey, actionTaskID, instruction)
}
