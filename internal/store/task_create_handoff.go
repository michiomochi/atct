package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

var ErrTaskCreateHandoffState = fmt.Errorf("task create handoff is not in the required state")

type TaskCreateHandoff struct {
	ID                                           string
	GoalID, RequestedBy, ReceivedBy, CompletedBy int64
	RequestedAt, ReceivedAt, CompletedAt         *time.Time
}

func taskCreateHandoffFromRow(row sqlcgen.TaskCreateHandoff) (TaskCreateHandoff, error) {
	h := TaskCreateHandoff{ID: row.ID, GoalID: row.GoalID, RequestedBy: nullableAgentSessionID(row.RequestedBy), ReceivedBy: nullableAgentSessionID(row.ReceivedBy), CompletedBy: nullableAgentSessionID(row.CompletedBy)}
	var err error
	if h.RequestedAt, err = parseTaskHandoffTime("task create requested_at", row.RequestedAt); err != nil {
		return h, err
	}
	if h.ReceivedAt, err = parseTaskHandoffTime("task create received_at", row.ReceivedAt); err != nil {
		return h, err
	}
	if h.CompletedAt, err = parseTaskHandoffTime("task create completed_at", row.CompletedAt); err != nil {
		return h, err
	}
	return h, nil
}

func (s *Store) GetTaskCreateHandoffForGoal(ctx context.Context, goalID int64) (TaskCreateHandoff, error) {
	row, err := sqlcgen.New(s.db).GetTaskCreateHandoffForGoal(ctx, goalID)
	if err != nil {
		return TaskCreateHandoff{}, err
	}
	return taskCreateHandoffFromRow(row)
}

func (s *Store) ListTaskCreateHandoffs(ctx context.Context, goalID int64) ([]TaskCreateHandoff, error) {
	rows, err := sqlcgen.New(s.db).ListTaskCreateHandoffs(ctx, goalID)
	if err != nil {
		return nil, err
	}
	handoffs := make([]TaskCreateHandoff, 0, len(rows))
	for _, row := range rows {
		handoff, err := taskCreateHandoffFromRow(row)
		if err != nil {
			return nil, err
		}
		handoffs = append(handoffs, handoff)
	}
	return handoffs, nil
}

func (s *Store) getTaskCreateHandoff(ctx context.Context, id string) (TaskCreateHandoff, error) {
	row, err := sqlcgen.New(s.db).GetTaskCreateHandoff(ctx, id)
	if err != nil {
		return TaskCreateHandoff{}, err
	}
	return taskCreateHandoffFromRow(row)
}

func (s *Store) ReceiveTaskCreateHandoff(ctx context.Context, id string, receivedBy int64) (TaskCreateHandoff, error) {
	h, err := s.getTaskCreateHandoff(ctx, id)
	if err != nil {
		return h, err
	}
	if h.ReceivedBy != 0 || h.CompletedAt != nil || h.RequestedBy == receivedBy || receivedBy == 0 {
		return h, ErrTaskCreateHandoffState
	}
	goalHandoff, err := s.openGoalHandoff(ctx, h.GoalID)
	if err != nil || goalHandoff == nil || goalHandoff.ReceivedBy != receivedBy {
		return h, ErrTaskCreateHandoffState
	}
	_, err = sqlcgen.New(s.db).ReceiveTaskCreateHandoff(ctx, sqlcgen.ReceiveTaskCreateHandoffParams{ID: id, ReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: true}, ReceivedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true}})
	if err != nil {
		return h, err
	}
	return s.getTaskCreateHandoff(ctx, id)
}

func (s *Store) createTaskCreateHandoffTx(ctx context.Context, tx *sql.Tx, goalID, requestedBy int64) error {
	return sqlcgen.New(tx).CreateTaskCreateHandoff(ctx, sqlcgen.CreateTaskCreateHandoffParams{ID: uuid.NewString(), GoalID: goalID, RequestedBy: sql.NullInt64{Int64: requestedBy, Valid: true}, RequestedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true}, RequestReport: sql.NullString{String: "create implementation tasks for accepted plan", Valid: true}})
}

