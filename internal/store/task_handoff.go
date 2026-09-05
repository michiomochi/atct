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

var (
	ErrTaskHandoffNotFound     = errors.New("task handoff not found")
	ErrTaskHandoffTaskMismatch = errors.New("task handoff task mismatch")
	ErrTaskHandoffGoalNotHeld  = errors.New("task handoff requires the goal's handoff: caller does not hold an open received handoff for goal")
	ErrTaskHandoffAlreadyOpen  = errors.New("task handoff already open")
	ErrTaskHandoffAmbiguous    = errors.New("multiple task handoffs pending receipt")
	ErrTaskHandoffReportEmpty  = errors.New("task handoff needs a complete_report describing what was done, what was verified, and paths changed; without it, the record cannot distinguish completion from no report")
)

const (
	taskHandoffReclaimedReport = "セッションが停止した"
	taskHandoffReleasedReport  = "作業ロックを手放した（報告者なし）"
)

// TaskHandoff records one delegation between agents. Each event timestamp is
// independent so a partial handoff remains observable.
type TaskHandoff struct {
	ID                string
	TaskID            int64
	RequestedBy       int64
	ReceivedBy        int64
	RequestReport     string
	CompleteReport    string
	RequestedAt       *time.Time
	ReceivedAt        *time.Time
	CompletedReportAt *time.Time
	Entries           []HandoffEntry
	HasMore           bool
	NextCursor        int64
	History           HandoffEntryPage
}

