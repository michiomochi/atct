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
	ErrGoalHandoffNotFound               = errors.New("goal handoff not found")
	ErrGoalHandoffGoalMismatch           = errors.New("goal handoff goal mismatch")
	ErrGoalHandoffProjectNotHeld         = errors.New("goal handoff requires the project claim: caller does not hold a live claim on project")
	ErrGoalHandoffAlreadyOpen            = errors.New("goal handoff already open")
	ErrGoalHandoffAmbiguous              = errors.New("multiple goal handoffs pending receipt")
	ErrGoalHandoffReportEmpty            = errors.New("goal handoff needs a complete_report describing what was done, what was verified, and paths changed; without it, the record cannot distinguish completion from no report")
	ErrGoalHandoffReviewState            = errors.New("goal handoff review is not in the required state")
	ErrGoalHandoffReviewReviewerMismatch = errors.New("goal handoff review reviewer mismatch")
	ErrGoalHandoffReviewReportEmpty      = errors.New("goal handoff review needs a non-empty report")
	ErrPlanHandoffNotFound               = errors.New("plan handoff not found")
	ErrPlanHandoffGoalMismatch           = errors.New("plan handoff goal mismatch")
	ErrPlanHandoffReviewState            = errors.New("plan handoff review is not in the required state")
	ErrPlanHandoffReviewerMismatch       = errors.New("plan handoff review reviewer mismatch")
	ErrPlanHandoffReviewReportEmpty      = errors.New("plan handoff review needs a non-empty report")
)

const (
	goalHandoffReclaimedReport = "セッションが停止した"
	goalHandoffReleasedReport  = "ゴールを手放した（報告者なし）"
)

// GoalHandoff records one delegation between agents. Each event timestamp is
// independent so a partial handoff remains observable.
type GoalHandoff struct {
	ID                  string
	GoalID              int64
	RequestedBy         int64
	ReceivedBy          int64
	RequestReport       string
	CompleteReport      string
	ReviewRequestedBy   int64
	ReviewRequestedAt   *time.Time
	ReviewRequestReport string
	ReviewReceivedBy    int64
	ReviewReceivedAt    *time.Time
	ReviewRejectedAt    *time.Time
	ReviewRejectReport  string
	RequestedAt         *time.Time
	ReceivedAt          *time.Time
	CompletedReportAt   *time.Time
}

// PlanHandoff records a plan review routed from the goal handoff receiver back
// to the goal handoff requester. A plan has no task claim or task status.
type PlanHandoff struct {
	ID                  string
	GoalID              int64
	ReviewRequestedBy   int64
	ReviewRequestedAt   *time.Time
	ReviewRequestReport string
	ReviewReceivedBy    int64
	ReviewReceivedAt    *time.Time
	ReviewRejectedAt    *time.Time
	ReviewRejectReport  string
	CompleteReport      string
	CompletedReportAt   *time.Time
}

// GoalSession identifies an agent session that received a handoff for a goal.
type GoalSession struct {
	SessionKey  string
	Role        string
	HandoffOpen bool
}

func goalHandoffFromRow(row sqlcgen.GoalHandoff) (GoalHandoff, error) {
	handoff := GoalHandoff{
		ID:                  row.ID,
		GoalID:              row.GoalID,
		RequestedBy:         nullableAgentSessionID(row.RequestedBy),
		ReceivedBy:          nullableAgentSessionID(row.ReceivedBy),
		RequestReport:       row.RequestReport.String,
		CompleteReport:      row.CompleteReport.String,
		ReviewRequestedBy:   nullableAgentSessionID(row.ReviewRequestedBy),
		ReviewRequestReport: row.ReviewRequestReport.String,
		ReviewReceivedBy:    nullableAgentSessionID(row.ReviewReceivedBy),
		ReviewRejectReport:  row.ReviewRejectReport.String,
	}
	var err error
	if handoff.RequestedAt, err = parseGoalHandoffTime("requested_at", row.RequestedAt); err != nil {
		return GoalHandoff{}, err
	}
	if handoff.ReceivedAt, err = parseGoalHandoffTime("received_at", row.ReceivedAt); err != nil {
		return GoalHandoff{}, err
	}
	if handoff.CompletedReportAt, err = parseGoalHandoffTime("completed_report_at", row.CompletedReportAt); err != nil {
		return GoalHandoff{}, err
	}
	if handoff.ReviewRequestedAt, err = parseGoalHandoffTime("review_requested_at", row.ReviewRequestedAt); err != nil {
		return GoalHandoff{}, err
	}
	if handoff.ReviewReceivedAt, err = parseGoalHandoffTime("review_received_at", row.ReviewReceivedAt); err != nil {
		return GoalHandoff{}, err
	}
	if handoff.ReviewRejectedAt, err = parseGoalHandoffTime("review_rejected_at", row.ReviewRejectedAt); err != nil {
		return GoalHandoff{}, err
	}
	return handoff, nil
}

func parseGoalHandoffTime(column string, value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse goal handoff %s: %w", column, err)
	}
	return &parsed, nil
}

