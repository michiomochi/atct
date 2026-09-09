package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

var ErrTaskCreateHandoffState = fmt.Errorf("task create handoff is not in the required state")

type TaskCreateHandoff struct {
	ID, PlanHandoffID                            string
	GoalID, RequestedBy, ReceivedBy, CompletedBy int64
	RequestedAt, ReceivedAt, CompletedAt         *time.Time
	CreatedTaskIDs                               []int64
}

func taskCreateHandoffFromRow(row sqlcgen.TaskCreateHandoff) (TaskCreateHandoff, error) {
	h := TaskCreateHandoff{ID: row.ID, PlanHandoffID: row.PlanHandoffID, GoalID: row.GoalID, RequestedBy: nullableAgentSessionID(row.RequestedBy), ReceivedBy: nullableAgentSessionID(row.ReceivedBy), CompletedBy: nullableAgentSessionID(row.CompletedBy)}
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

func (s *Store) GetTaskCreateHandoffForPlan(ctx context.Context, planHandoffID string) (TaskCreateHandoff, error) {
	row, err := sqlcgen.New(s.db).GetTaskCreateHandoffForPlan(ctx, planHandoffID)
	return s.handoffFromGenerated(ctx, row, err)
}

func (s *Store) ListTaskCreateHandoffs(ctx context.Context, goalID int64) ([]TaskCreateHandoff, error) {
	rows, err := sqlcgen.New(s.db).ListTaskCreateHandoffs(ctx, goalID)
	if err != nil {
		return nil, err
	}
	handoffs := make([]TaskCreateHandoff, 0, len(rows))
	for _, row := range rows {
		handoff, err := s.handoffFromGenerated(ctx, row, nil)
		if err != nil {
			return nil, err
		}
		handoffs = append(handoffs, handoff)
	}
	return handoffs, nil
}

func (s *Store) handoffFromGenerated(ctx context.Context, row sqlcgen.TaskCreateHandoff, err error) (TaskCreateHandoff, error) {
	h, err := taskCreateHandoffFromRow(row)
	if err != nil {
		return h, err
	}
	h.CreatedTaskIDs, err = sqlcgen.New(s.db).ListTaskCreateHandoffTaskIDs(ctx, h.ID)
	return h, err
}

func (s *Store) getTaskCreateHandoff(ctx context.Context, id string) (TaskCreateHandoff, error) {
	row, err := sqlcgen.New(s.db).GetTaskCreateHandoff(ctx, id)
	return s.handoffFromGenerated(ctx, row, err)
}

func (s *Store) ReceiveTaskCreateHandoff(ctx context.Context, id string, receivedBy int64) (TaskCreateHandoff, error) {
	h, err := s.getTaskCreateHandoff(ctx, id)
	if err != nil {
		return h, err
	}
	if h.ReceivedBy != 0 || h.CompletedAt != nil || h.RequestedBy == receivedBy || receivedBy == 0 {
		return h, ErrTaskCreateHandoffState
	}
	// The plan submitter is the only receiver; its session is recorded on the plan handoff.
	plan, err := s.GetPlanHandoff(ctx, h.PlanHandoffID)
	if err != nil || plan.ReviewRequestedBy != receivedBy {
		return h, ErrTaskCreateHandoffState
	}
	_, err = sqlcgen.New(s.db).ReceiveTaskCreateHandoff(ctx, sqlcgen.ReceiveTaskCreateHandoffParams{ID: id, ReceivedBy: sql.NullInt64{Int64: receivedBy, Valid: true}, ReceivedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true}})
	if err != nil {
		return h, err
	}
	return s.getTaskCreateHandoff(ctx, id)
}

func (s *Store) CompleteTaskCreateHandoff(ctx context.Context, id string, completedBy int64, report string) (TaskCreateHandoff, error) {
	h, err := s.getTaskCreateHandoff(ctx, id)
	if err != nil {
		return h, err
	}
	if h.ReceivedBy != completedBy || h.CompletedAt != nil || len(h.CreatedTaskIDs) == 0 {
		return h, ErrTaskCreateHandoffState
	}
	missing, err := sqlcgen.New(s.db).CountUndelegatedTaskCreateHandoffTasks(ctx, id)
	if err != nil || missing != 0 {
		return h, ErrTaskCreateHandoffState
	}
	err = sqlcgen.New(s.db).CompleteTaskCreateHandoff(ctx, sqlcgen.CompleteTaskCreateHandoffParams{ID: id, CompletedBy: sql.NullInt64{Int64: completedBy, Valid: true}, CompletedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true}, CompleteReport: sql.NullString{String: report, Valid: true}})
	if err != nil {
		return h, err
	}
	return s.getTaskCreateHandoff(ctx, id)
}

func (s *Store) createTaskCreateHandoffTx(ctx context.Context, tx *sql.Tx, plan PlanHandoff, requestedBy int64) error {
	return sqlcgen.New(tx).CreateTaskCreateHandoff(ctx, sqlcgen.CreateTaskCreateHandoffParams{ID: uuid.NewString(), PlanHandoffID: plan.ID, GoalID: plan.GoalID, RequestedBy: sql.NullInt64{Int64: requestedBy, Valid: true}, RequestedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true}, RequestReport: sql.NullString{String: "create implementation tasks for accepted plan", Valid: true}})
}

func (s *Store) CreateTasksForHandoff(ctx context.Context, handoffID string, sessionID, goalID int64, agent, key string, titles, descriptions []string) ([]domain.Task, error) {
	if handoffID == "" {
		return nil, ErrTaskCreateHandoffState
	}
	h, err := s.getTaskCreateHandoff(ctx, handoffID)
	if err != nil {
		return nil, err
	}
	if h.GoalID != goalID || h.ReceivedBy != sessionID || h.CompletedAt != nil {
		return nil, ErrTaskCreateHandoffState
	}
	if len(titles) != len(descriptions) {
		return nil, fmt.Errorf("create tasks: descriptions count %d does not match titles count %d", len(descriptions), len(titles))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
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
		id, err := q.CreateTask(ctx, sqlcgen.CreateTaskParams{GoalID: goalID, Title: title, Description: descriptions[i], Status: string(domain.TaskTodo), Agent: agent, SortOrder: max + 1 + int64(i), DeclareKey: declareKey, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return nil, err
		}
		if err := q.LinkTaskCreateHandoffTask(ctx, sqlcgen.LinkTaskCreateHandoffTaskParams{HandoffID: handoffID, TaskID: id}); err != nil {
			return nil, err
		}
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