func taskHandoffFromRow(row sqlcgen.TaskHandoff) (TaskHandoff, error) {
	handoff := TaskHandoff{
		ID:             row.ID,
		TaskID:         row.TaskID,
		RequestedBy:    nullableAgentSessionID(row.RequestedBy),
		ReceivedBy:     nullableAgentSessionID(row.ReceivedBy),
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

func nullableAgentSessionID(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func completeReportIsEmpty(completeReport string) bool {
	return strings.TrimSpace(completeReport) == ""
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

func (s *Store) ensureTaskHandoffTask(ctx context.Context, handoffID string, taskID int64) error {
	existingTaskID, err := sqlcgen.New(s.db).GetTaskHandoffTaskID(ctx, handoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %q", ErrTaskHandoffNotFound, handoffID)
	}
	if err != nil {
		return fmt.Errorf("find task handoff %q: %w", handoffID, err)
	}
	if existingTaskID != taskID {
		return fmt.Errorf("%w: %q belongs to task %d, not %d", ErrTaskHandoffTaskMismatch, handoffID, existingTaskID, taskID)
	}
	return nil
}

func (s *Store) ensureTaskHandoffTaskForRequest(ctx context.Context, handoffID string, taskID int64) error {
	err := s.ensureTaskHandoffTask(ctx, handoffID, taskID)
	if errors.Is(err, ErrTaskHandoffNotFound) {
		return nil
	}
	return err
}

func (s *Store) requireGoalHandoffForTask(ctx context.Context, taskID int64, requestedBy int64) error {
	goalID, err := sqlcgen.New(s.db).GetTaskGoalID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("find goal for task %d: %w", taskID, err)
	}

	goalHandoff, err := s.openGoalHandoff(ctx, goalID)
	if err != nil {
		return fmt.Errorf("find live goal handoff for task %d: %w", taskID, err)
	}
	if goalHandoff != nil && goalHandoff.ReceivedBy == requestedBy && requestedBy != 0 && claimIsRunning(ctx, s, requestedBy) {
		return nil
	}

	return fmt.Errorf("%w: %d", ErrTaskHandoffGoalNotHeld, goalID)
}

// reclaimOpenTaskHandoff enforces the one-open-handoff rule before inserting a
// new request. A retry for the same handoff ID is allowed to fill an existing
// receipt-only row. For a different ID, only a handoff whose owner is no longer
// running may be reclaimed; an unknown owner is treated as active because its
// liveness cannot be disproved.
func (s *Store) reclaimOpenTaskHandoff(ctx context.Context, handoffID string, taskID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin task handoff reclaim tx: %w", err)
	}
	defer tx.Rollback()
	reclaimed, err := reclaimOpenTaskHandoffTx(ctx, tx, handoffID, taskID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task handoff reclaim: %w", err)
	}
	if reclaimed != nil {
		s.publishTaskHandoffReported(ctx, *reclaimed)
	}
	return nil
}

func reclaimOpenTaskHandoffTx(ctx context.Context, tx *sql.Tx, handoffID string, taskID int64) (*TaskHandoff, error) {
	rows, err := sqlcgen.New(tx).ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list open task handoffs: %w", err)
	}

	var open *TaskHandoff
	for i := range rows {
		handoff, err := taskHandoffFromRow(rows[i])
		if err != nil {
			return nil, fmt.Errorf("parse open task handoff: %w", err)
		}
		if handoff.CompletedReportAt != nil || handoff.ID == handoffID {
			continue
		}
		if open != nil {
			return nil, fmt.Errorf("%w: task %d has multiple open handoffs", ErrTaskHandoffAlreadyOpen, taskID)
		}
		candidate := handoff
		open = &candidate
	}
	if open == nil {
		return nil, nil
	}

	ownerID := open.ReceivedBy
	if ownerID == 0 {
		// An unreceived handoff has no receiver to inspect, so the requester
		// is the only available liveness signal.
		ownerID = open.RequestedBy
	}
	if ownerID == 0 || !claimIsDefinitelyDeadWithQuery(ctx, sqlcgen.New(tx), ownerID) {
		return nil, fmt.Errorf("%w: task %d has a live handoff owner", ErrTaskHandoffAlreadyOpen, taskID)
	}
	now := time.Now().UTC()
	result, err := sqlcgen.New(tx).CompleteTaskHandoff(ctx, sqlcgen.CompleteTaskHandoffParams{
		ID:                open.ID,
		TaskID:            taskID,
		CompletedReportAt: sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
		CompleteReport:    sql.NullString{String: taskHandoffReclaimedReport, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("reclaim task handoff %q: %w", open.ID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("reclaim task handoff %q rows affected: %w", open.ID, err)
	}
	if rowsAffected != 1 {
		return nil, fmt.Errorf("reclaim task handoff %q was not completed", open.ID)
	}
	authorID := open.ReceivedBy
	if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "task_handoff_entries", open.ID, HandoffEntryKindComplete, taskHandoffReclaimedReport, authorID, "", "", true, now); err != nil {
		return nil, fmt.Errorf("append reclaimed task handoff entry: %w", err)
	}
	open.CompletedReportAt = &now
	open.CompleteReport = taskHandoffReclaimedReport
	return open, nil
}

// openTaskHandoff returns the task's single received, incomplete handoff.
// Request-only handoffs are not claims and therefore do not authorize task
// release. The partial unique index should make multiple open rows
// impossible, but keep the ambiguity check at this boundary as well.
func (s *Store) openTaskHandoff(ctx context.Context, taskID int64) (*TaskHandoff, error) {
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
			return nil, fmt.Errorf("%w: task %d has multiple open handoffs", ErrTaskHandoffAmbiguous, taskID)
		}
		candidate := handoffs[i]
		open = &candidate
	}
	return open, nil
}

// RequestTaskHandoff records the request side of a handoff. It only writes
// request columns; a receipt or completion report is a separate call.
func (s *Store) RequestTaskHandoff(ctx context.Context, handoffID string, taskID int64, requestedBy int64, requestReport string) (TaskHandoff, error) {
	return s.requestTaskHandoff(ctx, handoffID, taskID, requestedBy, requestReport, true)
}

func (s *Store) requestTaskHandoffForClaim(ctx context.Context, handoffID string, taskID int64, requestedBy int64) (TaskHandoff, error) {
	return s.requestTaskHandoff(ctx, handoffID, taskID, requestedBy, "", false)
}

func (s *Store) requestTaskHandoff(ctx context.Context, handoffID string, taskID int64, requestedBy int64, requestReport string, requireLiveClaim bool) (TaskHandoff, error) {
	if err := s.ensureTaskHandoffTaskForRequest(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	if requireLiveClaim {
		if err := s.requireGoalHandoffForTask(ctx, taskID, requestedBy); err != nil {
			return TaskHandoff{}, err
		}
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff request tx: %w", err)
	}
	defer tx.Rollback()
	txq := sqlcgen.New(tx)
	existing, lookupErr := txq.GetTaskHandoff(ctx, handoffID)
	if lookupErr == nil && existing.CompletedReportAt.Valid && strings.TrimSpace(existing.CompletedReportAt.String) != "" {
		return TaskHandoff{}, fmt.Errorf("%w: task handoff %q is already complete; use amend or reopen", ErrHandoffEntryTerminal, handoffID)
	}
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return TaskHandoff{}, fmt.Errorf("find task handoff before request: %w", lookupErr)
	}
	reclaimed, err := reclaimOpenTaskHandoffTx(ctx, tx, handoffID, taskID)
	if err != nil {
		return TaskHandoff{}, err
	}
	existingEntryCount, err := txq.CountTaskHandoffRequestEntries(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("check existing task handoff request entry: %w", err)
	}
	if err := txq.RequestTaskHandoff(ctx, sqlcgen.RequestTaskHandoffParams{
		ID:            handoffID,
		TaskID:        taskID,
		RequestedBy:   sql.NullInt64{Int64: requestedBy, Valid: requestedBy != 0},
		RequestedAt:   sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
		RequestReport: sql.NullString{String: requestReport, Valid: requestReport != ""},
	}); err != nil {
		return TaskHandoff{}, fmt.Errorf("request task handoff: %w", err)
	}
	if strings.TrimSpace(requestReport) != "" && existingEntryCount == 0 {
		if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "task_handoff_entries", handoffID, HandoffEntryKindRequest, requestReport, requestedBy, "", "", true, now); err != nil {
			return TaskHandoff{}, fmt.Errorf("append task handoff request entry: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff request: %w", err)
	}
	if reclaimed != nil {
		s.publishTaskHandoffReported(ctx, *reclaimed)
	}
	return s.GetTaskHandoff(ctx, handoffID)
}

// ReceiveTaskHandoff records the receipt side of a requested handoff.
func (s *Store) ReceiveTaskHandoff(ctx context.Context, handoffID string, taskID int64, receivedBy int64) (TaskHandoff, error) {
	if err := s.ensureTaskHandoffTask(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff receive tx: %w", err)
	}
	defer tx.Rollback()
	current, err := sqlcgen.New(tx).GetTaskHandoff(ctx, handoffID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffNotFound, handoffID)
		}
		return TaskHandoff{}, fmt.Errorf("find task handoff receipt state: %w", err)
	}
	if current.TaskID != taskID || !current.RequestedAt.Valid || strings.TrimSpace(current.RequestedAt.String) == "" {
		return TaskHandoff{}, fmt.Errorf("%w: handoff %q has no request", ErrTaskHandoffNotFound, handoffID)
	}
	if current.ReceivedAt.Valid && current.ReceivedAt.String != "" {
		if current.ReceivedBy.Valid && current.ReceivedBy.Int64 != receivedBy {
			return TaskHandoff{}, fmt.Errorf("%w: handoff %q is already received by %d", ErrHandoffEntryParticipant, handoffID, current.ReceivedBy.Int64)
		}
		page, err := listHandoffEntries(ctx, sqlcgen.New(tx), "task_handoff_entries", handoffID, 0, HandoffHistoryMaxLimit)
		if err != nil {
			return TaskHandoff{}, fmt.Errorf("list task handoff history after retry receive: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return TaskHandoff{}, fmt.Errorf("commit task handoff receive retry: %w", err)
		}
		received, err := s.GetTaskHandoff(ctx, handoffID)
		if err != nil {
			return TaskHandoff{}, err
		}
		received.Entries = page.Entries
		received.HasMore = page.HasMore
		received.NextCursor = page.NextCursor
		received.History = page
		return received, nil
	}
	result, err := sqlcgen.New(tx).ReceiveTaskHandoff(ctx, sqlcgen.ReceiveTaskHandoffParams{
		ID:         handoffID,
		TaskID:     taskID,
		ReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: receivedBy != 0},
		ReceivedAt: sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("receive task handoff: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("receive task handoff rows affected: %w", err)
	}
	if n == 0 {
		return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffNotFound, handoffID)
	}
	if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "task_handoff_entries", handoffID, HandoffEntryKindReceived, "received", receivedBy, "", "", true, now); err != nil {
		return TaskHandoff{}, fmt.Errorf("append task handoff received entry: %w", err)
	}
	page, err := listHandoffEntries(ctx, sqlcgen.New(tx), "task_handoff_entries", handoffID, 0, HandoffHistoryMaxLimit)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("list task handoff history after receive: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff receive: %w", err)
	}
	received, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	received.Entries = page.Entries
	received.HasMore = page.HasMore
	received.NextCursor = page.NextCursor
	received.History = page
	return received, nil
}