func planHandoffFromRow(row sqlcgen.PlanHandoff) (PlanHandoff, error) {
	handoff := PlanHandoff{
		ID:                  row.ID,
		GoalID:              row.GoalID,
		ReviewRequestedBy:   nullableAgentSessionID(row.ReviewRequestedBy),
		ReviewRequestReport: row.ReviewRequestReport.String,
		ReviewReceivedBy:    nullableAgentSessionID(row.ReviewReceivedBy),
		ReviewRejectReport:  row.ReviewRejectReport.String,
		CompleteReport:      row.CompleteReport.String,
	}
	var err error
	if handoff.ReviewRequestedAt, err = parseGoalHandoffTime("plan review_requested_at", row.ReviewRequestedAt); err != nil {
		return PlanHandoff{}, err
	}
	if handoff.ReviewReceivedAt, err = parseGoalHandoffTime("plan review_received_at", row.ReviewReceivedAt); err != nil {
		return PlanHandoff{}, err
	}
	if handoff.ReviewRejectedAt, err = parseGoalHandoffTime("plan review_rejected_at", row.ReviewRejectedAt); err != nil {
		return PlanHandoff{}, err
	}
	if handoff.CompletedReportAt, err = parseGoalHandoffTime("plan completed_report_at", row.CompletedReportAt); err != nil {
		return PlanHandoff{}, err
	}
	return handoff, nil
}

func (s *Store) ensureGoalHandoffGoal(ctx context.Context, handoffID string, goalID int64) error {
	existingGoalID, err := sqlcgen.New(s.db).GetGoalHandoffGoalID(ctx, handoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %q", ErrGoalHandoffNotFound, handoffID)
	}
	if err != nil {
		return fmt.Errorf("find goal handoff %q: %w", handoffID, err)
	}
	if existingGoalID != goalID {
		return fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrGoalHandoffGoalMismatch, handoffID, existingGoalID, goalID)
	}
	return nil
}

func (s *Store) ensureGoalHandoffGoalForRequest(ctx context.Context, handoffID string, goalID int64) error {
	err := s.ensureGoalHandoffGoal(ctx, handoffID, goalID)
	if errors.Is(err, ErrGoalHandoffNotFound) {
		return nil
	}
	return err
}

func (s *Store) requireProjectClaimForGoal(ctx context.Context, goalID int64, requestedBy int64) error {
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return fmt.Errorf("find goal %d: %w", goalID, err)
	}

	project, err := sqlcgen.New(s.db).GetProject(ctx, goal.ProjectID)
	if err != nil {
		return fmt.Errorf("find project for goal %d: %w", goalID, err)
	}

	projectOwner := project.ClaimedBy
	if projectOwner == requestedBy && projectOwner != 0 && claimIsRunning(ctx, s, projectOwner) {
		return nil
	}

	return fmt.Errorf("%w: %d", ErrGoalHandoffProjectNotHeld, project.ID)
}

// reclaimOpenGoalHandoff enforces the one-open-handoff rule before inserting a
// new request. A retry for the same handoff ID is allowed to fill an existing
// receipt-only row. For a different ID, only a handoff whose owner is no longer
// running may be reclaimed; an unknown owner is treated as active because its
// liveness cannot be disproved.
func (s *Store) reclaimOpenGoalHandoff(ctx context.Context, handoffID string, goalID int64) error {
	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list open goal handoffs: %w", err)
	}

	var open *GoalHandoff
	for i := range handoffs {
		if handoffs[i].CompletedReportAt != nil || handoffs[i].ID == handoffID {
			continue
		}
		if open != nil {
			return fmt.Errorf("%w: goal %d has multiple open handoffs", ErrGoalHandoffAlreadyOpen, goalID)
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
		return fmt.Errorf("%w: goal %d has a live handoff owner", ErrGoalHandoffAlreadyOpen, goalID)
	}
	if _, err := s.CompleteGoalHandoff(ctx, open.ID, goalID, goalHandoffReclaimedReport); err != nil {
		return fmt.Errorf("reclaim goal handoff %q: %w", open.ID, err)
	}
	return nil
}

// openGoalHandoff returns the goal's single received, incomplete handoff.
// Request-only handoffs are not claims and therefore do not authorize goal
// release.
func (s *Store) openGoalHandoff(ctx context.Context, goalID int64) (*GoalHandoff, error) {
	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return nil, err
	}

	var open *GoalHandoff
	for i := range handoffs {
		if handoffs[i].ReceivedAt == nil || handoffs[i].CompletedReportAt != nil {
			continue
		}
		if open != nil {
			return nil, fmt.Errorf("%w: goal %d has multiple open handoffs", ErrGoalHandoffAmbiguous, goalID)
		}
		candidate := handoffs[i]
		open = &candidate
	}
	return open, nil
}

// RequestGoalHandoff records the request side of a handoff. It requires the
// requester to hold a live claim on the goal's project; receipt and completion
// are separate calls.
func (s *Store) RequestGoalHandoff(ctx context.Context, handoffID string, goalID int64, requestedBy int64, requestReport string) (GoalHandoff, error) {
	return s.requestGoalHandoff(ctx, handoffID, goalID, requestedBy, requestReport, true)
}

