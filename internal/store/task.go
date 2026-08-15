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

// UpdateTask treats the transition to done as a guarded operation.
// Allowing a task with an open Decision to become done would break the
// invariant that human-waiting tasks are derived from Decisions.
func (s *Store) UpdateTask(ctx context.Context, taskID string, status domain.TaskStatus) (domain.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if status == domain.TaskDone {
		var open int
		err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM decisions WHERE task_id = ? AND status = 'open'`, taskID).Scan(&open)
		if err != nil {
			return domain.Task{}, fmt.Errorf("count open decisions: %w", err)
		}
		if open > 0 {
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskHasOpenDecision, taskID)
		}
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), time.Now().UTC().Format(time.RFC3339), taskID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("update task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return domain.Task{}, fmt.Errorf("task not found: %s", taskID)
	}
	if err := tx.Commit(); err != nil {
		return domain.Task{}, fmt.Errorf("commit: %w", err)
	}

	var goalID string
	if err := s.db.QueryRowContext(ctx, `SELECT goal_id FROM tasks WHERE id = ?`, taskID).Scan(&goalID); err != nil {
		return domain.Task{}, fmt.Errorf("lookup goal_id: %w", err)
	}
	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		return domain.Task{}, err
	}
	for _, tk := range tasks {
		if tk.ID == taskID {
			return tk, nil
		}
	}
	return domain.Task{}, fmt.Errorf("task not found after update: %s", taskID)
}
