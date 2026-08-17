package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
)

var (
	ErrDecisionNotFound       = errors.New("decision not found")
	ErrDecisionNotOpen        = errors.New("decision is not open")
	ErrInvalidDecisionDefault = errors.New("invalid decision default")
	ErrTaskHasOpenDecision    = errors.New("task has an open decision")
	ErrGoalHasOpenDecision    = errors.New("goal has an open decision")
)

type AskInput struct {
	GoalID         string
	TaskID         string
	Kind           domain.DecisionKind
	Question       string
	Options        []domain.Option
	DefaultOption  string
	DefaultAfterMs *int64
	RunID          string
}

const decisionColumns = `id, goal_id, COALESCE(task_id, ''), kind, question, options, status,
	default_option, default_after_ms, default_applied_at,
	answer_label, answer_text, answered_by, answered_at, applied_at, run_id, created_at`

func (s *Store) AskDecision(ctx context.Context, in AskInput) (domain.Decision, error) {
	if err := validateDecisionDefault(in.Options, in.DefaultOption, in.DefaultAfterMs); err != nil {
		return domain.Decision{}, err
	}
	if in.Options == nil {
		in.Options = []domain.Option{}
	}
	raw, err := json.Marshal(in.Options)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("marshal options: %w", err)
	}

	d := domain.Decision{
		ID:            uuid.NewString(),
		GoalID:        in.GoalID,
		TaskID:        in.TaskID,
		Kind:          in.Kind,
		Question:      in.Question,
		Options:       in.Options,
		DefaultOption: in.DefaultOption,
		Status:        domain.DecisionOpen,
		RunID:         in.RunID,
		CreatedAt:     time.Now().UTC(),
	}
	if in.DefaultAfterMs != nil {
		after := *in.DefaultAfterMs
		d.DefaultAfterMs = &after
	}

	var taskID any
	if in.TaskID != "" {
		taskID = in.TaskID
	}

	var defaultAfterMs any
	if in.DefaultAfterMs != nil {
		defaultAfterMs = *in.DefaultAfterMs
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO decisions (id, goal_id, task_id, kind, question, options, status,
		 default_option, default_after_ms, run_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.GoalID, taskID, string(d.Kind), d.Question, string(raw),
		string(d.Status), d.DefaultOption, defaultAfterMs, d.RunID, d.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return domain.Decision{}, fmt.Errorf("insert decision: %w", err)
	}
	s.notify.publish(d.ID)
	s.notify.publishAll()
	s.notify.publishEvent(DecisionEvent{Name: "decision.created", Decision: d})
	return d, nil
}

func validateDecisionDefault(options []domain.Option, defaultOption string, defaultAfterMs *int64) error {
	if defaultOption == "" && defaultAfterMs == nil {
		return nil
	}
	if defaultOption == "" {
		return fmt.Errorf("%w: default_option is required", ErrInvalidDecisionDefault)
	}
	if defaultAfterMs != nil && *defaultAfterMs < 0 {
		return fmt.Errorf("%w: default_after_ms must not be negative", ErrInvalidDecisionDefault)
	}
	for _, option := range options {
		if option.Label == defaultOption {
			return nil
		}
	}
	return fmt.Errorf("%w: default_option %q is not one of the option labels", ErrInvalidDecisionDefault, defaultOption)
}