func (s *Store) requestGoalHandoffForClaim(ctx context.Context, handoffID string, goalID int64, requestedBy int64) (GoalHandoff, error) {
	return s.requestGoalHandoff(ctx, handoffID, goalID, requestedBy, "", false)
}

func (s *Store) requestGoalHandoff(ctx context.Context, handoffID string, goalID int64, requestedBy int64, requestReport string, requireLiveClaim bool) (GoalHandoff, error) {
	if err := s.ensureGoalHandoffGoalForRequest(ctx, handoffID, goalID); err != nil {
		return GoalHandoff{}, err
	}
	if requireLiveClaim {
		if err := s.requireProjectClaimForGoal(ctx, goalID, requestedBy); err != nil {
			return GoalHandoff{}, err
		}
	}
	if err := s.reclaimOpenGoalHandoff(ctx, handoffID, goalID); err != nil {
		return GoalHandoff{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff request tx: %w", err)
	}
	defer tx.Rollback()
	if err := sqlcgen.New(tx).RequestGoalHandoff(ctx, sqlcgen.RequestGoalHandoffParams{
		ID:            handoffID,
		GoalID:        goalID,
		RequestedBy:   sql.NullInt64{Int64: requestedBy, Valid: requestedBy != 0},
		RequestedAt:   sql.NullString{String: now, Valid: true},
		RequestReport: sql.NullString{String: requestReport, Valid: requestReport != ""},
	}); err != nil {
		return GoalHandoff{}, fmt.Errorf("request goal handoff: %w", err)
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventGoalHandoffRequest,
		Data: HandoffEvent{GoalID: goalID, HandoffID: handoffID, RequestedBy: requestedBy, RequestReport: requestReport},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("persist goal handoff request event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff request: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetGoalHandoff(ctx, handoffID)
}

// ReceiveGoalHandoff records the receipt side of a requested handoff.
func (s *Store) ReceiveGoalHandoff(ctx context.Context, handoffID string, goalID int64, receivedBy int64) (GoalHandoff, error) {
	if err := s.ensureGoalHandoffGoal(ctx, handoffID, goalID); err != nil {
		return GoalHandoff{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff receive tx: %w", err)
	}
	defer tx.Rollback()
	result, err := sqlcgen.New(tx).ReceiveGoalHandoff(ctx, sqlcgen.ReceiveGoalHandoffParams{
		ID:         handoffID,
		GoalID:     goalID,
		ReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: receivedBy != 0},
		ReceivedAt: sql.NullString{String: now, Valid: true},
	})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("receive goal handoff: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("receive goal handoff rows affected: %w", err)
	}
	if n == 0 {
		return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffNotFound, handoffID)
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventGoalHandoffReceive,
		Data: HandoffEvent{GoalID: goalID, HandoffID: handoffID, ReceivedBy: receivedBy},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("persist goal handoff receive event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff receive: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetGoalHandoff(ctx, handoffID)
}

// RequestGoalHandoffReview starts the review state for a received goal
// handoff. The goal handoff receiver is the only session allowed to request
// its review.
func (s *Store) RequestGoalHandoffReview(ctx context.Context, handoffID string, goalID, requestedBy int64, reviewRequestReport string) (GoalHandoff, error) {
	if completeReportIsEmpty(reviewRequestReport) {
		return GoalHandoff{}, ErrGoalHandoffReviewReportEmpty
	}
	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, err
	}
	if handoff.GoalID != goalID {
		return GoalHandoff{}, fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrGoalHandoffGoalMismatch, handoffID, handoff.GoalID, goalID)
	}
	if handoff.RequestedAt == nil || handoff.ReceivedAt == nil || handoff.CompletedReportAt != nil {
		return GoalHandoff{}, ErrGoalHandoffReviewState
	}
	if handoff.ReceivedBy == 0 || handoff.ReceivedBy != requestedBy {
		return GoalHandoff{}, fmt.Errorf("%w: goal handoff receiver %d cannot request review as %d", ErrGoalHandoffReviewReviewerMismatch, handoff.ReceivedBy, requestedBy)
	}
	if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil {
		return GoalHandoff{}, fmt.Errorf("%w: goal handoff review is already open", ErrGoalHandoffReviewState)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff review request tx: %w", err)
	}
	defer tx.Rollback()
	result, err := sqlcgen.New(tx).RequestGoalHandoffReview(ctx, sqlcgen.RequestGoalHandoffReviewParams{
		ReviewRequestedBy:   sql.NullInt64{Int64: requestedBy, Valid: true},
		ReviewRequestedAt:   sql.NullString{String: now, Valid: true},
		ReviewRequestReport: sql.NullString{String: reviewRequestReport, Valid: true},
		ID:                  handoffID,
		GoalID:              goalID,
	})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("request goal handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return GoalHandoff{}, fmt.Errorf("request goal handoff review rows affected: %w", err)
	} else if affected == 0 {
		return GoalHandoff{}, ErrGoalHandoffReviewState
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventGoalHandoffReviewRequest,
		Data: HandoffReviewEvent{GoalID: goalID, HandoffID: handoffID, ReviewerID: requestedBy, ReviewRequestReport: reviewRequestReport},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("persist goal handoff review request event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff review request: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetGoalHandoff(ctx, handoffID)
}

// ReceiveGoalHandoffReview records the reviewer's receipt. The original goal
// handoff requester remains allowed, while a new current claimant of the goal's
// project may receive the review after a claim turnover.
func (s *Store) ReceiveGoalHandoffReview(ctx context.Context, handoffID string, goalID, receivedBy int64) (GoalHandoff, error) {
	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, err
	}
	if handoff.GoalID != goalID {
		return GoalHandoff{}, fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrGoalHandoffGoalMismatch, handoffID, handoff.GoalID, goalID)
	}
	if handoff.ReviewRequestedAt == nil || handoff.CompletedReportAt != nil || handoff.ReviewReceivedAt != nil {
		return GoalHandoff{}, ErrGoalHandoffReviewState
	}
	if receivedBy == 0 || handoff.RequestedBy != receivedBy {
		if err := s.requireProjectClaimForGoal(ctx, goalID, receivedBy); err != nil {
			return GoalHandoff{}, fmt.Errorf("%w: goal handoff reviewer %d is not requester %d or current project claimant: %v", ErrGoalHandoffReviewReviewerMismatch, receivedBy, handoff.RequestedBy, err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff review receive tx: %w", err)
	}
	defer tx.Rollback()
	result, err := sqlcgen.New(tx).ReceiveGoalHandoffReview(ctx, sqlcgen.ReceiveGoalHandoffReviewParams{
		ReviewReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: true},
		ReviewReceivedAt: sql.NullString{String: now, Valid: true},
		ID:               handoffID,
		GoalID:           goalID,
	})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("receive goal handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return GoalHandoff{}, fmt.Errorf("receive goal handoff review rows affected: %w", err)
	} else if affected == 0 {
		return GoalHandoff{}, ErrGoalHandoffReviewState
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventGoalHandoffReviewReceive,
		Data: HandoffReviewEvent{GoalID: goalID, HandoffID: handoffID, ReviewerID: receivedBy},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("persist goal handoff review receive event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff review receive: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetGoalHandoff(ctx, handoffID)
}

// RejectGoalHandoffReview clears the reviewer receipt while keeping the goal
// handoff claim open for another review cycle.
func (s *Store) RejectGoalHandoffReview(ctx context.Context, handoffID string, goalID, reviewerID int64, rejectReport string) (GoalHandoff, error) {
	if completeReportIsEmpty(rejectReport) {
		return GoalHandoff{}, ErrGoalHandoffReviewReportEmpty
	}
	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, err
	}
	if handoff.GoalID != goalID {
		return GoalHandoff{}, fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrGoalHandoffGoalMismatch, handoffID, handoff.GoalID, goalID)
	}
	if handoff.ReviewReceivedAt == nil || handoff.CompletedReportAt != nil {
		return GoalHandoff{}, ErrGoalHandoffReviewState
	}
	if handoff.ReviewReceivedBy != reviewerID {
		return GoalHandoff{}, fmt.Errorf("%w: goal handoff reviewer %d is not recorded reviewer %d", ErrGoalHandoffReviewReviewerMismatch, reviewerID, handoff.ReviewReceivedBy)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff review rejection tx: %w", err)
	}
	defer tx.Rollback()
	result, err := sqlcgen.New(tx).RejectGoalHandoffReview(ctx, sqlcgen.RejectGoalHandoffReviewParams{
		ReviewRejectedAt:   sql.NullString{String: now, Valid: true},
		ReviewRejectReport: sql.NullString{String: rejectReport, Valid: true},
		ID:                 handoffID,
		GoalID:             goalID,
	})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("reject goal handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return GoalHandoff{}, fmt.Errorf("reject goal handoff review rows affected: %w", err)
	} else if affected == 0 {
		return GoalHandoff{}, ErrGoalHandoffReviewState
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventGoalHandoffReviewReject,
		Data: HandoffReviewEvent{GoalID: goalID, HandoffID: handoffID, ReviewerID: reviewerID, ReviewRejectReport: rejectReport},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("persist goal handoff review rejection event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff review rejection: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetGoalHandoff(ctx, handoffID)
}

// CompleteGoalHandoffByReviewer closes a goal handoff after the recorded
// reviewer accepts the received work.
func (s *Store) CompleteGoalHandoffByReviewer(ctx context.Context, handoffID string, goalID, reviewerID int64, completeReport string) (GoalHandoff, error) {
	if completeReportIsEmpty(completeReport) {
		return GoalHandoff{}, ErrGoalHandoffReportEmpty
	}
	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, err
	}
	if handoff.GoalID != goalID {
		return GoalHandoff{}, fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrGoalHandoffGoalMismatch, handoffID, handoff.GoalID, goalID)
	}
	if handoff.ReviewReceivedAt == nil || handoff.CompletedReportAt != nil {
		return GoalHandoff{}, ErrGoalHandoffReviewState
	}
	if handoff.ReviewReceivedBy != reviewerID {
		return GoalHandoff{}, fmt.Errorf("%w: goal handoff reviewer %d is not recorded reviewer %d", ErrGoalHandoffReviewReviewerMismatch, reviewerID, handoff.ReviewReceivedBy)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff review completion tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	result, err := q.CompleteGoalHandoffByReviewer(ctx, sqlcgen.CompleteGoalHandoffByReviewerParams{
		CompletedReportAt: sql.NullString{String: now, Valid: true},
		CompleteReport:    sql.NullString{String: completeReport, Valid: true},
		ID:                handoffID,
		GoalID:            goalID,
		ReviewReceivedBy:  sql.NullInt64{Int64: reviewerID, Valid: true},
	})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("complete goal handoff by reviewer: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return GoalHandoff{}, fmt.Errorf("complete goal handoff by reviewer rows affected: %w", err)
	} else if affected == 0 {
		return GoalHandoff{}, ErrGoalHandoffReviewState
	}
	projectID, err := q.GetGoalProjectID(ctx, goalID)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("find project for goal handoff review completion: %w", err)
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventHandoffReported,
		Data: DetectionEvent{
			DetectionID:    NewDetectionID(),
			ProjectID:      projectID,
			GoalID:         goalID,
			HandoffID:      handoffID,
			CompleteReport: completeReport,
		},
	}, workflowEventMetadata{ProjectID: projectID, GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("persist goal handoff review completion event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff review completion: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetGoalHandoff(ctx, handoffID)
}

// ReceiveGoalHandoffForGoal resolves receipt by the explicit pending
// handoff. Multiple pending requests are rejected so receipt cannot be
// assigned to the wrong delegation.
func (s *Store) ReceiveGoalHandoffForGoal(ctx context.Context, goalID int64, receivedBy int64) (GoalHandoff, error) {
	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("list pending goal handoffs: %w", err)
	}
	pending := make([]GoalHandoff, 0, len(handoffs))
	for _, handoff := range handoffs {
		if handoff.RequestedAt != nil && handoff.ReceivedAt == nil {
			pending = append(pending, handoff)
		}
	}
	if len(pending) == 0 {
		return GoalHandoff{}, fmt.Errorf("%w: goal %d", ErrGoalHandoffNotFound, goalID)
	}
	if len(pending) > 1 {
		return GoalHandoff{}, fmt.Errorf("%w: goal %d has %d pending handoffs", ErrGoalHandoffAmbiguous, goalID, len(pending))
	}
	return s.ReceiveGoalHandoff(ctx, pending[0].ID, goalID, receivedBy)
}

// CompleteGoalHandoffForGoal finds the single requested, received, and
// incomplete handoff for a goal and records its completion. Multiple
// incomplete handoffs are rejected so completion cannot be assigned to the
// wrong delegation.
func (s *Store) CompleteGoalHandoffForGoal(ctx context.Context, goalID int64, completeReport string) (GoalHandoff, error) {
	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("list incomplete goal handoffs: %w", err)
	}
	pending := make([]GoalHandoff, 0, len(handoffs))
	for _, handoff := range handoffs {
		if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil {
			pending = append(pending, handoff)
		}
	}
	if len(pending) == 0 {
		return GoalHandoff{}, fmt.Errorf("%w: goal %d", ErrGoalHandoffNotFound, goalID)
	}
	if len(pending) > 1 {
		return GoalHandoff{}, fmt.Errorf("%w: goal %d has %d incomplete handoffs", ErrGoalHandoffAmbiguous, goalID, len(pending))
	}
	return s.CompleteGoalHandoff(ctx, pending[0].ID, goalID, completeReport)
}

// CompleteGoalHandoff records the completion report side of a handoff. It
// only writes the completion timestamp and report and therefore preserves partial states.
func (s *Store) CompleteGoalHandoff(ctx context.Context, handoffID string, goalID int64, completeReport string) (GoalHandoff, error) {
	if completeReportIsEmpty(completeReport) {
		return GoalHandoff{}, ErrGoalHandoffReportEmpty
	}
	if err := s.ensureGoalHandoffGoal(ctx, handoffID, goalID); err != nil {
		return GoalHandoff{}, err
	}
	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, err
	}
	if handoff.ReviewRequestedAt != nil && completeReport != goalHandoffReclaimedReport && completeReport != goalHandoffReleasedReport {
		return GoalHandoff{}, fmt.Errorf("%w: complete the goal handoff through its recorded reviewer", ErrGoalHandoffReviewState)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff completion tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	result, err := q.CompleteGoalHandoff(ctx, sqlcgen.CompleteGoalHandoffParams{
		ID:                handoffID,
		GoalID:            goalID,
		CompletedReportAt: sql.NullString{String: now, Valid: true},
		CompleteReport:    sql.NullString{String: completeReport, Valid: completeReport != ""},
	})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("complete goal handoff: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("complete goal handoff rows affected: %w", err)
	}
	if n == 0 {
		currentRow, lookupErr := q.GetGoalHandoff(ctx, handoffID)
		if lookupErr == nil {
			current, parseErr := goalHandoffFromRow(currentRow)
			if parseErr == nil && current.CompletedReportAt != nil {
				return GoalHandoff{}, fmt.Errorf("goal handoff %q is already reported; use another path to add a report after completion", handoffID)
			}
		}
		return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffNotFound, handoffID)
	}
	// Claim locks have no delegate report, so their completion is not reportable.
	var event DecisionEvent
	if handoffIsDelegation(handoff.RequestedBy, handoff.ReceivedBy) {
		projectID, err := q.GetGoalProjectID(ctx, goalID)
		if err != nil {
			return GoalHandoff{}, fmt.Errorf("find project for goal handoff completion: %w", err)
		}
		event, err = s.persistWorkflowEvent(ctx, tx, Event{
			Name: EventHandoffReported,
			Data: DetectionEvent{
				DetectionID:    NewDetectionID(),
				ProjectID:      projectID,
				GoalID:         goalID,
				HandoffID:      handoffID,
				CompleteReport: completeReport,
			},
		}, workflowEventMetadata{ProjectID: projectID, GoalID: goalID, HandoffID: handoffID})
		if err != nil {
			return GoalHandoff{}, fmt.Errorf("persist goal handoff completion event: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff completion: %w", err)
	}
	completed, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, err
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return completed, nil
}

// AmendGoalHandoffReport fills in or corrects the report on a handoff that is
// already closed without changing when it was completed.
func (s *Store) AmendGoalHandoffReport(ctx context.Context, handoffID string, goalID int64, completeReport string) (GoalHandoff, error) {
	if completeReportIsEmpty(completeReport) {
		return GoalHandoff{}, ErrGoalHandoffReportEmpty
	}
	if err := s.ensureGoalHandoffGoal(ctx, handoffID, goalID); err != nil {
		return GoalHandoff{}, err
	}
	result, err := sqlcgen.New(s.db).AmendGoalHandoffReport(ctx, sqlcgen.AmendGoalHandoffReportParams{
		ID: handoffID, GoalID: goalID, CompleteReport: sql.NullString{String: completeReport, Valid: true},
	})
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("amend goal handoff report: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("amend goal handoff report rows affected: %w", err)
	}
	if n == 0 {
		return GoalHandoff{}, fmt.Errorf("goal handoff %q is not yet completed; use atct_goal_handoff_complete", handoffID)
	}
	return s.GetGoalHandoff(ctx, handoffID)
}

// GetGoalHandoff returns one handoff, including NULL timestamps as nil.
func (s *Store) GetGoalHandoff(ctx context.Context, handoffID string) (GoalHandoff, error) {
	row, err := sqlcgen.New(s.db).GetGoalHandoff(ctx, handoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffNotFound, handoffID)
	}
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("get goal handoff %q: %w", handoffID, err)
	}
	return goalHandoffFromRow(row)
}

// ListGoalHandoffs returns all handoffs for a goal, including partial rows.
func (s *Store) ListGoalHandoffs(ctx context.Context, goalID int64) ([]GoalHandoff, error) {
	rows, err := sqlcgen.New(s.db).ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal handoffs: %w", err)
	}

	handoffs := make([]GoalHandoff, 0, len(rows))
	for _, row := range rows {
		handoff, err := goalHandoffFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("parse goal handoff: %w", err)
		}
		handoffs = append(handoffs, handoff)
	}
	return handoffs, nil
}

