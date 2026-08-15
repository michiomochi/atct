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
