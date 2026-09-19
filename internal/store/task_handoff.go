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
	ErrTaskHandoffNotFound               = errors.New("task handoff not found")
	ErrTaskHandoffLiveReceiver           = errors.New("task handoff is held by a live receiver")
	ErrTaskHandoffTaskMismatch           = errors.New("task handoff task mismatch")
	ErrTaskHandoffGoalNotHeld            = errors.New("task handoff requires the goal's handoff: caller does not hold an open received handoff for goal")
	ErrTaskHandoffAlreadyOpen            = errors.New("task handoff already open")
	ErrTaskHandoffAmbiguous              = errors.New("multiple task handoffs pending receipt")
	ErrTaskHandoffReportEmpty            = errors.New("task handoff needs a complete_report describing what was done, what was verified, and paths changed; without it, the record cannot distinguish completion from no report")
	ErrTaskHandoffReviewState            = errors.New("task handoff review is not in the required state")
	ErrTaskHandoffReviewReviewerMismatch = errors.New("task handoff review reviewer mismatch")
	ErrTaskHandoffReviewReportEmpty      = errors.New("task handoff review needs a non-empty report")
	ErrTaskHandoffRecoveryState          = errors.New("task handoff recovery is not in the required state")
)

const (
	taskHandoffReclaimedReport = "セッションが停止した"
	taskHandoffReleasedReport  = "作業ロックを手放した（報告者なし）"
)

func taskWorkflowEventScope(ctx context.Context, q *sqlcgen.Queries, taskID int64) (projectID, goalID int64, err error) {
	goalID, err = q.GetTaskGoalID(ctx, taskID)
	if err != nil {
		return 0, 0, fmt.Errorf("find goal for task workflow event: %w", err)
	}
	projectID, err = q.GetGoalProjectID(ctx, goalID)
	if err != nil {
		return 0, 0, fmt.Errorf("find project for task workflow event: %w", err)
	}
	return projectID, goalID, nil
}

// TaskHandoff records one delegation between agents. Each event timestamp is
// independent so a partial handoff remains observable.
type TaskHandoff struct {
	ID string
	// MonitorLost says the worker holding this handoff has no monitor left.
	// An open handoff otherwise reads as work in progress, which stops being
	// true the moment the pane behind it is gone.
	MonitorLost bool
	// GoalID is the task's goal. The watch scopes a subcommander by goal and
	// asks every handoff which goal it belongs to; a task handoff that cannot
	// answer is invisible to that scope, and an executor holding it stops
	// counting as work in progress.
	GoalID                    int64
	TaskID                    int64
	RequestedBy               int64
	ReceivedBy                int64
	RequestReport             string
	CompleteReport            string
	ReviewRequestedBy         int64
	ReviewRequestedAt         *time.Time
	ReviewRequestReport       string
	ReviewReceivedBy          int64
	ReviewReceivedAt          *time.Time
	ReviewRejectedAt          *time.Time
	ReviewRejectReport        string
	ReviewRejectionReceivedBy int64
	ReviewRejectionReceivedAt *time.Time
	RecoveredAt               *time.Time
	RecoveryReport            string
	RequestedAt               *time.Time
	ReceivedAt                *time.Time
	CompletedReportAt         *time.Time
}

