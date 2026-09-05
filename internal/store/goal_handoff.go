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
	ErrGoalHandoffNotFound       = errors.New("goal handoff not found")
	ErrGoalHandoffGoalMismatch   = errors.New("goal handoff goal mismatch")
	ErrGoalHandoffProjectNotHeld = errors.New("goal handoff requires the project claim: caller does not hold a live claim on project")
	ErrGoalHandoffAlreadyOpen    = errors.New("goal handoff already open")
	ErrGoalHandoffAmbiguous      = errors.New("multiple goal handoffs pending receipt")
	ErrGoalHandoffReportEmpty    = errors.New("goal handoff needs a complete_report describing what was done, what was verified, and paths changed; without it, the record cannot distinguish completion from no report")
)

const (
	goalHandoffReclaimedReport = "セッションが停止した"
	goalHandoffReleasedReport  = "ゴールを手放した（報告者なし）"
)

// GoalHandoff records one delegation between agents. Each event timestamp is
// independent so a partial handoff remains observable.
type GoalHandoff struct {
	ID                string
	GoalID            int64
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

// GoalSession identifies an agent session that received a handoff for a goal.
type GoalSession struct {
	SessionKey  string
	Role        string
	HandoffOpen bool
}

func goalHandoffFromRow(row sqlcgen.GoalHandoff) (GoalHandoff, error) {
	handoff := GoalHandoff{
		ID:             row.ID,
		GoalID:         row.GoalID,
		RequestedBy:    nullableAgentSessionID(row.RequestedBy),
		ReceivedBy:     nullableAgentSessionID(row.ReceivedBy),
		RequestReport:  row.RequestReport.String,
		CompleteReport: row.CompleteReport.String,
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin goal handoff reclaim tx: %w", err)
	}
	defer tx.Rollback()
	reclaimed, err := reclaimOpenGoalHandoffTx(ctx, tx, handoffID, goalID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit goal handoff reclaim: %w", err)
	}
	if reclaimed != nil {
		s.publishGoalHandoffReported(ctx, *reclaimed)
	}
	return nil
}

func reclaimOpenGoalHandoffTx(ctx context.Context, tx *sql.Tx, handoffID string, goalID int64) (*GoalHandoff, error) {
	rows, err := sqlcgen.New(tx).ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list open goal handoffs: %w", err)
	}

	var open *GoalHandoff
	for i := range rows {
		handoff, err := goalHandoffFromRow(rows[i])
		if err != nil {
			return nil, fmt.Errorf("parse open goal handoff: %w", err)
		}
		if handoff.CompletedReportAt != nil || handoff.ID == handoffID {
			continue
		}
		if open != nil {
			return nil, fmt.Errorf("%w: goal %d has multiple open handoffs", ErrGoalHandoffAlreadyOpen, goalID)
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
		return nil, fmt.Errorf("%w: goal %d has a live handoff owner", ErrGoalHandoffAlreadyOpen, goalID)
	}
	now := time.Now().UTC()
	result, err := sqlcgen.New(tx).CompleteGoalHandoff(ctx, sqlcgen.CompleteGoalHandoffParams{
		ID:                open.ID,
		GoalID:            goalID,
		CompletedReportAt: sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
		CompleteReport:    sql.NullString{String: goalHandoffReclaimedReport, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("reclaim goal handoff %q: %w", open.ID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("reclaim goal handoff %q rows affected: %w", open.ID, err)
	}
	if rowsAffected != 1 {
		return nil, fmt.Errorf("reclaim goal handoff %q was not completed", open.ID)
	}
	authorID := open.ReceivedBy
	if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "goal_handoff_entries", open.ID, HandoffEntryKindComplete, goalHandoffReclaimedReport, authorID, "", "", true, now); err != nil {
		return nil, fmt.Errorf("append reclaimed goal handoff entry: %w", err)
	}
	open.CompletedReportAt = &now
	open.CompleteReport = goalHandoffReclaimedReport
	return open, nil
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
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff request tx: %w", err)
	}
	defer tx.Rollback()
	txq := sqlcgen.New(tx)
	existing, lookupErr := txq.GetGoalHandoff(ctx, handoffID)
	if lookupErr == nil && existing.CompletedReportAt.Valid && strings.TrimSpace(existing.CompletedReportAt.String) != "" {
		return GoalHandoff{}, fmt.Errorf("%w: goal handoff %q is already complete; use amend or reopen", ErrHandoffEntryTerminal, handoffID)
	}
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return GoalHandoff{}, fmt.Errorf("find goal handoff before request: %w", lookupErr)
	}
	reclaimed, err := reclaimOpenGoalHandoffTx(ctx, tx, handoffID, goalID)
	if err != nil {
		return GoalHandoff{}, err
	}
	existingEntryCount, err := txq.CountGoalHandoffRequestEntries(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("check existing goal handoff request entry: %w", err)
	}
	if err := txq.RequestGoalHandoff(ctx, sqlcgen.RequestGoalHandoffParams{
		ID:            handoffID,
		GoalID:        goalID,
		RequestedBy:   sql.NullInt64{Int64: requestedBy, Valid: requestedBy != 0},
		RequestedAt:   sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
		RequestReport: sql.NullString{String: requestReport, Valid: requestReport != ""},
	}); err != nil {
		return GoalHandoff{}, fmt.Errorf("request goal handoff: %w", err)
	}
	if strings.TrimSpace(requestReport) != "" && existingEntryCount == 0 {
		if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "goal_handoff_entries", handoffID, HandoffEntryKindRequest, requestReport, requestedBy, "", "", true, now); err != nil {
			return GoalHandoff{}, fmt.Errorf("append goal handoff request entry: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff request: %w", err)
	}
	if reclaimed != nil {
		s.publishGoalHandoffReported(ctx, *reclaimed)
	}
	return s.GetGoalHandoff(ctx, handoffID)
}

// ReceiveGoalHandoff records the receipt side of a requested handoff.
func (s *Store) ReceiveGoalHandoff(ctx context.Context, handoffID string, goalID int64, receivedBy int64) (GoalHandoff, error) {
	if err := s.ensureGoalHandoffGoal(ctx, handoffID, goalID); err != nil {
		return GoalHandoff{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff receive tx: %w", err)
	}
	defer tx.Rollback()
	current, err := sqlcgen.New(tx).GetGoalHandoff(ctx, handoffID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffNotFound, handoffID)
		}
		return GoalHandoff{}, fmt.Errorf("find goal handoff receipt state: %w", err)
	}
	if current.GoalID != goalID || !current.RequestedAt.Valid || strings.TrimSpace(current.RequestedAt.String) == "" {
		return GoalHandoff{}, fmt.Errorf("%w: handoff %q has no request", ErrGoalHandoffNotFound, handoffID)
	}
	if current.ReceivedAt.Valid && current.ReceivedAt.String != "" {
		if current.ReceivedBy.Valid && current.ReceivedBy.Int64 != receivedBy {
			return GoalHandoff{}, fmt.Errorf("%w: handoff %q is already received by %d", ErrHandoffEntryParticipant, handoffID, current.ReceivedBy.Int64)
		}
		page, err := listHandoffEntries(ctx, sqlcgen.New(tx), "goal_handoff_entries", handoffID, 0, HandoffHistoryMaxLimit)
		if err != nil {
			return GoalHandoff{}, fmt.Errorf("list goal handoff history after retry receive: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return GoalHandoff{}, fmt.Errorf("commit goal handoff receive retry: %w", err)
		}
		received, err := s.GetGoalHandoff(ctx, handoffID)
		if err != nil {
			return GoalHandoff{}, err
		}
		received.Entries = page.Entries
		received.HasMore = page.HasMore
		received.NextCursor = page.NextCursor
		received.History = page
		return received, nil
	}
	result, err := sqlcgen.New(tx).ReceiveGoalHandoff(ctx, sqlcgen.ReceiveGoalHandoffParams{
		ID:         handoffID,
		GoalID:     goalID,
		ReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: receivedBy != 0},
		ReceivedAt: sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
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
	if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "goal_handoff_entries", handoffID, HandoffEntryKindReceived, "received", receivedBy, "", "", true, now); err != nil {
		return GoalHandoff{}, fmt.Errorf("append goal handoff received entry: %w", err)
	}
	page, err := listHandoffEntries(ctx, sqlcgen.New(tx), "goal_handoff_entries", handoffID, 0, HandoffHistoryMaxLimit)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("list goal handoff history after receive: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff receive: %w", err)
	}
	received, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, err
	}
	received.Entries = page.Entries
	received.HasMore = page.HasMore
	received.NextCursor = page.NextCursor
	received.History = page
	return received, nil
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
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff completion tx: %w", err)
	}
	defer tx.Rollback()
	current, err := sqlcgen.New(tx).GetGoalHandoff(ctx, handoffID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffNotFound, handoffID)
		}
		return GoalHandoff{}, fmt.Errorf("find goal handoff receiver: %w", err)
	}
	if current.GoalID != goalID {
		return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffGoalMismatch, handoffID)
	}
	if !current.ReceivedAt.Valid || strings.TrimSpace(current.ReceivedAt.String) == "" {
		return GoalHandoff{}, fmt.Errorf("%w: handoff %q must be received before completion", ErrHandoffEntryParticipant, handoffID)
	}
	receivedBy := current.ReceivedBy
	result, err := sqlcgen.New(tx).CompleteGoalHandoff(ctx, sqlcgen.CompleteGoalHandoffParams{
		ID:                handoffID,
		GoalID:            goalID,
		CompletedReportAt: sql.NullString{String: now.Format(time.RFC3339Nano), Valid: true},
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
		handoff, lookupErr := sqlcgen.New(tx).GetGoalHandoff(ctx, handoffID)
		if lookupErr == nil && handoff.CompletedReportAt.Valid && handoff.CompletedReportAt.String != "" {
			return GoalHandoff{}, fmt.Errorf("goal handoff %q is already reported; use another path to add a report after completion", handoffID)
		}
		return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffNotFound, handoffID)
	}
	authorID := int64(0)
	if receivedBy.Valid {
		authorID = receivedBy.Int64
	}
	if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "goal_handoff_entries", handoffID, HandoffEntryKindComplete, completeReport, authorID, "", "", true, now); err != nil {
		return GoalHandoff{}, fmt.Errorf("append goal handoff complete entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff completion: %w", err)
	}
	completed, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		return GoalHandoff{}, err
	}
	s.publishGoalHandoffReported(ctx, completed)
	return completed, nil
}

