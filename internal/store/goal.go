package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
)

var ErrGoalNotFound = errors.New("goal not found")

func (s *Store) CreateGoal(ctx context.Context, namespaceID, title, description string) (domain.Goal, error) {
	now := time.Now().UTC()
	g := domain.Goal{
		ID:          uuid.NewString(),
		NamespaceID: namespaceID,
		Title:       title,
		Description: description,
		Status:      domain.GoalActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO goals (id, namespace_id, title, description, status, result_summary, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, '', ?, ?)`,
		g.ID, g.NamespaceID, g.Title, g.Description, string(g.Status),
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return domain.Goal{}, fmt.Errorf("insert goal: %w", err)
	}
	return g, nil
}

func scanGoal(sc interface{ Scan(...any) error }) (domain.Goal, error) {
	var g domain.Goal
	var status, createdAt, updatedAt string
	if err := sc.Scan(&g.ID, &g.NamespaceID, &g.Title, &g.Description,
		&status, &g.ResultSummary, &createdAt, &updatedAt); err != nil {
		return domain.Goal{}, err
	}
	g.Status = domain.GoalStatus(status)
	var err error
	if g.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return domain.Goal{}, fmt.Errorf("parse created_at: %w", err)
	}
	if g.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
		return domain.Goal{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return g, nil
}

const goalColumns = `id, namespace_id, title, description, status, result_summary, created_at, updated_at`

func (s *Store) GetGoal(ctx context.Context, id string) (domain.Goal, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+goalColumns+` FROM goals WHERE id = ?`, id)
	g, err := scanGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %s", ErrGoalNotFound, id)
	}
	return g, err
}

func (s *Store) ListGoals(ctx context.Context, namespaceID string) ([]domain.Goal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+goalColumns+` FROM goals WHERE namespace_id = ? ORDER BY created_at`, namespaceID)
	if err != nil {
		return nil, fmt.Errorf("query goals: %w", err)
	}
	defer rows.Close()

	var out []domain.Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
