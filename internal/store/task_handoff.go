package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

var (
	ErrTaskHandoffNotFound      = errors.New("task handoff not found")
	ErrTaskHandoffTaskMismatch  = errors.New("task handoff task mismatch")
	ErrTaskHandoffTaskUnclaimed = errors.New("task handoff task is unclaimed")
	ErrTaskHandoffAlreadyOpen   = errors.New("task handoff already open")
	ErrTaskHandoffAmbiguous     = errors.New("multiple task handoffs pending receipt")
)

// TaskHandoff records one delegation between agents. Each event timestamp is
// independent so a partial handoff remains observable.
type TaskHandoff struct {
	ID                string
	TaskID            string
	RequestedBy       string
	ReceivedBy        string
	RequestReport     string
	CompleteReport    string
	RequestedAt       *time.Time
	ReceivedAt        *time.Time
	CompletedReportAt *time.Time
}

func taskHandoffFromRow(row sqlcgen.TaskHandoff) (TaskHandoff, error) {
	handoff := TaskHandoff{
		ID:             row.ID,
		TaskID:         row.TaskID,
		RequestedBy:    row.RequestedBy.String,
		ReceivedBy:     row.ReceivedBy.String,
		RequestReport:  row.RequestReport.String,
		CompleteReport: row.CompleteReport.String,
	}
	var err error
	if handoff.RequestedAt, err = parseTaskHandoffTime("requested_at", row.RequestedAt); err != nil {
		return TaskHandoff{}, err
	}
	if handoff.ReceivedAt, err = parseTaskHandoffTime("received_at", row.ReceivedAt); err != nil {
		return TaskHandoff{}, err
	}
	if handoff.CompletedReportAt, err = parseTaskHandoffTime("completed_report_at", row.CompletedReportAt); err != nil {
		return TaskHandoff{}, err
	}
	return handoff, nil
}

func parseTaskHandoffTime(column string, value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse task handoff %s: %w", column, err)
	}
	return &parsed, nil
}

func (s *Store) ensureTaskHandoffTask(ctx context.Context, handoffID, taskID string) error {
	existingTaskID, err := sqlcgen.New(s.db).GetTaskHandoffTaskID(ctx, handoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find task handoff %q: %w", handoffID, err)
	}
	if existingTaskID != taskID {
		return fmt.Errorf("%w: %q belongs to task %q, not %q", ErrTaskHandoffTaskMismatch, handoffID, existingTaskID, taskID)
	}
	return nil
}

func (s *Store) requireLiveTaskClaim(ctx context.Context, taskID string) error {
	projectID, err := sqlcgen.New(s.db).GetTaskProjectID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("find project for task %q: %w", taskID, err)
	}

	running, _, err := ClaimLiveness(ctx, s, projectID)
	if err != nil {
		return fmt.Errorf("check claim liveness for task %q: %w", taskID, err)
	}
	for _, task := range running {
		if task.ID == taskID {
			return nil
		}
	}

	return fmt.Errorf("%w: %s", ErrTaskHandoffTaskUnclaimed, taskID)
}

// reclaimOpenTaskHandoff enforces the one-open-handoff rule before inserting a
// new request. A retry for the same handoff ID is allowed to fill an existing
// receipt-only row. For a different ID, only a handoff whose owner is no longer
// running may be reclaimed; an unknown owner is treated as active because its
// liveness cannot be disproved.
func (s *Store) reclaimOpenTaskHandoff(ctx context.Context, handoffID, taskID string) error {
	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return fmt.Errorf("list open task handoffs: %w", err)
	}

	var open *TaskHandoff
	for i := range handoffs {
		if handoffs[i].CompletedReportAt != nil || handoffs[i].ID == handoffID {
			continue
		}
		if open != nil {
			return fmt.Errorf("%w: task %q has multiple open handoffs", ErrTaskHandoffAlreadyOpen, taskID)
		}
		open = &handoffs[i]
	}
	if open == nil {
		return nil
	}

	ownerID := open.ReceivedBy
	if ownerID == "" {
		// An unreceived handoff has no receiver to inspect, so the requester
		// is the only available liveness signal.
		ownerID = open.RequestedBy
	}
	if ownerID == "" || claimIsRunning(ctx, s, ownerID) {
		return fmt.Errorf("%w: task %q has a live handoff owner", ErrTaskHandoffAlreadyOpen, taskID)
	}
	if _, err := s.CompleteTaskHandoff(ctx, open.ID, taskID, "セッションが停止した"); err != nil {
		return fmt.Errorf("reclaim task handoff %q: %w", open.ID, err)
	}
	return nil
}