// ReceiveTaskHandoffForTask finds the single requested and unreceived handoff
// for a task and records the receipt. Multiple pending handoffs are rejected
// so receipt cannot be assigned to the wrong delegation.
func (s *Store) ReceiveTaskHandoffForTask(ctx context.Context, taskID int64, receivedBy int64) (TaskHandoff, error) {
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
		return TaskHandoff{}, fmt.Errorf("%w: task %d", ErrTaskHandoffNotFound, taskID)
	}
	if len(pending) > 1 {
		return TaskHandoff{}, fmt.Errorf("%w: task %d has %d pending handoffs", ErrTaskHandoffAmbiguous, taskID, len(pending))
	}
	return s.ReceiveTaskHandoff(ctx, pending[0].ID, taskID, receivedBy)
}

// CompleteTaskHandoffForTask finds the single requested, received, and
// incomplete handoff for a task and records its completion. Multiple
// incomplete handoffs are rejected so completion cannot be assigned to the
// wrong delegation.
func (s *Store) CompleteTaskHandoffForTask(ctx context.Context, taskID int64, completeReport string) (TaskHandoff, error) {
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
		return TaskHandoff{}, fmt.Errorf("%w: task %d", ErrTaskHandoffNotFound, taskID)
	}
	if len(pending) > 1 {
		return TaskHandoff{}, fmt.Errorf("%w: task %d has %d incomplete handoffs", ErrTaskHandoffAmbiguous, taskID, len(pending))
	}
	return s.CompleteTaskHandoff(ctx, pending[0].ID, taskID, completeReport)
}