func taskHandoffFromRow(row sqlcgen.TaskHandoff) (TaskHandoff, error) {
	handoff := TaskHandoff{
		ID:                        row.ID,
		TaskID:                    row.TaskID,
		RequestedBy:               nullableAgentSessionID(row.RequestedBy),
		ReceivedBy:                nullableAgentSessionID(row.ReceivedBy),
		RequestReport:             row.RequestReport.String,
		CompleteReport:            row.CompleteReport.String,
		ReviewRequestedBy:         nullableAgentSessionID(row.ReviewRequestedBy),
		ReviewRequestReport:       row.ReviewRequestReport.String,
		ReviewReceivedBy:          nullableAgentSessionID(row.ReviewReceivedBy),
		ReviewRejectReport:        row.ReviewRejectReport.String,
		ReviewRejectionReceivedBy: nullableAgentSessionID(row.ReviewRejectionReceivedBy),
		RecoveryReport:            row.RecoveryReport.String,
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
	if handoff.ReviewRequestedAt, err = parseTaskHandoffTime("review_requested_at", row.ReviewRequestedAt); err != nil {
		return TaskHandoff{}, err
	}
	if handoff.ReviewReceivedAt, err = parseTaskHandoffTime("review_received_at", row.ReviewReceivedAt); err != nil {
		return TaskHandoff{}, err
	}
	if handoff.ReviewRejectedAt, err = parseTaskHandoffTime("review_rejected_at", row.ReviewRejectedAt); err != nil {
		return TaskHandoff{}, err
	}
	if handoff.ReviewRejectionReceivedAt, err = parseTaskHandoffTime("review_rejection_received_at", row.ReviewRejectionReceivedAt); err != nil {
		return TaskHandoff{}, err
	}
	if handoff.RecoveredAt, err = parseTaskHandoffTime("recovered_at", row.RecoveredAt); err != nil {
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

// taskHandoffReceiverID is for an error message, so an unreadable row answers
// zero rather than replacing the refusal with a lookup failure.
func taskHandoffRefusal(ctx context.Context, q *sqlcgen.Queries, handoffID string) (requested bool, receiver int64) {
	handoff, err := q.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return false, 0
	}
	if handoff.ReceivedBy.Valid {
		receiver = handoff.ReceivedBy.Int64
	}
	return handoff.RequestedAt.Valid, receiver
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
	return s.requireGoalHandoffHolder(ctx, goalID, requestedBy)
}

func (s *Store) requireGoalHandoffHolder(ctx context.Context, goalID, holderID int64) error {
	goalHandoff, err := s.openGoalHandoff(ctx, goalID)
	if err != nil {
		return fmt.Errorf("find live goal handoff for goal %d: %w", goalID, err)
	}
	if goalHandoff != nil && goalHandoff.ReceivedBy == holderID && holderID != 0 && claimIsRunning(ctx, s, holderID) {
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
	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return fmt.Errorf("list open task handoffs: %w", err)
	}

	var open *TaskHandoff
	for i := range handoffs {
		if handoffs[i].CompletedReportAt != nil || handoffs[i].RecoveredAt != nil || handoffs[i].ID == handoffID {
			continue
		}
		if open != nil {
			return fmt.Errorf("%w: task %d has multiple open handoffs", ErrTaskHandoffAlreadyOpen, taskID)
		}
		open = &handoffs[i]
	}
	if open == nil {
		return nil
	}

	ownerID := open.ReceivedBy
	if ownerID == 0 {
		// An unreceived handoff has no receiver to inspect, so the requester
		// is the only available liveness signal.
		ownerID = open.RequestedBy
	}
	if ownerID == 0 || !claimIsDefinitelyDead(ctx, s, ownerID) {
		return fmt.Errorf("%w: task %d has a live handoff owner", ErrTaskHandoffAlreadyOpen, taskID)
	}
	if _, err := s.CompleteTaskHandoff(ctx, open.ID, taskID, taskHandoffReclaimedReport); err != nil {
		return fmt.Errorf("reclaim task handoff %q: %w", open.ID, err)
	}
	return nil
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
		if handoffs[i].ReceivedAt == nil || handoffs[i].CompletedReportAt != nil || handoffs[i].RecoveredAt != nil {
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
	if err := s.requireUndiscardedSession(ctx, requestedBy); err != nil {
		return TaskHandoff{}, err
	}
	if err := s.ensureTaskHandoffTaskForRequest(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	if existing, err := s.GetTaskHandoff(ctx, handoffID); err == nil {
		if existing.RecoveredAt != nil {
			return TaskHandoff{}, fmt.Errorf("task handoff %q was recovered and cannot be reused: %w", handoffID, ErrTaskHandoffRecoveryState)
		}
	} else if !errors.Is(err, ErrTaskHandoffNotFound) {
		return TaskHandoff{}, err
	}
	if requireLiveClaim {
		if err := s.requireGoalHandoffForTask(ctx, taskID, requestedBy); err != nil {
			return TaskHandoff{}, err
		}
	}
	if err := s.reclaimOpenTaskHandoff(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	now := formatTimestamp(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff request tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	if err := q.RequestTaskHandoff(ctx, sqlcgen.RequestTaskHandoffParams{
		ID:            handoffID,
		TaskID:        taskID,
		RequestedBy:   sql.NullInt64{Int64: requestedBy, Valid: requestedBy != 0},
		RequestedAt:   sql.NullString{String: now, Valid: true},
		RequestReport: sql.NullString{String: requestReport, Valid: requestReport != ""},
	}); err != nil {
		return TaskHandoff{}, fmt.Errorf("request task handoff: %w", err)
	}
	statusResult, err := q.UpdateTaskStatus(ctx, sqlcgen.UpdateTaskStatusParams{
		Status:    "todo",
		UpdatedAt: now,
		ID:        taskID,
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("set task todo after handoff request: %w", err)
	}
	if affected, err := statusResult.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("set task todo rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	projectID, goalID, err := taskWorkflowEventScope(ctx, q, taskID)
	if err != nil {
		return TaskHandoff{}, err
	}
	event := DecisionEvent{
		Name: EventTaskHandoffRequest,
		Data: HandoffEvent{ProjectID: projectID, GoalID: goalID, TaskID: taskID, HandoffID: handoffID, RequestedBy: requestedBy, RequestReport: requestReport},
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff request: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetTaskHandoff(ctx, handoffID)
}

// ReceiveTaskHandoff records the receipt side of a requested handoff.
func (s *Store) ReceiveTaskHandoff(ctx context.Context, handoffID string, taskID int64, receivedBy int64) (TaskHandoff, error) {
	if err := s.requireUndiscardedSession(ctx, receivedBy); err != nil {
		return TaskHandoff{}, err
	}
	if err := s.ensureTaskHandoffTask(ctx, handoffID, taskID); err != nil {
		return TaskHandoff{}, err
	}
	now := formatTimestamp(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff receive tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	result, err := q.ReceiveTaskHandoff(ctx, sqlcgen.ReceiveTaskHandoffParams{
		ID:          handoffID,
		TaskID:      taskID,
		ReceivedBy:  sql.NullInt64{Int64: receivedBy, Valid: receivedBy != 0},
		ReceivedAt:  sql.NullString{String: now, Valid: true},
		LeaseCutoff: leaseCutoff(),
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("receive task handoff: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("receive task handoff rows affected: %w", err)
	}
	if n == 0 {
		if requested, receiver := taskHandoffRefusal(ctx, q, handoffID); requested && receiver != 0 {
			return TaskHandoff{}, fmt.Errorf("task handoff %s is held by session %d, whose lease is still being renewed: %w",
				handoffID, receiver, ErrTaskHandoffLiveReceiver)
		}
		return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffNotFound, handoffID)
	}
	statusResult, err := q.UpdateTaskStatus(ctx, sqlcgen.UpdateTaskStatusParams{
		Status:    "doing",
		UpdatedAt: now,
		ID:        taskID,
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("set task doing after handoff receive: %w", err)
	}
	if affected, err := statusResult.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("set task doing rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	projectID, goalID, err := taskWorkflowEventScope(ctx, q, taskID)
	if err != nil {
		return TaskHandoff{}, err
	}
	event := DecisionEvent{
		Name: EventTaskHandoffReceive,
		Data: HandoffEvent{ProjectID: projectID, GoalID: goalID, TaskID: taskID, HandoffID: handoffID, ReceivedBy: receivedBy},
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff receive: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetTaskHandoff(ctx, handoffID)
}

// RequestTaskHandoffReview starts the review state for a received task handoff.
// The task receiver is the only session allowed to submit the work for review.
func (s *Store) RequestTaskHandoffReview(ctx context.Context, handoffID string, taskID, requestedBy int64, reviewRequestReport string) (TaskHandoff, error) {
	if err := s.requireUndiscardedSession(ctx, requestedBy); err != nil {
		return TaskHandoff{}, err
	}
	if completeReportIsEmpty(reviewRequestReport) {
		return TaskHandoff{}, ErrTaskHandoffReviewReportEmpty
	}
	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	if handoff.TaskID != taskID {
		return TaskHandoff{}, fmt.Errorf("%w: %q belongs to task %d, not %d", ErrTaskHandoffTaskMismatch, handoffID, handoff.TaskID, taskID)
	}
	if handoff.RequestedAt == nil || handoff.ReceivedAt == nil || handoff.CompletedReportAt != nil || handoff.RecoveredAt != nil {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	if handoff.ReceivedBy == 0 || handoff.ReceivedBy != requestedBy {
		return TaskHandoff{}, fmt.Errorf("%w: task handoff receiver %d cannot request review as %d", ErrTaskHandoffReviewReviewerMismatch, handoff.ReceivedBy, requestedBy)
	}
	if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil {
		return TaskHandoff{}, fmt.Errorf("%w: task handoff review is already open", ErrTaskHandoffReviewState)
	}

	now := formatTimestamp(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff review request tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	result, err := q.RequestTaskHandoffReview(ctx, sqlcgen.RequestTaskHandoffReviewParams{
		ReviewRequestedBy:   sql.NullInt64{Int64: requestedBy, Valid: true},
		ReviewRequestedAt:   sql.NullString{String: now, Valid: true},
		ReviewRequestReport: sql.NullString{String: reviewRequestReport, Valid: true},
		ID:                  handoffID,
		TaskID:              taskID,
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("request task handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("request task handoff review rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	statusResult, err := q.UpdateTaskStatus(ctx, sqlcgen.UpdateTaskStatusParams{
		Status:    "review",
		UpdatedAt: now,
		ID:        taskID,
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("set task review after handoff review request: %w", err)
	}
	if affected, err := statusResult.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("set task review rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	projectID, goalID, err := taskWorkflowEventScope(ctx, q, taskID)
	if err != nil {
		return TaskHandoff{}, err
	}
	event := DecisionEvent{
		Name: EventTaskHandoffReviewRequest,
		Data: HandoffReviewEvent{ProjectID: projectID, GoalID: goalID, TaskID: taskID, HandoffID: handoffID, ReviewerID: requestedBy, ReviewRequestReport: reviewRequestReport},
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff review request: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetTaskHandoff(ctx, handoffID)
}

// ReceiveTaskHandoffReview records the reviewer's receipt. The original
// requester is the only reviewer for a task handoff.
func (s *Store) ReceiveTaskHandoffReview(ctx context.Context, handoffID string, taskID, receivedBy int64) (TaskHandoff, error) {
	if err := s.requireUndiscardedSession(ctx, receivedBy); err != nil {
		return TaskHandoff{}, err
	}
	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	if handoff.TaskID != taskID {
		return TaskHandoff{}, fmt.Errorf("%w: %q belongs to task %d, not %d", ErrTaskHandoffTaskMismatch, handoffID, handoff.TaskID, taskID)
	}
	if handoff.ReviewRequestedAt == nil || handoff.CompletedReportAt != nil || handoff.RecoveredAt != nil || handoff.ReviewReceivedAt != nil {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	if receivedBy == 0 || handoff.RequestedBy != receivedBy {
		if err := s.requireGoalHandoffForTask(ctx, taskID, receivedBy); err != nil {
			return TaskHandoff{}, fmt.Errorf("%w: task handoff reviewer %d is not requester %d or current goal holder: %v", ErrTaskHandoffReviewReviewerMismatch, receivedBy, handoff.RequestedBy, err)
		}
	}

	now := formatTimestamp(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff review receive tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	result, err := q.ReceiveTaskHandoffReview(ctx, sqlcgen.ReceiveTaskHandoffReviewParams{
		ReviewReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: true},
		ReviewReceivedAt: sql.NullString{String: now, Valid: true},
		ID:               handoffID,
		TaskID:           taskID,
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("receive task handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("receive task handoff review rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	projectID, goalID, err := taskWorkflowEventScope(ctx, q, taskID)
	if err != nil {
		return TaskHandoff{}, err
	}
	event := DecisionEvent{
		Name: EventTaskHandoffReviewReceive,
		Data: HandoffReviewEvent{ProjectID: projectID, GoalID: goalID, TaskID: taskID, HandoffID: handoffID, ReviewerID: receivedBy},
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff review receive: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetTaskHandoff(ctx, handoffID)
}

// RecoverTaskHandoff replaces a definitely stale task requester/receiver or
// reopens a review receipt held by a definitely stale reviewer. The current
// goal handoff holder is the only caller allowed to recover task work.
func (s *Store) RecoverTaskHandoff(ctx context.Context, handoffID string, taskID, callerID int64, reason string) (TaskHandoff, error) {
	if err := s.requireUndiscardedSession(ctx, callerID); err != nil {
		return TaskHandoff{}, err
	}
	if err := s.requireGoalHandoffForTask(ctx, taskID, callerID); err != nil {
		return TaskHandoff{}, fmt.Errorf("recover task handoff requires the current goal holder: %w", err)
	}
	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	if handoff.TaskID != taskID || handoff.CompletedReportAt != nil {
		return TaskHandoff{}, ErrTaskHandoffRecoveryState
	}
	if handoff.RecoveredAt != nil {
		return handoff, nil
	}

	staleSessionID := int64(0)
	phase := ""
	switch {
	case handoff.ReviewReceivedAt != nil:
		staleSessionID = handoff.ReviewReceivedBy
		phase = "review_received"
	case handoff.ReceivedAt != nil:
		staleSessionID = handoff.ReceivedBy
		phase = "received"
	case handoff.RequestedAt != nil:
		staleSessionID = handoff.RequestedBy
		phase = "requested"
	default:
		return TaskHandoff{}, ErrTaskHandoffRecoveryState
	}
	if staleSessionID == 0 || staleSessionID == callerID {
		return TaskHandoff{}, fmt.Errorf("task handoff recovery caller cannot replace session %d: %w", staleSessionID, ErrTaskHandoffReviewReviewerMismatch)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff recovery: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	sessionProof, err := canRecoverSessionInTx(ctx, q, staleSessionID)
	if err != nil {
		return TaskHandoff{}, err
	}
	var result sql.Result
	recoveredAt := sql.NullString{String: formatTimestamp(time.Now()), Valid: true}
	recoveryReport := sql.NullString{String: reason, Valid: true}
	switch phase {
	case "requested":
		result, err = q.RecoverTaskHandoffRequester(ctx, sqlcgen.RecoverTaskHandoffRequesterParams{
			RecoveredAt: recoveredAt, RecoveryReport: recoveryReport, ID: handoffID, TaskID: taskID,
			RequestedBy: sql.NullInt64{Int64: staleSessionID, Valid: true}, Pid: sessionProof.PID, StartedAt: sessionProof.StartedAt,
		})
	case "received":
		result, err = q.RecoverTaskHandoffReceiver(ctx, sqlcgen.RecoverTaskHandoffReceiverParams{
			RecoveredAt: recoveredAt, RecoveryReport: recoveryReport, ID: handoffID, TaskID: taskID,
			ReceivedBy: sql.NullInt64{Int64: staleSessionID, Valid: true}, Pid: sessionProof.PID, StartedAt: sessionProof.StartedAt,
		})
	case "review_received":
		result, err = q.RecoverTaskHandoffReview(ctx, sqlcgen.RecoverTaskHandoffReviewParams{
			ID: handoffID, TaskID: taskID, ReviewReceivedBy: sql.NullInt64{Int64: staleSessionID, Valid: true},
			Pid: sessionProof.PID, StartedAt: sessionProof.StartedAt,
		})
	}
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("recover task handoff %s: %w", phase, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("inspect task handoff recovery: %w", err)
	} else if affected != 1 {
		return TaskHandoff{}, ErrTaskHandoffRecoveryState
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff recovery: %w", err)
	}
	return s.GetTaskHandoff(ctx, handoffID)
}

// RejectTaskHandoffReview returns a task handoff to doing without releasing
// its work claim. The reviewer receipt is cleared so a later review request
// can start a fresh review cycle.
func (s *Store) RejectTaskHandoffReview(ctx context.Context, handoffID string, taskID, reviewerID int64, rejectReport string) (TaskHandoff, error) {
	if err := s.requireUndiscardedSession(ctx, reviewerID); err != nil {
		return TaskHandoff{}, err
	}
	if completeReportIsEmpty(rejectReport) {
		return TaskHandoff{}, ErrTaskHandoffReviewReportEmpty
	}
	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	if handoff.TaskID != taskID {
		return TaskHandoff{}, fmt.Errorf("%w: %q belongs to task %d, not %d", ErrTaskHandoffTaskMismatch, handoffID, handoff.TaskID, taskID)
	}
	if handoff.ReviewReceivedAt == nil || handoff.CompletedReportAt != nil || handoff.RecoveredAt != nil {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	if handoff.ReviewReceivedBy != reviewerID {
		return TaskHandoff{}, fmt.Errorf("%w: task handoff reviewer %d is not recorded reviewer %d", ErrTaskHandoffReviewReviewerMismatch, reviewerID, handoff.ReviewReceivedBy)
	}

	now := formatTimestamp(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff review rejection tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	result, err := q.RejectTaskHandoffReview(ctx, sqlcgen.RejectTaskHandoffReviewParams{
		ReviewRejectedAt:   sql.NullString{String: now, Valid: true},
		ReviewRejectReport: sql.NullString{String: rejectReport, Valid: true},
		ID:                 handoffID,
		TaskID:             taskID,
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("reject task handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("reject task handoff review rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	statusResult, err := q.UpdateTaskStatus(ctx, sqlcgen.UpdateTaskStatusParams{
		Status:    "doing",
		UpdatedAt: now,
		ID:        taskID,
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("set task doing after handoff review rejection: %w", err)
	}
	if affected, err := statusResult.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("set task doing rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	projectID, goalID, err := taskWorkflowEventScope(ctx, q, taskID)
	if err != nil {
		return TaskHandoff{}, err
	}
	event := DecisionEvent{
		Name: EventTaskHandoffReviewReject,
		Data: HandoffReviewEvent{ProjectID: projectID, GoalID: goalID, TaskID: taskID, HandoffID: handoffID, ReviewerID: reviewerID, ReviewRejectReport: rejectReport},
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff review rejection: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetTaskHandoff(ctx, handoffID)
}

// ReceiveTaskHandoffReviewRejection records that the work submitter received a review rejection.
func (s *Store) ReceiveTaskHandoffReviewRejection(ctx context.Context, handoffID string, taskID, receivedBy int64) (TaskHandoff, error) {
	if err := s.requireUndiscardedSession(ctx, receivedBy); err != nil {
		return TaskHandoff{}, err
	}
	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	if handoff.TaskID != taskID {
		return TaskHandoff{}, fmt.Errorf("%w: %q belongs to task %d, not %d", ErrTaskHandoffTaskMismatch, handoffID, handoff.TaskID, taskID)
	}
	if handoff.ReviewRejectedAt == nil || handoff.CompletedReportAt != nil || handoff.RecoveredAt != nil || handoff.ReceivedBy != receivedBy || receivedBy == 0 {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	result, err := sqlcgen.New(s.db).ReceiveTaskHandoffReviewRejection(ctx, sqlcgen.ReceiveTaskHandoffReviewRejectionParams{ReviewRejectionReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: true}, ReviewRejectionReceivedAt: sql.NullString{String: formatTimestamp(time.Now()), Valid: true}, ID: handoffID, TaskID: taskID, ReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: true}})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("receive task handoff review rejection: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	return s.GetTaskHandoff(ctx, handoffID)
}

// CompleteTaskHandoffByReviewer closes a task handoff and transitions its task
// to done atomically after the recorded reviewer has accepted it.
func (s *Store) CompleteTaskHandoffByReviewer(ctx context.Context, handoffID string, taskID, reviewerID int64, completeReport string) (TaskHandoff, error) {
	if err := s.requireUndiscardedSession(ctx, reviewerID); err != nil {
		return TaskHandoff{}, err
	}
	if completeReportIsEmpty(completeReport) {
		return TaskHandoff{}, ErrTaskHandoffReportEmpty
	}
	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	if handoff.TaskID != taskID {
		return TaskHandoff{}, fmt.Errorf("%w: %q belongs to task %d, not %d", ErrTaskHandoffTaskMismatch, handoffID, handoff.TaskID, taskID)
	}
	if handoff.ReviewReceivedAt == nil || handoff.CompletedReportAt != nil || handoff.RecoveredAt != nil {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	if handoff.ReviewReceivedBy != reviewerID {
		return TaskHandoff{}, fmt.Errorf("%w: task handoff reviewer %d is not recorded reviewer %d", ErrTaskHandoffReviewReviewerMismatch, reviewerID, handoff.ReviewReceivedBy)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff review completion tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	openDecisions, err := q.CountOpenDecisionsForTask(ctx, sql.NullInt64{Int64: taskID, Valid: true})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("count open decisions: %w", err)
	}
	if openDecisions > 0 {
		decisions, err := listOpenTaskDecisions(ctx, q, taskID)
		if err != nil {
			return TaskHandoff{}, fmt.Errorf("list open decisions: %w", err)
		}
		return TaskHandoff{}, taskHasOpenDecisionsError(taskID, decisions)
	}

	now := formatTimestamp(time.Now())
	result, err := q.CompleteTaskHandoffByReviewer(ctx, sqlcgen.CompleteTaskHandoffByReviewerParams{
		CompletedReportAt: sql.NullString{String: now, Valid: true},
		CompleteReport:    sql.NullString{String: completeReport, Valid: true},
		ID:                handoffID,
		TaskID:            taskID,
		ReviewReceivedBy:  sql.NullInt64{Int64: reviewerID, Valid: true},
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("complete task handoff by reviewer: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("complete task handoff by reviewer rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, ErrTaskHandoffReviewState
	}
	statusResult, err := q.UpdateTaskStatus(ctx, sqlcgen.UpdateTaskStatusParams{
		Status:    "done",
		UpdatedAt: now,
		ID:        taskID,
	})
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("set task done after handoff review completion: %w", err)
	}
	if affected, err := statusResult.RowsAffected(); err != nil {
		return TaskHandoff{}, fmt.Errorf("set task done rows affected: %w", err)
	} else if affected == 0 {
		return TaskHandoff{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	projectID, goalID, err := taskWorkflowEventScope(ctx, q, taskID)
	if err != nil {
		return TaskHandoff{}, err
	}
	event := DecisionEvent{
		Name: EventHandoffReported,
		Data: WakeupEvent{WakeupID: NewWakeupID(), ProjectID: projectID, GoalID: goalID, TaskID: taskID, HandoffID: handoffID, CompleteReport: completeReport},
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff review completion: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetTaskHandoff(ctx, handoffID)
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
		if handoff.RequestedAt != nil && handoff.ReceivedAt == nil && handoff.RecoveredAt == nil {
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
		if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
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
	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	if handoff.ReviewRequestedAt != nil && completeReport != taskHandoffReclaimedReport && completeReport != taskHandoffReleasedReport {
		return TaskHandoff{}, fmt.Errorf("%w: complete the task handoff through its recorded reviewer", ErrTaskHandoffReviewState)
	}
	now := formatTimestamp(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("begin task handoff completion tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	result, err := q.CompleteTaskHandoff(ctx, sqlcgen.CompleteTaskHandoffParams{
		ID:                handoffID,
		TaskID:            taskID,
		CompletedReportAt: sql.NullString{String: now, Valid: true},
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
		if handoff.CompletedReportAt != nil {
			return TaskHandoff{}, fmt.Errorf("task handoff %q is already reported; use another path to add a report after completion", handoffID)
		}
		return TaskHandoff{}, fmt.Errorf("%w: %s", ErrTaskHandoffNotFound, handoffID)
	}
	var event DecisionEvent
	// Claim locks have no delegate report, so their completion is not reportable.
	if handoffIsDelegation(handoff.RequestedBy, handoff.ReceivedBy) {
		projectID, goalID, scopeErr := taskWorkflowEventScope(ctx, q, taskID)
		if scopeErr != nil {
			return TaskHandoff{}, scopeErr
		}
		event = DecisionEvent{
			Name: EventHandoffReported,
			Data: WakeupEvent{
				WakeupID:       NewWakeupID(),
				ProjectID:      projectID,
				GoalID:         goalID,
				TaskID:         taskID,
				HandoffID:      handoffID,
				CompleteReport: completeReport,
			},
		}
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoff{}, fmt.Errorf("commit task handoff completion: %w", err)
	}
	completed, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		return TaskHandoff{}, err
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return completed, nil
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
	result, err := sqlcgen.New(s.db).AmendTaskHandoffReport(ctx, sqlcgen.AmendTaskHandoffReportParams{
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
		return TaskHandoff{}, fmt.Errorf("task handoff %q is not yet completed; use atct_task_handoff_complete", handoffID)
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
	handoff, err := taskHandoffFromRow(row)
	if err != nil {
		return TaskHandoff{}, err
	}
	// Filled on every read, so nothing downstream has to know which of them
	// happens to populate it.
	goalID, err := sqlcgen.New(s.db).GetTaskGoalID(ctx, handoff.TaskID)
	if err != nil {
		return TaskHandoff{}, fmt.Errorf("find goal for task handoff %q: %w", handoffID, err)
	}
	handoff.GoalID = goalID
	return handoff, nil
}

// ListTaskHandoffs returns all handoffs for a task, including partial rows.
func (s *Store) ListTaskHandoffs(ctx context.Context, taskID int64) ([]TaskHandoff, error) {
	queries := sqlcgen.New(s.db)
	rows, err := queries.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task handoffs: %w", err)
	}
	goalID, err := queries.GetTaskGoalID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("find goal for task %d handoffs: %w", taskID, err)
	}

	handoffs := make([]TaskHandoff, 0, len(rows))
	for _, row := range rows {
		handoff, err := taskHandoffFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("parse task handoff: %w", err)
		}
		handoff.GoalID = goalID
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
