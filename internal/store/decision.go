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
	ErrDecisionNotFound    = errors.New("decision not found")
	ErrDecisionNotOpen     = errors.New("decision is not open")
	ErrTaskHasOpenDecision = errors.New("task has an open decision")
	ErrGoalHasOpenDecision = errors.New("goal has an open decision")
)

type AskInput struct {
	GoalID   string
	TaskID   string
	Kind     domain.DecisionKind
	Question string
	Options  []domain.Option
	RunID    string
}

const decisionColumns = `id, goal_id, COALESCE(task_id, ''), kind, question, options, status,
	answer_label, answer_text, answered_by, answered_at, applied_at, run_id, created_at`

func (s *Store) AskDecision(ctx context.Context, in AskInput) (domain.Decision, error) {
	if in.Options == nil {
		in.Options = []domain.Option{}
	}
	raw, err := json.Marshal(in.Options)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("marshal options: %w", err)
	}

	d := domain.Decision{
		ID:        uuid.NewString(),
		GoalID:    in.GoalID,
		TaskID:    in.TaskID,
		Kind:      in.Kind,
		Question:  in.Question,
		Options:   in.Options,
		Status:    domain.DecisionOpen,
		RunID:     in.RunID,
		CreatedAt: time.Now().UTC(),
	}

	var taskID any
	if in.TaskID != "" {
		taskID = in.TaskID
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO decisions (id, goal_id, task_id, kind, question, options, status, run_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.GoalID, taskID, string(d.Kind), d.Question, string(raw),
		string(d.Status), d.RunID, d.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return domain.Decision{}, fmt.Errorf("insert decision: %w", err)
	}
	s.notify.publish(d.ID)
	s.notify.publishAll()
	s.notify.publishEvent(DecisionEvent{Name: "decision.created", Decision: d})
	return d, nil
}

func scanDecision(sc interface{ Scan(...any) error }) (domain.Decision, error) {
	var d domain.Decision
	var kind, status, rawOptions, createdAt string
	var answeredAt, appliedAt sql.NullString

	if err := sc.Scan(&d.ID, &d.GoalID, &d.TaskID, &kind, &d.Question, &rawOptions, &status,
		&d.AnswerLabel, &d.AnswerText, &d.AnsweredBy, &answeredAt, &appliedAt,
		&d.RunID, &createdAt); err != nil {
		return domain.Decision{}, err
	}
	d.Kind = domain.DecisionKind(kind)
	d.Status = domain.DecisionStatus(status)
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
