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
	if err := sqlcgen.New(s.db).RequestGoalHandoff(ctx, sqlcgen.RequestGoalHandoffParams{
		ID:            handoffID,
		GoalID:        goalID,
		RequestedBy:   sql.NullInt64{Int64: requestedBy, Valid: requestedBy != 0},
		RequestedAt:   sql.NullString{String: now, Valid: true},
		RequestReport: sql.NullString{String: requestReport, Valid: requestReport != ""},
	}); err != nil {
		return GoalHandoff{}, fmt.Errorf("request goal handoff: %w", err)
	}
	return s.GetGoalHandoff(ctx, handoffID)
}

// ReceiveGoalHandoff records the receipt side of a requested handoff.
func (s *Store) ReceiveGoalHandoff(ctx context.Context, handoffID string, goalID int64, receivedBy int64) (GoalHandoff, error) {
	if err := s.ensureGoalHandoffGoal(ctx, handoffID, goalID); err != nil {
		return GoalHandoff{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := sqlcgen.New(s.db).ReceiveGoalHandoff(ctx, sqlcgen.ReceiveGoalHandoffParams{
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
	if strings.TrimSpace(completeReport) == "" {
		return GoalHandoff{}, ErrGoalHandoffReportEmpty
	}
	if err := s.ensureGoalHandoffGoal(ctx, handoffID, goalID); err != nil {
		return GoalHandoff{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := sqlcgen.New(s.db).CompleteGoalHandoff(ctx, sqlcgen.CompleteGoalHandoffParams{
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
		handoff, lookupErr := s.GetGoalHandoff(ctx, handoffID)
		if lookupErr == nil && handoff.CompletedReportAt != nil {
			return GoalHandoff{}, fmt.Errorf("goal handoff %q is already reported; use another path to add a report after completion", handoffID)
		}
		return GoalHandoff{}, fmt.Errorf("%w: %s", ErrGoalHandoffNotFound, handoffID)
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