// openTaskHandoff returns the task's single received, incomplete handoff.
// Request-only handoffs are not claims and therefore do not authorize task
// release. The partial unique index should make multiple open rows
// impossible, but keep the ambiguity check at this boundary as well.
func (s *Store) openTaskHandoff(ctx context.Context, taskID string) (*TaskHandoff, error) {
	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return nil, err
	}

	var open *TaskHandoff
	for i := range handoffs {
		if handoffs[i].ReceivedAt == nil || handoffs[i].CompletedReportAt != nil {
			continue
		}
		if open != nil {
			return nil, fmt.Errorf("%w: task %q has multiple open handoffs", ErrTaskHandoffAmbiguous, taskID)
		}
		candidate := handoffs[i]
		open = &candidate
	}
	return open, nil
}

// RequestTaskHandoff records the request side of a handoff. It only writes
// request columns; a receipt or completion report is a separate call.
func (s *Store) RequestTaskHandoff(ctx context.Context, handoffID, taskID, requestedBy string, requestReport string) (TaskHandoff, error) {
	return s.requestTaskHandoff(ctx, handoffID, taskID, requestedBy, requestReport, true)
}

func (s *Store) requestTaskHandoffForClaim(ctx context.Context, handoffID, taskID, requestedBy string) (TaskHandoff, error) {
	return s.requestTaskHandoff(ctx, handoffID, taskID, requestedBy, "", false)
}

