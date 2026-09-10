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
	RecoveredAt                                  *time.Time
	RecoveryReport                               string
}

func taskCreateHandoffFromRow(row sqlcgen.TaskCreateHandoff) (TaskCreateHandoff, error) {
	h := TaskCreateHandoff{ID: row.ID, GoalID: row.GoalID, RequestedBy: nullableAgentSessionID(row.RequestedBy), ReceivedBy: nullableAgentSessionID(row.ReceivedBy), CompletedBy: nullableAgentSessionID(row.CompletedBy), RecoveryReport: row.RecoveryReport.String}
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
	if h.RecoveredAt, err = parseTaskHandoffTime("task create recovered_at", row.RecoveredAt); err != nil {
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
	if err := s.requireUndiscardedSession(ctx, receivedBy); err != nil {
		return TaskCreateHandoff{}, err
	}
	h, err := s.getTaskCreateHandoff(ctx, id)
	if err != nil {
		return h, err
	}
	if h.ReceivedBy != 0 || h.CompletedAt != nil || h.RecoveredAt != nil || h.RequestedBy == receivedBy || receivedBy == 0 {
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

// RecoverTaskCreateHandoff terminalizes a definitely stale task-create attempt
// and creates a normal replacement request for the current project commander.
// The goal handoff holder remains the sole caller.
func (s *Store) RecoverTaskCreateHandoff(ctx context.Context, handoffID string, goalID, callerID int64, reason string) (TaskCreateHandoff, error) {
	if err := s.requireUndiscardedSession(ctx, callerID); err != nil {
		return TaskCreateHandoff{}, err
	}
	if err := s.requireGoalHandoffHolder(ctx, goalID, callerID); err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("recover task-create handoff requires the current goal holder: %w", err)
	}
	handoff, err := s.getTaskCreateHandoff(ctx, handoffID)
	if err != nil {
		return TaskCreateHandoff{}, err
	}
	if handoff.GoalID != goalID || handoff.CompletedAt != nil {
		return TaskCreateHandoff{}, ErrTaskCreateHandoffState
	}
	if handoff.RecoveredAt != nil {
		return handoff, nil
	}
	phase := "requested"
	staleSessionID := handoff.RequestedBy
	if handoff.ReceivedAt != nil {
		phase = "received"
		staleSessionID = handoff.ReceivedBy
	}
	if staleSessionID == 0 || staleSessionID == callerID {
		return TaskCreateHandoff{}, ErrTaskCreateHandoffState
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("begin task-create handoff recovery: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	sessionProof, err := canRecoverSessionInTx(ctx, q, staleSessionID)
	if err != nil {
		return TaskCreateHandoff{}, err
	}
	projectID, err := q.GetGoalProjectID(ctx, goalID)
	if err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("find task-create replacement project: %w", err)
	}
	project, err := q.GetProject(ctx, projectID)
	if err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("find task-create replacement commander: %w", err)
	}
	replacementBy := project.ClaimedBy
	if replacementBy <= 0 || replacementBy == staleSessionID || !claimIsRunningWithQueries(ctx, q, replacementBy) {
		return TaskCreateHandoff{}, ErrTaskCreateHandoffState
	}
	replacementSession, err := q.GetAgentSessionRecovery(ctx, replacementBy)
	if err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("find task-create replacement session: %w", err)
	}
	if agentSessionHasDiscardMetadata(replacementSession) {
		return TaskCreateHandoff{}, ErrSessionDiscarded
	}
	recoveredAt := time.Now().UTC().Format(time.RFC3339Nano)
	var result sql.Result
	if phase == "received" {
		result, err = q.RecoverTaskCreateHandoffReceiver(ctx, sqlcgen.RecoverTaskCreateHandoffReceiverParams{
			RecoveredAt: sql.NullString{String: recoveredAt, Valid: true}, RecoveryReport: sql.NullString{String: reason, Valid: true},
			ID: handoffID, GoalID: goalID, ReceivedBy: sql.NullInt64{Int64: staleSessionID, Valid: true}, Pid: sessionProof.PID, StartedAt: sessionProof.StartedAt,
		})
	} else {
		result, err = q.RecoverTaskCreateHandoffRequester(ctx, sqlcgen.RecoverTaskCreateHandoffRequesterParams{
			RecoveredAt: sql.NullString{String: recoveredAt, Valid: true}, RecoveryReport: sql.NullString{String: reason, Valid: true},
			ID: handoffID, GoalID: goalID, RequestedBy: sql.NullInt64{Int64: staleSessionID, Valid: true}, Pid: sessionProof.PID, StartedAt: sessionProof.StartedAt,
		})
	}
	if err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("recover stale task-create handoff: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("inspect task-create handoff recovery: %w", err)
	} else if affected != 1 {
		return TaskCreateHandoff{}, ErrTaskCreateHandoffState
	}
	_, err = s.createTaskCreateHandoffTx(ctx, tx, goalID, replacementBy)
	if err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("create task-create replacement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TaskCreateHandoff{}, fmt.Errorf("commit task-create handoff recovery: %w", err)
	}
	return s.getTaskCreateHandoff(ctx, handoffID)
}

func (s *Store) createTaskCreateHandoffTx(ctx context.Context, tx *sql.Tx, goalID, requestedBy int64) (string, error) {
	id := uuid.NewString()
	err := sqlcgen.New(tx).CreateTaskCreateHandoff(ctx, sqlcgen.CreateTaskCreateHandoffParams{ID: id, GoalID: goalID, RequestedBy: sql.NullInt64{Int64: requestedBy, Valid: true}, RequestedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true}, RequestReport: sql.NullString{String: "create implementation tasks for accepted plan", Valid: true}})
	return id, err
}

func (s *Store) CreateTasksForHandoff(ctx context.Context, handoffID string, sessionID, goalID int64, agent, key string, titles, descriptions []string) ([]domain.Task, error) {
	if err := s.requireUndiscardedSession(ctx, sessionID); err != nil {
		return nil, err
	}
	if handoffID == "" || key == "" || len(titles) == 0 {
		return nil, ErrTaskCreateHandoffState
	}
	h, err := s.getTaskCreateHandoff(ctx, handoffID)
	if err != nil {
		return nil, err
	}
	if h.GoalID != goalID || h.ReceivedBy != sessionID || h.RecoveredAt != nil {
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
	if err != nil || h.GoalID != goalID || h.ReceivedBy != sessionID || h.RecoveredAt != nil {
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
