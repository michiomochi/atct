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

const taskColumns = `id, goal_id, title, status, agent, files, sort_order, declare_key, claimed_by, claimed_at, created_at, updated_at`

var ErrTaskAlreadyClaimed = errors.New("task already claimed")
var ErrTaskFileConflict = errors.New("task file conflict")

// DeclareTasks derives declare_key from the idempotency key and task position.
// The unique (goal_id, declare_key) constraint absorbs duplicate declarations.
// Agents retry and repeat declarations after context compaction, so this prevents task multiplication.
func (s *Store) DeclareTasks(ctx context.Context, goalID, agent, idempotencyKey string, titles []string, declaredFiles ...[][]string) ([]domain.Task, error) {
	filesByTask := make([][]string, len(titles))
	if len(declaredFiles) > 1 {
		return nil, fmt.Errorf("declare tasks: expected at most one files list, got %d", len(declaredFiles))
	}
	if len(declaredFiles) == 1 {
		if len(declaredFiles[0]) != 0 && len(declaredFiles[0]) != len(titles) {
			return nil, fmt.Errorf("declare tasks: files count %d does not match titles count %d", len(declaredFiles[0]), len(titles))
		}
		if len(declaredFiles[0]) == len(titles) {
			filesByTask = declaredFiles[0]
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	for i, title := range titles {
		declareKey := fmt.Sprintf("%s#%d", idempotencyKey, i)
		filesJSON, err := marshalTaskFiles(filesByTask[i])
		if err != nil {
			return nil, fmt.Errorf("encode task %d files: %w", i, err)
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO tasks (id, goal_id, title, status, agent, files, sort_order, declare_key, claimed_by, claimed_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(goal_id, declare_key) DO NOTHING`,
			uuid.NewString(), goalID, title, string(domain.TaskTodo), agent, filesJSON, i, declareKey, "", nil, now, now)
		if err != nil {
			return nil, fmt.Errorf("insert task %d: %w", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.ListTasks(ctx, goalID)
}

func marshalTaskFiles(files []string) (string, error) {
	if len(files) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(files)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalTaskFiles(filesJSON sql.NullString) ([]string, error) {
	if !filesJSON.Valid || filesJSON.String == "" {
		return []string{}, nil
	}
	var files []string
	if err := json.Unmarshal([]byte(filesJSON.String), &files); err != nil {
		return nil, err
	}
	if files == nil {
		return []string{}, nil
	}
	return files, nil
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
		var claimedAt sql.NullString
		var filesJSON sql.NullString
		if err := rows.Scan(&tk.ID, &tk.GoalID, &tk.Title, &status, &tk.Agent,
			&filesJSON, &tk.Order, &tk.DeclareKey, &tk.ClaimedBy, &claimedAt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tk.Status = domain.TaskStatus(status)
		files, err := unmarshalTaskFiles(filesJSON)
		if err != nil {
			return nil, fmt.Errorf("parse files: %w", err)
		}
		tk.Files = files
		if claimedAt.Valid {
			t, err := time.Parse(time.RFC3339, claimedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse claimed_at: %w", err)
			}
			tk.ClaimedAt = &t
		}
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

	updateSQL := `UPDATE tasks SET status = ?, updated_at = ?`
	args := []any{string(status), time.Now().UTC().Format(time.RFC3339)}
	if status == domain.TaskTodo || status == domain.TaskDone || status == domain.TaskDropped {
		updateSQL += `, claimed_by = '', claimed_at = NULL`
	}
	updateSQL += ` WHERE id = ?`
	args = append(args, taskID)
	res, err := tx.ExecContext(ctx,
		updateSQL, args...)
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

// ClaimTask atomically assigns a task to one run.
func (s *Store) ClaimTask(ctx context.Context, taskID, runID string) (domain.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin claim tx: %w", err)
	}
	defer tx.Rollback()

	var title, status, claimedBy string
	var filesJSON sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT title, status, claimed_by, files FROM tasks WHERE id = ?`, taskID,
	).Scan(&title, &status, &claimedBy, &filesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("task not found: %s", taskID)
		}
		return domain.Task{}, fmt.Errorf("lookup task claim: %w", err)
	}
	if claimedBy != "" {
		return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskAlreadyClaimed, taskID)
	}

	files, err := unmarshalTaskFiles(filesJSON)
	if err != nil {
		return domain.Task{}, fmt.Errorf("parse task files: %w", err)
	}
	if len(files) > 0 {
		if err := rejectTaskFileConflict(ctx, tx, taskID, title, files, runID); err != nil {
			return domain.Task{}, err
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx,
		`UPDATE tasks
		 SET claimed_by = ?, claimed_at = ?, updated_at = ?
		 WHERE id = ? AND claimed_by = ''`,
		runID, now, now, taskID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("claim task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("claim rows affected: %w", err)
	}
	if n == 0 {
		var currentClaim string
		err := tx.QueryRowContext(ctx, `SELECT claimed_by FROM tasks WHERE id = ?`, taskID).Scan(&currentClaim)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("task not found: %s", taskID)
		}
		if err != nil {
			return domain.Task{}, fmt.Errorf("lookup task claim: %w", err)
		}
		if currentClaim != "" {
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskAlreadyClaimed, taskID)
		}
		return domain.Task{}, fmt.Errorf("claim task affected no rows: %s", taskID)
	}
	if err := tx.Commit(); err != nil {
		return domain.Task{}, fmt.Errorf("commit claim: %w", err)
	}
	return s.loadTask(ctx, taskID)
}

func rejectTaskFileConflict(ctx context.Context, tx *sql.Tx, taskID, title string, files []string, runID string) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, title, status, claimed_by, files
		 FROM tasks
		 WHERE id <> ?
		   AND claimed_by <> ''
		   AND claimed_by <> ?
		   AND status NOT IN (?, ?)
		 ORDER BY sort_order, id`,
		taskID, runID, string(domain.TaskDone), string(domain.TaskDropped))
	if err != nil {
		return fmt.Errorf("query claimed tasks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var otherID, otherTitle, otherStatus, otherClaimedBy string
		var otherFilesJSON sql.NullString
		if err := rows.Scan(&otherID, &otherTitle, &otherStatus, &otherClaimedBy, &otherFilesJSON); err != nil {
			return fmt.Errorf("scan claimed task: %w", err)
		}
		otherFiles, err := unmarshalTaskFiles(otherFilesJSON)
		if err != nil {
			return fmt.Errorf("parse claimed task files: %w", err)
		}
		if file, ok := firstOverlappingFile(files, otherFiles); ok {
			return fmt.Errorf("%w: task %q (%s) conflicts with task %q (%s) on file %q", ErrTaskFileConflict, taskID, title, otherID, otherTitle, file)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate claimed tasks: %w", err)
	}
	return nil
}

func firstOverlappingFile(files, otherFiles []string) (string, bool) {
	otherSet := make(map[string]struct{}, len(otherFiles))
	for _, file := range otherFiles {
		otherSet[file] = struct{}{}
	}
	for _, file := range files {
		if _, ok := otherSet[file]; ok {
			return file, true
		}
	}
	return "", false
}

// ReleaseTask clears a task claim for the human stale-claim release path.
func (s *Store) ReleaseTask(ctx context.Context, taskID string) (domain.Task, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET claimed_by = '', claimed_at = NULL, updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), taskID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("release task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("release rows affected: %w", err)
	}
	if n == 0 {
		var id string
		err := s.db.QueryRowContext(ctx, `SELECT id FROM tasks WHERE id = ?`, taskID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("task not found: %s", taskID)
		}
		if err != nil {
			return domain.Task{}, fmt.Errorf("lookup task: %w", err)
		}
	}
	return s.loadTask(ctx, taskID)
}

func (s *Store) loadTask(ctx context.Context, taskID string) (domain.Task, error) {
	var goalID string
	if err := s.db.QueryRowContext(ctx, `SELECT goal_id FROM tasks WHERE id = ?`, taskID).Scan(&goalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("task not found: %s", taskID)
		}
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