func (s *Store) CreateTasksForHandoff(ctx context.Context, handoffID string, sessionID, goalID int64, agent, key string, titles, descriptions []string) ([]domain.Task, error) {
	if handoffID == "" || key == "" || len(titles) == 0 {
		return nil, ErrTaskCreateHandoffState
	}
	h, err := s.getTaskCreateHandoff(ctx, handoffID)
	if err != nil {
		return nil, err
	}
	if h.GoalID != goalID || h.ReceivedBy != sessionID {
		return nil, ErrTaskCreateHandoffState
	}
	if len(titles) != len(descriptions) {
		return nil, fmt.Errorf("create tasks: descriptions count %d does not match titles count %d", len(descriptions), len(titles))
	}
	if h.CompletedAt != nil {
		return s.replayTasksForHandoff(ctx, goalID, key)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	row, err := q.GetTaskCreateHandoff(ctx, handoffID)
	if err != nil {
		return nil, err
	}
	h, err = taskCreateHandoffFromRow(row)
	if err != nil || h.GoalID != goalID || h.ReceivedBy != sessionID {
		return nil, ErrTaskCreateHandoffState
	}
	if h.CompletedAt != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.replayTasksForHandoff(ctx, goalID, key)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	existing, err := q.ListTasks(ctx, goalID)
	if err != nil {
		return nil, err
	}
	existed := map[string]bool{}
	for _, task := range existing {
		existed[task.DeclareKey] = true
	}
	max, err := q.MaxTaskSortOrder(ctx, goalID)
	if err != nil {
		return nil, err
	}
	for i, title := range titles {
		declareKey := fmt.Sprintf("%s#%d", key, i)
		_, err := q.CreateTask(ctx, sqlcgen.CreateTaskParams{GoalID: goalID, Title: title, Description: descriptions[i], Status: string(domain.TaskTodo), Agent: agent, SortOrder: max + 1 + int64(i), DeclareKey: declareKey, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return nil, err
		}
	}
	completed, err := q.CompleteTaskCreateHandoff(ctx, sqlcgen.CompleteTaskCreateHandoffParams{ID: handoffID, ReceivedBy: sql.NullInt64{Int64: sessionID, Valid: true}, CompletedBy: sql.NullInt64{Int64: sessionID, Valid: true}, CompletedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true}, CompleteReport: sql.NullString{String: "implementation tasks created", Valid: true}})
	if err != nil {
		return nil, err
	}
	if affected, err := completed.RowsAffected(); err != nil || affected != 1 {
		return nil, ErrTaskCreateHandoffState
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	all, err := s.ListTasks(ctx, goalID)
	if err != nil {
		return nil, err
	}
	byKey := map[string]domain.Task{}
	for _, task := range all {
		byKey[task.DeclareKey] = task
	}
	out := make([]domain.Task, 0, len(titles))
	for i := range titles {
		k := fmt.Sprintf("%s#%d", key, i)
		task := byKey[k]
		created := !existed[k]
		task.Created = &created
		out = append(out, task)
	}
	return out, nil
}

func (s *Store) replayTasksForHandoff(ctx context.Context, goalID int64, key string) ([]domain.Task, error) {
	all, err := s.ListTasks(ctx, goalID)
	if err != nil {
		return nil, err
	}
	prefix := key + "#"
	byIndex := make(map[int]domain.Task)
	for _, task := range all {
		if !strings.HasPrefix(task.DeclareKey, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(task.DeclareKey, prefix)
		index, err := strconv.Atoi(suffix)
		if err != nil || index < 0 || strconv.Itoa(index) != suffix {
			return nil, ErrTaskCreateHandoffState
		}
		byIndex[index] = task
	}
	if len(byIndex) == 0 {
		return nil, ErrTaskCreateHandoffState
	}
	out := make([]domain.Task, 0, len(byIndex))
	for index := 0; index < len(byIndex); index++ {
		task, ok := byIndex[index]
		if !ok {
			return nil, ErrTaskCreateHandoffState
		}
		created := false
		task.Created = &created
		out = append(out, task)
	}
	return out, nil
}