func (s *Store) publishGoalHandoffReported(ctx context.Context, completed GoalHandoff) {
	// Claim locks have no delegate report, so their completion is not reportable.
	if !handoffIsDelegation(completed.RequestedBy, completed.ReceivedBy) {
		return
	}
	goal, err := s.GetGoal(ctx, completed.GoalID)
	if err != nil {
		// Notification is best-effort; do not turn a successful completion into an error.
		return
	}
	s.notify.publishEvent(Event{
		Name: EventHandoffReported,
		Data: DetectionEvent{
			DetectionID:    NewDetectionID(),
			ProjectID:      goal.ProjectID,
			GoalID:         completed.GoalID,
			HandoffID:      completed.ID,
			CompleteReport: completed.CompleteReport,
		},
	})
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoff{}, fmt.Errorf("begin goal handoff amend tx: %w", err)
	}
	defer tx.Rollback()
	current, err := sqlcgen.New(tx).GetGoalHandoff(ctx, handoffID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffNotFound, handoffID)
		}
		return GoalHandoff{}, fmt.Errorf("find goal handoff receiver for amend: %w", err)
	}
	if current.GoalID != goalID {
		return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffGoalMismatch, handoffID)
	}
	receivedBy := current.ReceivedBy
	result, err := sqlcgen.New(tx).AmendGoalHandoffReport(ctx, sqlcgen.AmendGoalHandoffReportParams{
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
	if !receivedBy.Valid || receivedBy.Int64 <= 0 {
		return GoalHandoff{}, fmt.Errorf("%w: completed goal handoff has no received_by", ErrHandoffEntryParticipant)
	}
	if _, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "goal_handoff_entries", handoffID, HandoffEntryKindAmend, completeReport, receivedBy.Int64, "", "", false, time.Now().UTC()); err != nil {
		return GoalHandoff{}, fmt.Errorf("append goal handoff amend entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoff{}, fmt.Errorf("commit goal handoff amend: %w", err)
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

// ListOpenGoalHandoffs returns all incomplete goal handoffs with one query.
func (s *Store) ListOpenGoalHandoffs(ctx context.Context) (map[int64]*GoalHandoff, error) {
	rows, err := sqlcgen.New(s.db).ListOpenGoalHandoffs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open goal handoffs: %w", err)
	}

	handoffs := make(map[int64]*GoalHandoff, len(rows))
	for _, row := range rows {
		handoff, err := goalHandoffFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("parse open goal handoff: %w", err)
		}
		handoffs[handoff.GoalID] = &handoff
	}
	return handoffs, nil
}