// CompleteTaskHandoff records the completion report side of a handoff. It
// only writes the completion timestamp and report and therefore preserves partial states.
func (s *Store) CompleteTaskHandoff(ctx context.Context, handoffID string, taskID int64, completeReport string) (TaskHandoff, error) {
	if completeReportIsEmpty(completeReport) {
		return TaskHandoff{}, ErrTaskHandoffReportEmpty
	}
	if err := s.ensureTaskHandoffTask(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff completion tx: %w", err)
	}
	defer tx.Rollback()
	current, err := sqlcgen.New(tx).GetTaskHandoff(ctx, handoffID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffNotFound, handoffID)
		}
		return TaskHandoff{}, fmt.Errorf("find task handoff receiver: %w", err)
	}
	if current.TaskID != taskID {
		return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffTaskMismatch, handoffID)
	}
	if !current.ReceivedAt.Valid || strings.TrimSpace(current.ReceivedAt.String) == "" {
		return TaskHandoff{}, fmt.Errorf("%w: handoff %q must be received before completion", ErrHandoffEntryParticipant, handoffID)
	}
	receivedBy := current.ReceivedBy
	result, err := sqlcgen.New(tx).CompleteTaskHandoff(ctx, sqlcgen.CompleteTaskHandoffParams{
		ID:                handoffID,
		TaskID:            taskID,
		CompletedReportAt: sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
		CompleteReport:    sql.NullString{String: completeReport, Valid: completeReport != ""},
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("complete task handoff: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("complete task handoff rows affected: %w", err)
	}
	if n == 0 {
		handoff, lookupErr := sqlcgen.New(tx).GetTaskHandoff(ctx, handoffID)
		if lookupErr == nil && handoff.CompletedReportAt.Valid && handoff.CompletedReportAt.String != "" {
			return TaskHandoff{}, fmt.Errorf("task handoff %q is already reported; use another path to add a report after completion", handoffID)
		}
		return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffNotFound, handoffID)
	}
	authorID := int64(0)
	if receivedBy.Valid {
		authorID = receivedBy.Int64
	}
	if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "task_handoff_entries", handoffID, HandoffEntryKindComplete, completeReport, authorID, "", "", true, now); err != nil {
		return TaskHandoff{}, fmt.Errorf("append task handoff complete entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff completion: %w", err)
	}
	completed, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	s.publishTaskHandoffReported(ctx, completed)
	return completed, nil
}