func scanDecision(sc interface{ Scan(...any) error }) (domain.Decision, error) {
	var d domain.Decision
	var kind, status, rawOptions, createdAt string
	var defaultAfterMs sql.NullInt64
	var defaultAppliedAt, answeredAt, appliedAt sql.NullString

	if err := sc.Scan(&d.ID, &d.GoalID, &d.TaskID, &kind, &d.Question, &rawOptions, &status,
		&d.DefaultOption, &defaultAfterMs, &defaultAppliedAt,
		&d.AnswerLabel, &d.AnswerText, &d.AnsweredBy, &answeredAt, &appliedAt,
		&d.RunID, &createdAt); err != nil {
		return domain.Decision{}, err
	}
	d.Kind = domain.DecisionKind(kind)
	d.Status = domain.DecisionStatus(status)
	if defaultAfterMs.Valid {
		after := defaultAfterMs.Int64
		d.DefaultAfterMs = &after
	}
	if err := json.Unmarshal([]byte(rawOptions), &d.Options); err != nil {
		return domain.Decision{}, fmt.Errorf("unmarshal options: %w", err)
	}
	var err error
	if d.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return domain.Decision{}, fmt.Errorf("parse created_at: %w", err)
	}
	if answeredAt.Valid {
		t, err := time.Parse(time.RFC3339, answeredAt.String)
		if err != nil {
			return domain.Decision{}, fmt.Errorf("parse answered_at: %w", err)
		}
		d.AnsweredAt = &t
	}
	if appliedAt.Valid {
		t, err := time.Parse(time.RFC3339, appliedAt.String)
		if err != nil {
			return domain.Decision{}, fmt.Errorf("parse applied_at: %w", err)
		}
		d.AppliedAt = &t
	}
	if defaultAppliedAt.Valid {
		t, err := time.Parse(time.RFC3339, defaultAppliedAt.String)
		if err != nil {
			return domain.Decision{}, fmt.Errorf("parse default_applied_at: %w", err)
		}
		d.DefaultAppliedAt = &t
	}
	return d, nil
}

func (s *Store) GetDecision(ctx context.Context, id string) (domain.Decision, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+decisionColumns+` FROM decisions WHERE id = ?`, id)
	d, err := scanDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Decision{}, fmt.Errorf("%w: %s", ErrDecisionNotFound, id)
	}
	return d, err
}