func (s *Store) requestTaskHandoff(ctx context.Context, handoffID, taskID, requestedBy, requestReport string, requireLiveClaim bool) (TaskHandoff, error) {
	if err := s.ensureTaskHandoffTask(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	if requireLiveClaim {
		if err := s.requireLiveTaskClaim(ctx, taskID); err != nil {
			return TaskHandoff{}, err
		}
	}
	if err := s.reclaimOpenTaskHandoff(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := sqlcgen.New(s.db).RequestTaskHandoff(ctx, sqlcgen.RequestTaskHandoffParams{
		ID:            handoffID,
		TaskID:        taskID,
		RequestedBy:   sql.NullString{String: requestedBy, Valid: true},
		RequestedAt:   sql.NullString{String: now, Valid: true},
		RequestReport: sql.NullString{String: requestReport, Valid: requestReport != ""},
	}); err != nil {
		return TaskHandoff{}, fmt.Errorf("request task handoff: %w", err)
	}
	return s.GetTaskHandoff(ctx, handoffID)
}

// ReceiveTaskHandoff records the receipt side of a handoff. If the request
// row does not exist yet, this creates a receipt-only row with request fields
// left NULL; the same handoff ID can be completed by a later request call.
func (s *Store) ReceiveTaskHandoff(ctx context.Context, handoffID, taskID, receivedBy string) (TaskHandoff, error) {
	if err := s.ensureTaskHandoffTask(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := sqlcgen.New(s.db).ReceiveTaskHandoff(ctx, sqlcgen.ReceiveTaskHandoffParams{
		ID:         handoffID,
		TaskID:     taskID,
		ReceivedBy: sql.NullString{String: receivedBy, Valid: true},
		ReceivedAt: sql.NullString{String: now, Valid: true},
	}); err != nil {
		return TaskHandoff{}, fmt.Errorf("receive task handoff: %w", err)
	}
	return s.GetTaskHandoff(ctx, handoffID)
}

// ReceiveTaskHandoffForTask finds the single requested and unreceived handoff
// for a task and records the receipt. Multiple pending handoffs are rejected
// so receipt cannot be assigned to the wrong delegation.
func (s *Store) ReceiveTaskHandoffForTask(ctx context.Context, taskID, receivedBy string) (TaskHandoff, error) {
	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("list pending task handoffs: %w", err)
	}
	pending := make([]TaskHandoff, 0, len(handoffs))
	for _, handoff := range handoffs {
		if handoff.RequestedAt != nil && handoff.ReceivedAt == nil {
			pending = append(pending, handoff)
		}
	}
	if len(pending) == 0 {
		return TaskHandoff{}, fmt.Errorf("%w: task %s", ErrTaskHandoffNotFound, taskID)
	}
	if len(pending) > 1 {
		return TaskHandoff{}, fmt.Errorf("%w: task %q has %d pending handoffs", ErrTaskHandoffAmbiguous, taskID, len(pending))
	}
	return s.ReceiveTaskHandoff(ctx, pending[0].ID, taskID, receivedBy)
}

// CompleteTaskHandoffForTask finds the single requested, received, and
// incomplete handoff for a task and records its completion. Multiple
// incomplete handoffs are rejected so completion cannot be assigned to the
// wrong delegation.
func (s *Store) CompleteTaskHandoffForTask(ctx context.Context, taskID, completeReport string) (TaskHandoff, error) {
	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("list incomplete task handoffs: %w", err)
	}
	pending := make([]TaskHandoff, 0, len(handoffs))
	for _, handoff := range handoffs {
		if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil {
			pending = append(pending, handoff)
		}
	}
	if len(pending) == 0 {
		return TaskHandoff{}, fmt.Errorf("%w: task %s", ErrTaskHandoffNotFound, taskID)
	}
	if len(pending) > 1 {
		return TaskHandoff{}, fmt.Errorf("%w: task %q has %d incomplete handoffs", ErrTaskHandoffAmbiguous, taskID, len(pending))
	}
	return s.CompleteTaskHandoff(ctx, pending[0].ID, taskID, completeReport)
}

// CompleteTaskHandoff records the completion report side of a handoff. It
// only writes the completion timestamp and report and therefore preserves partial states.
func (s *Store) CompleteTaskHandoff(ctx context.Context, handoffID, taskID string, completeReport string) (TaskHandoff, error) {
	if err := s.ensureTaskHandoffTask(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := sqlcgen.New(s.db).CompleteTaskHandoff(ctx, sqlcgen.CompleteTaskHandoffParams{
		ID:                handoffID,
		TaskID:            taskID,
		CompletedReportAt: sql.NullString{String: now, Valid: true},
		CompleteReport:    sql.NullString{String: completeReport, Valid: completeReport != ""},
	}); err != nil {
		return TaskHandoff{}, fmt.Errorf("complete task handoff: %w", err)
	}
	return s.GetTaskHandoff(ctx, handoffID)
}

// GetTaskHandoff returns one handoff, including NULL timestamps as nil.
func (s *Store) GetTaskHandoff(ctx context.Context, handoffID string) (TaskHandoff, error) {
	row, err := sqlcgen.New(s.db).GetTaskHandoff(ctx, handoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffNotFound, handoffID)
	}
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("get task handoff %q: %w", handoffID, err)
	}
	return taskHandoffFromRow(row)
}

// ListTaskHandoffs returns all handoffs for a task, including partial rows.
func (s *Store) ListTaskHandoffs(ctx context.Context, taskID string) ([]TaskHandoff, error) {
	rows, err := sqlcgen.New(s.db).ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task handoffs: %w", err)
	}

	handoffs := make([]TaskHandoff, 0, len(rows))
	for _, row := range rows {
		handoff, err := taskHandoffFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("parse task handoff: %w", err)
		}
		handoffs = append(handoffs, handoff)
	}
	return handoffs, nil
}