func (s *Store) publishTaskHandoffReported(ctx context.Context, completed TaskHandoff) {
	// Claim locks have no delegate report, so their completion is not reportable.
	if !handoffIsDelegation(completed.RequestedBy, completed.ReceivedBy) {
		return
	}
	// Notification is best-effort; do not turn a successful completion into an error.
	goalID, err := sqlcgen.New(s.db).GetTaskGoalID(ctx, completed.TaskID)
	if err != nil {
		return
	}
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return
	}
	s.notify.publishEvent(Event{
		Name: EventHandoffReported,
		Data: DetectionEvent{
			DetectionID:    NewDetectionID(),
			ProjectID:      goal.ProjectID,
			GoalID:         goalID,
			TaskID:         completed.TaskID,
			HandoffID:      completed.ID,
			CompleteReport: completed.CompleteReport,
		},
	})
}

// AmendTaskHandoffReport fills in or corrects the report on a handoff that is
// already closed without changing when it was completed.
func (s *Store) AmendTaskHandoffReport(ctx context.Context, handoffID string, taskID int64, completeReport string) (TaskHandoff, error) {
	if completeReportIsEmpty(completeReport) {
		return TaskHandoff{}, ErrTaskHandoffReportEmpty
	}
	if err := s.ensureTaskHandoffTask(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff amend tx: %w", err)
	}
	defer tx.Rollback()
	current, err := sqlcgen.New(tx).GetTaskHandoff(ctx, handoffID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffNotFound, handoffID)
		}
		return TaskHandoff{}, fmt.Errorf("find task handoff receiver for amend: %w", err)
	}
	if current.TaskID != taskID {
		return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffTaskMismatch, handoffID)
	}
	receivedBy := current.ReceivedBy
	result, err := sqlcgen.New(tx).AmendTaskHandoffReport(ctx, sqlcgen.AmendTaskHandoffReportParams{
		ID: handoffID, TaskID: taskID, CompleteReport: sql.NullString{String: completeReport, Valid: true},
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("amend task handoff report: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("amend task handoff report rows affected: %w", err)
	}
	if n == 0 {
		return TaskHandoff{}, fmt.Errorf("task handoff %q is not yet completed; use atct_handoff_complete", handoffID)
	}
	if !receivedBy.Valid || receivedBy.Int64 <= 0 {
		return TaskHandoff{}, fmt.Errorf("%w: completed task handoff has no received_by", ErrHandoffEntryParticipant)
	}
	if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "task_handoff_entries", handoffID, HandoffEntryKindAmend, completeReport, receivedBy.Int64, "", "", false, time.Now().UTC()); err != nil {
		return TaskHandoff{}, fmt.Errorf("append task handoff amend entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff amend: %w", err)
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
func (s *Store) ListTaskHandoffs(ctx context.Context, taskID int64) ([]TaskHandoff, error) {
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

// ListOpenTaskHandoffsForGoal returns all incomplete handoffs for tasks in a
// goal with one query.
func (s *Store) ListOpenTaskHandoffsForGoal(ctx context.Context, goalID int64) (map[int64]*TaskHandoff, error) {
	rows, err := sqlcgen.New(s.db).ListOpenTaskHandoffsForGoal(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list open task handoffs for goal: %w", err)
	}

	handoffs := make(map[int64]*TaskHandoff, len(rows))
	for _, row := range rows {
		handoff, err := taskHandoffFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("parse open task handoff: %w", err)
		}
		handoffs[handoff.TaskID] = &handoff
	}
	return handoffs, nil
}