func (s *Store) ListOpenDecisions(ctx context.Context, goalID string) ([]domain.Decision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+decisionColumns+` FROM decisions WHERE goal_id = ? AND status = 'open' ORDER BY created_at`,
		goalID)
	if err != nil {
		return nil, fmt.Errorf("query open decisions: %w", err)
	}
	defer rows.Close()

	var out []domain.Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ListAllOpenDecisions(ctx context.Context) ([]domain.Decision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+decisionColumns+` FROM decisions WHERE status = 'open' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("query all open decisions: %w", err)
	}
	defer rows.Close()

	var out []domain.Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type AnswerInput struct {
	DecisionID  string
	AnswerLabel string
	AnswerText  string
	AnsweredBy  string
}

// AnswerDecision performs a conditional transition from open.
// WHERE status = 'open' ensures only one concurrent answer succeeds.
// The application must enforce this at the database boundary because humans can answer twice in separate tabs.
func (s *Store) AnswerDecision(ctx context.Context, in AnswerInput) (domain.Decision, error) {
	return s.answerDecision(ctx, in, "decision.answered")
}

func (s *Store) answerDecision(ctx context.Context, in AnswerInput, eventName string) (domain.Decision, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := s.db.ExecContext(ctx,
		`UPDATE decisions
		 SET status = 'answered', answer_label = ?, answer_text = ?, answered_by = ?, answered_at = ?
		 WHERE id = ? AND status = 'open'`,
		in.AnswerLabel, in.AnswerText, in.AnsweredBy, now, in.DecisionID)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("update decision: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Decision{}, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return domain.Decision{}, fmt.Errorf("%w: %s", ErrDecisionNotOpen, in.DecisionID)
	}
	d, err := s.GetDecision(ctx, in.DecisionID)
	if err != nil {
		return domain.Decision{}, err
	}
	s.notify.publish(in.DecisionID)
	s.notify.publishAll()
	s.notify.publishEvent(DecisionEvent{Name: eventName, Decision: d})
	return d, nil
}

// ApplyExpiredDefaults settles open decisions whose default timeout has elapsed.
// The selection and conditional update happen in one transaction so a human answer
// that wins the open-to-answered transition cannot be overwritten by a default.
func (s *Store) ApplyExpiredDefaults(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT `+decisionColumns+` FROM decisions
		 WHERE status = 'open' AND default_after_ms IS NOT NULL AND default_option != ''
		 ORDER BY created_at`)
	if err != nil {
		return 0, fmt.Errorf("query expired decisions: %w", err)
	}

	var candidates []domain.Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		if d.DefaultAfterMs != nil && !now.Before(d.CreatedAt.Add(time.Duration(*d.DefaultAfterMs)*time.Millisecond)) {
			candidates = append(candidates, d)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close expired decisions: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	settledAt := now.UTC()
	settledAtText := settledAt.Format(time.RFC3339)
	var settledDecisions []domain.Decision
	for i := range candidates {
		result, err := tx.ExecContext(ctx,
			`UPDATE decisions
			 SET status = 'answered', answer_label = ?, answered_at = ?, default_applied_at = ?
			 WHERE id = ? AND status = 'open'`,
			candidates[i].DefaultOption, settledAtText, settledAtText, candidates[i].ID)
		if err != nil {
			return 0, fmt.Errorf("apply default: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("default rows affected: %w", err)
		}
		if updated == 0 {
			continue
		}
		candidates[i].Status = domain.DecisionAnswered
		candidates[i].AnswerLabel = candidates[i].DefaultOption
		answeredAt := settledAt
		candidates[i].AnsweredAt = &answeredAt
		defaultAppliedAt := settledAt
		candidates[i].DefaultAppliedAt = &defaultAppliedAt
		settledDecisions = append(settledDecisions, candidates[i])
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	for _, d := range settledDecisions {
		s.notify.publish(d.ID)
		s.notify.publishEvent(DecisionEvent{Name: "decision.answered", Decision: d})
	}
	if len(settledDecisions) > 0 {
		s.notify.publishAll()
	}
	return len(settledDecisions), nil
}

func (s *Store) WithdrawDecision(ctx context.Context, decisionID, reason string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE decisions SET status = 'withdrawn', answer_text = ? WHERE id = ? AND status = 'open'`,
		reason, decisionID)
	if err != nil {
		return fmt.Errorf("withdraw decision: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrDecisionNotOpen, decisionID)
	}
	d, err := s.GetDecision(ctx, decisionID)
	if err != nil {
		return err
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.notify.publishEvent(DecisionEvent{Name: "decision.withdrawn", Decision: d})
	return nil
}

// PollDecisions returns answered Decisions and transitions them to applied in one transaction.
// "Human answered" and "agent received" are distinct facts.
// A decision becomes applied when returned; if the process dies before return, it remains answered and the next poll can recover it.
func (s *Store) PollDecisions(ctx context.Context, runID string, decisionID string) ([]domain.Decision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var query string
	var args []any
	if decisionID == "" {
		query = `SELECT ` + decisionColumns + ` FROM decisions WHERE status = 'answered' AND run_id = ?`
		args = []any{runID}
	} else {
		query = `SELECT ` + decisionColumns + ` FROM decisions WHERE status = 'answered' AND id = ?`
		args = []any{decisionID}
	}
	query += ` ORDER BY answered_at`

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query answered decisions: %w", err)
	}
	var out []domain.Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	for i := range out {
		if _, err := tx.ExecContext(ctx,
			`UPDATE decisions SET status = 'applied', applied_at = ? WHERE id = ? AND status = 'answered'`,
			now.Format(time.RFC3339), out[i].ID); err != nil {
			return nil, fmt.Errorf("mark applied: %w", err)
		}
		out[i].Status = domain.DecisionApplied
		applied := now
		out[i].AppliedAt = &applied
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	for _, d := range out {
		s.notify.publish(d.ID)
		s.notify.publishEvent(DecisionEvent{Name: "decision.applied", Decision: d})
	}
	if len(out) > 0 {
		s.notify.publishAll()
	}
	return out, nil
}

// ListUnappliedDecisions returns answered Decisions not yet received by an agent.
// This is the only place to detect an answer stranded after a dead session.
func (s *Store) ListUnappliedDecisions(ctx context.Context) ([]domain.Decision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+decisionColumns+` FROM decisions WHERE status = 'answered' ORDER BY answered_at`)
	if err != nil {
		return nil, fmt.Errorf("query unapplied decisions: %w", err)
	}
	defer rows.Close()

	var out []domain.Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