// ListGoalSessions returns the identified sessions that received a handoff for a goal.
func (s *Store) ListGoalSessions(ctx context.Context, goalID int64) ([]GoalSession, error) {
	rows, err := sqlcgen.New(s.db).ListGoalSessionKeys(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal sessions: %w", err)
	}

	sessions := make([]GoalSession, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, GoalSession{
			SessionKey:  row.SessionKey,
			Role:        row.Role,
			HandoffOpen: row.HandoffOpen != 0,
		})
	}
	return sessions, nil
}

// ListOpenGoalHandoffs returns all incomplete goal handoffs with their full
// review state.
func (s *Store) ListOpenGoalHandoffs(ctx context.Context) (map[int64]*GoalHandoff, error) {
	rows, err := sqlcgen.New(s.db).ListOpenGoalHandoffs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open goal handoffs: %w", err)
	}

	handoffs := make(map[int64]*GoalHandoff, len(rows))
	for _, row := range rows {
		handoff, err := s.GetGoalHandoff(ctx, row.ID)
		if err != nil {
			return nil, fmt.Errorf("parse open goal handoff: %w", err)
		}
		handoffs[handoff.GoalID] = &handoff
	}
	return handoffs, nil
}

// RequestPlanHandoffReview records a plan submitted by the receiver of the
// goal handoff. The open goal handoff remains the authorization for the
// request, while the plan table owns the review lifecycle.
func (s *Store) RequestPlanHandoffReview(ctx context.Context, handoffID string, goalID, requestedBy int64, reviewRequestReport string) (PlanHandoff, error) {
	if completeReportIsEmpty(reviewRequestReport) {
		return PlanHandoff{}, ErrPlanHandoffReviewReportEmpty
	}
	goalHandoff, err := s.openGoalHandoff(ctx, goalID)
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("find live goal handoff for plan review: %w", err)
	}
	if goalHandoff == nil || goalHandoff.ReceivedBy == 0 || goalHandoff.ReceivedBy != requestedBy {
		return PlanHandoff{}, fmt.Errorf("%w: plan review requester %d does not hold the goal handoff", ErrPlanHandoffReviewerMismatch, requestedBy)
	}

	existing, err := s.GetPlanHandoff(ctx, handoffID)
	if err == nil {
		if existing.GoalID != goalID {
			return PlanHandoff{}, fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrPlanHandoffGoalMismatch, handoffID, existing.GoalID, goalID)
		}
		if existing.CompletedReportAt != nil {
			return PlanHandoff{}, ErrPlanHandoffReviewState
		}
		if existing.ReviewRequestedAt != nil && existing.ReviewRejectedAt == nil {
			return PlanHandoff{}, fmt.Errorf("%w: plan handoff review is already open", ErrPlanHandoffReviewState)
		}
	} else if !errors.Is(err, ErrPlanHandoffNotFound) {
		return PlanHandoff{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("begin plan handoff review request tx: %w", err)
	}
	defer tx.Rollback()
	result, err := sqlcgen.New(tx).RequestPlanHandoffReview(ctx, sqlcgen.RequestPlanHandoffReviewParams{
		ID:                  handoffID,
		GoalID:              goalID,
		ReviewRequestedBy:   sql.NullInt64{Int64: requestedBy, Valid: true},
		ReviewRequestedAt:   sql.NullString{String: now, Valid: true},
		ReviewRequestReport: sql.NullString{String: reviewRequestReport, Valid: true},
	})
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("request plan handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return PlanHandoff{}, fmt.Errorf("request plan handoff review rows affected: %w", err)
	} else if affected == 0 {
		return PlanHandoff{}, ErrPlanHandoffReviewState
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventPlanHandoffReviewRequest,
		Data: HandoffReviewEvent{GoalID: goalID, HandoffID: handoffID, ReviewerID: requestedBy, ReviewRequestReport: reviewRequestReport},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("persist plan handoff review request event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PlanHandoff{}, fmt.Errorf("commit plan handoff review request: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetPlanHandoff(ctx, handoffID)
}

// ReceivePlanHandoffReview records receipt by the project claim holder for the
// goal. A plan review does not create or change a task claim.
func (s *Store) ReceivePlanHandoffReview(ctx context.Context, handoffID string, goalID, receivedBy int64) (PlanHandoff, error) {
	handoff, err := s.GetPlanHandoff(ctx, handoffID)
	if err != nil {
		return PlanHandoff{}, err
	}
	if handoff.GoalID != goalID {
		return PlanHandoff{}, fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrPlanHandoffGoalMismatch, handoffID, handoff.GoalID, goalID)
	}
	if handoff.ReviewRequestedAt == nil || handoff.ReviewReceivedAt != nil || handoff.CompletedReportAt != nil {
		return PlanHandoff{}, ErrPlanHandoffReviewState
	}
	if err := s.requireProjectClaimForGoal(ctx, goalID, receivedBy); err != nil {
		return PlanHandoff{}, fmt.Errorf("%w: %v", ErrPlanHandoffReviewerMismatch, err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("begin plan handoff review receive tx: %w", err)
	}
	defer tx.Rollback()
	result, err := sqlcgen.New(tx).ReceivePlanHandoffReview(ctx, sqlcgen.ReceivePlanHandoffReviewParams{
		ReviewReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: true},
		ReviewReceivedAt: sql.NullString{String: now, Valid: true},
		ID:               handoffID,
		GoalID:           goalID,
	})
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("receive plan handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return PlanHandoff{}, fmt.Errorf("receive plan handoff review rows affected: %w", err)
	} else if affected == 0 {
		return PlanHandoff{}, ErrPlanHandoffReviewState
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventPlanHandoffReviewReceive,
		Data: HandoffReviewEvent{GoalID: goalID, HandoffID: handoffID, ReviewerID: receivedBy},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("persist plan handoff review receive event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PlanHandoff{}, fmt.Errorf("commit plan handoff review receive: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetPlanHandoff(ctx, handoffID)
}

// RejectPlanHandoffReview clears the project review receipt while retaining the
// plan row for another review cycle.
func (s *Store) RejectPlanHandoffReview(ctx context.Context, handoffID string, goalID, reviewerID int64, rejectReport string) (PlanHandoff, error) {
	if completeReportIsEmpty(rejectReport) {
		return PlanHandoff{}, ErrPlanHandoffReviewReportEmpty
	}
	handoff, err := s.GetPlanHandoff(ctx, handoffID)
	if err != nil {
		return PlanHandoff{}, err
	}
	if handoff.GoalID != goalID {
		return PlanHandoff{}, fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrPlanHandoffGoalMismatch, handoffID, handoff.GoalID, goalID)
	}
	if handoff.ReviewReceivedAt == nil || handoff.CompletedReportAt != nil {
		return PlanHandoff{}, ErrPlanHandoffReviewState
	}
	if handoff.ReviewReceivedBy != reviewerID {
		return PlanHandoff{}, fmt.Errorf("%w: plan handoff reviewer %d is not recorded reviewer %d", ErrPlanHandoffReviewerMismatch, reviewerID, handoff.ReviewReceivedBy)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("begin plan handoff review rejection tx: %w", err)
	}
	defer tx.Rollback()
	result, err := sqlcgen.New(tx).RejectPlanHandoffReview(ctx, sqlcgen.RejectPlanHandoffReviewParams{
		ReviewRejectedAt:   sql.NullString{String: now, Valid: true},
		ReviewRejectReport: sql.NullString{String: rejectReport, Valid: true},
		ID:                 handoffID,
		GoalID:             goalID,
	})
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("reject plan handoff review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return PlanHandoff{}, fmt.Errorf("reject plan handoff review rows affected: %w", err)
	} else if affected == 0 {
		return PlanHandoff{}, ErrPlanHandoffReviewState
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventPlanHandoffReviewReject,
		Data: HandoffReviewEvent{GoalID: goalID, HandoffID: handoffID, ReviewerID: reviewerID, ReviewRejectReport: rejectReport},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("persist plan handoff review rejection event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PlanHandoff{}, fmt.Errorf("commit plan handoff review rejection: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetPlanHandoff(ctx, handoffID)
}

// CompletePlanHandoff closes a plan review after the recorded project
// reviewer accepts it.
func (s *Store) CompletePlanHandoff(ctx context.Context, handoffID string, goalID, reviewerID int64, completeReport string) (PlanHandoff, error) {
	if completeReportIsEmpty(completeReport) {
		return PlanHandoff{}, ErrPlanHandoffReviewReportEmpty
	}
	handoff, err := s.GetPlanHandoff(ctx, handoffID)
	if err != nil {
		return PlanHandoff{}, err
	}
	if handoff.GoalID != goalID {
		return PlanHandoff{}, fmt.Errorf("%w: %q belongs to goal %d, not %d", ErrPlanHandoffGoalMismatch, handoffID, handoff.GoalID, goalID)
	}
	if handoff.ReviewReceivedAt == nil || handoff.CompletedReportAt != nil {
		return PlanHandoff{}, ErrPlanHandoffReviewState
	}
	if handoff.ReviewReceivedBy != reviewerID {
		return PlanHandoff{}, fmt.Errorf("%w: plan handoff reviewer %d is not recorded reviewer %d", ErrPlanHandoffReviewerMismatch, reviewerID, handoff.ReviewReceivedBy)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("begin plan handoff review completion tx: %w", err)
	}
	defer tx.Rollback()
	result, err := sqlcgen.New(tx).CompletePlanHandoff(ctx, sqlcgen.CompletePlanHandoffParams{
		CompletedReportAt: sql.NullString{String: now, Valid: true},
		CompleteReport:    sql.NullString{String: completeReport, Valid: true},
		ID:                handoffID,
		GoalID:            goalID,
		ReviewReceivedBy:  sql.NullInt64{Int64: reviewerID, Valid: true},
	})
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("complete plan handoff: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return PlanHandoff{}, fmt.Errorf("complete plan handoff rows affected: %w", err)
	} else if affected == 0 {
		return PlanHandoff{}, ErrPlanHandoffReviewState
	}
	event, err := s.persistWorkflowEvent(ctx, tx, Event{
		Name: EventPlanHandoffComplete,
		Data: HandoffReviewEvent{GoalID: goalID, HandoffID: handoffID, ReviewerID: reviewerID, CompleteReport: completeReport},
	}, workflowEventMetadata{GoalID: goalID, HandoffID: handoffID})
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("persist plan handoff completion event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PlanHandoff{}, fmt.Errorf("commit plan handoff review completion: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetPlanHandoff(ctx, handoffID)
}

// GetPlanHandoff returns one plan review handoff, including NULL timestamps as
// nil.
func (s *Store) GetPlanHandoff(ctx context.Context, handoffID string) (PlanHandoff, error) {
	row, err := sqlcgen.New(s.db).GetPlanHandoff(ctx, handoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanHandoff{}, fmt.Errorf("%w: %s", ErrPlanHandoffNotFound, handoffID)
	}
	if err != nil {
		return PlanHandoff{}, fmt.Errorf("get plan handoff %q: %w", handoffID, err)
	}
	return planHandoffFromRow(row)
}

// ListPlanHandoffs returns all plan review handoffs for a goal.
func (s *Store) ListPlanHandoffs(ctx context.Context, goalID int64) ([]PlanHandoff, error) {
	rows, err := sqlcgen.New(s.db).ListPlanHandoffs(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list plan handoffs: %w", err)
	}
	handoffs := make([]PlanHandoff, 0, len(rows))
	for _, row := range rows {
		handoff, err := planHandoffFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("parse plan handoff: %w", err)
		}
		handoffs = append(handoffs, handoff)
	}
	return handoffs, nil
}
