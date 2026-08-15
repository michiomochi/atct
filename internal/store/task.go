package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
)

const taskColumns = `id, goal_id, title, status, agent, sort_order, declare_key, created_at, updated_at`

// DeclareTasks derives declare_key from the idempotency key and task position.
// The unique (goal_id, declare_key) constraint absorbs duplicate declarations.
// Agents retry and repeat declarations after context compaction, so this prevents task multiplication.
func (s *Store) DeclareTasks(ctx context.Context, goalID, agent, idempotencyKey string, titles []string) ([]domain.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	for i, title := range titles {
		declareKey := fmt.Sprintf("%s#%d", idempotencyKey, i)
		_, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (id, goal_id, title, status, agent, sort_order, declare_key, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(goal_id, declare_key) DO NOTHING`,
			uuid.NewString(), goalID, title, string(domain.TaskTodo), agent, i, declareKey, now, now)
		if err != nil {
			return nil, fmt.Errorf("insert task %d: %w", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.ListTasks(ctx, goalID)
}

func (s *Store) ListTasks(ctx context.Context, goalID string) ([]domain.Task, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE goal_id = ? ORDER BY sort_order`, goalID)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	var out []domain.Task
	for rows.Next() {
		var tk domain.Task
		var status, createdAt, updatedAt string
		if err := rows.Scan(&tk.ID, &tk.GoalID, &tk.Title, &status, &tk.Agent,
			&tk.Order, &tk.DeclareKey, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tk.Status = domain.TaskStatus(status)
		if tk.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		if tk.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		out = append(out, tk)
	}
	return out, rows.Err()
}
