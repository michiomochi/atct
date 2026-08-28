package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

var ErrTaskNotFound = errors.New("task not found")
var ErrTaskAlreadyClaimed = errors.New("task already claimed")
var ErrTaskNotEditable = errors.New("task not editable")
var ErrTaskContentNotOwned = errors.New("task content not owned")

const maxOpenDecisionQuestionLength = 160

type openTaskDecision struct {
	ID       int64
	Question string
}

func goalNotActiveError(goalID int64, status domain.GoalStatus, beforeAction, action string) error {
	var stateMessage string
	switch status {
	case domain.GoalProposed:
		stateMessage = fmt.Sprintf("goal %d is not approved; obtain human approval before %s its tasks (承認されていないため、先に人間の承認を得てください)", goalID, beforeAction)
	case domain.GoalDone:
		stateMessage = fmt.Sprintf("goal %d is complete; cannot %s its tasks (ゴールが完了しているため、タスク操作はできません)", goalID, action)
	case domain.GoalDropped:
		stateMessage = fmt.Sprintf("goal %d has been withdrawn; cannot %s its tasks (ゴールが取り下げられているため、タスク操作はできません)", goalID, action)
	default:
		stateMessage = fmt.Sprintf("goal %d has status %q and is not active; cannot %s its tasks (ゴールの状態 %q はアクティブではないため、タスク操作はできません)", goalID, status, action, status)
	}
	return fmt.Errorf("%w: %s", ErrGoalNotActive, stateMessage)
}

// DeclareTasks derives declare_key from the idempotency key and task position.
// The unique (goal_id, declare_key) constraint absorbs duplicate declarations.
// Agents retry and repeat declarations after context compaction, so this prevents task multiplication.
func (s *Store) DeclareTasks(ctx context.Context, goalID int64, agent, idempotencyKey string, titles []string, descriptions []string) ([]domain.Task, error) {
	if len(descriptions) != len(titles) {
		return nil, fmt.Errorf("declare tasks: descriptions count %d does not match titles count %d", len(descriptions), len(titles))
	}
	for i, description := range descriptions {
		if strings.TrimSpace(description) == "" {
			return nil, fmt.Errorf("declare tasks: description %d is empty", i)
		}
	}

	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("get goal for declaring tasks: %w", err)
	}
	if goal.Status != domain.GoalActive {
		return nil, goalNotActiveError(goalID, goal.Status, "declaring", "declare")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	q := sqlcgen.New(tx)
	existingTasks, err := q.ListTasks(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list existing tasks: %w", err)
	}
	existingDeclareKeys := make(map[string]struct{}, len(existingTasks))
	for _, task := range existingTasks {
		existingDeclareKeys[task.DeclareKey] = struct{}{}
	}
	createdByKey := make(map[string]bool, len(titles))
	maxSortOrder, err := q.MaxTaskSortOrder(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("get max task sort order: %w", err)
	}
	for i, title := range titles {
		declareKey := fmt.Sprintf("%s#%d", idempotencyKey, i)
		_, alreadyExists := existingDeclareKeys[declareKey]
		createdByKey[declareKey] = !alreadyExists
		_, err = q.CreateTask(ctx, sqlcgen.CreateTaskParams{
			GoalID:       goalID,
			Title:        title,
			Description:  descriptions[i],
			Status:       string(domain.TaskTodo),
			Agent:        agent,
			SortOrder:    maxSortOrder + 1 + int64(i),
			DeclareKey:   declareKey,
			SnoozedUntil: sql.NullString{},
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			return nil, fmt.Errorf("insert task %d: %w", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		created, ok := createdByKey[tasks[i].DeclareKey]
		if !ok {
			continue
		}
		tasks[i].Created = new(bool)
		*tasks[i].Created = created
	}
	return tasks, nil
}

func (s *Store) ListTasks(ctx context.Context, goalID int64) ([]domain.Task, error) {
	rows, err := taskQueries(s).ListTasks(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}

	var out []domain.Task
	for _, row := range rows {
		tk, err := taskFromFields(row.ID, row.GoalID, row.Title, row.Description, row.Status, row.Agent, row.SortOrder, row.DeclareKey, row.SnoozedUntil, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, tk)
	}
	return out, nil
}

// SnoozeTask sets or clears the absolute deadline that temporarily hides a
// todo task from wakeup detection. A nil deadline clears the snooze.
func (s *Store) SnoozeTask(ctx context.Context, taskID int64, until *time.Time) (domain.Task, error) {
	var snoozedUntil sql.NullString
	if until != nil {
		snoozedUntil = sql.NullString{String: until.UTC().Format(time.RFC3339Nano), Valid: true}
	}

	res, err := taskQueries(s).UpdateTaskSnooze(ctx, sqlcgen.UpdateTaskSnoozeParams{
		SnoozedUntil: snoozedUntil,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
		ID:           taskID,
	})
	if err != nil {
		return domain.Task{}, fmt.Errorf("snooze task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("snooze rows affected: %w", err)
	}
	if n == 0 {
		_, lookupErr := taskQueries(s).TaskExists(ctx, taskID)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
		}
		if lookupErr != nil {
			return domain.Task{}, fmt.Errorf("lookup task after snooze: %w", lookupErr)
		}
		return domain.Task{}, fmt.Errorf("snooze task affected no rows: %d", taskID)
	}
	return s.loadTask(ctx, taskID)
}

func (s *Store) LinkTaskCommit(ctx context.Context, taskID int64, c domain.TaskCommit) error {
	err := taskQueries(s).LinkTaskCommit(ctx, sqlcgen.LinkTaskCommitParams{
		TaskID:       taskID,
		Sha:          c.SHA,
		Subject:      c.Subject,
		FilesChanged: int64(c.FilesChanged),
		Insertions:   int64(c.Insertions),
		Deletions:    int64(c.Deletions),
		CreatedAt:    c.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("link task commit: %w", err)
	}
	return nil
}

func (s *Store) ListTaskCommits(ctx context.Context, taskID int64) ([]domain.TaskCommit, error) {
	rows, err := taskQueries(s).ListTaskCommits(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("query task commits: %w", err)
	}

	out := make([]domain.TaskCommit, 0, len(rows))
	for _, row := range rows {
		createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse task commit created_at: %w", err)
		}
		out = append(out, domain.TaskCommit{
			SHA:          row.Sha,
			Subject:      row.Subject,
			FilesChanged: int(row.FilesChanged),
			Insertions:   int(row.Insertions),
			Deletions:    int(row.Deletions),
			CreatedAt:    createdAt,
		})
	}
	return out, nil
}

func (s *Store) GetTaskGoalID(ctx context.Context, taskID int64) (int64, error) {
	goalID, err := taskQueries(s).GetTaskGoalID(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	if err != nil {
		return 0, fmt.Errorf("lookup goal_id: %w", err)
	}
	return goalID, nil
}

func taskFromRow(row sqlcgen.Task) (domain.Task, error) {
	tk := domain.Task{
		ID:          row.ID,
		GoalID:      row.GoalID,
		Title:       row.Title,
		Description: row.Description,
		Status:      domain.TaskStatus(row.Status),
		Agent:       row.Agent,
		Order:       int(row.SortOrder),
		DeclareKey:  row.DeclareKey,
	}
	if row.SnoozedUntil.Valid {
		t, err := time.Parse(time.RFC3339Nano, row.SnoozedUntil.String)
		if err != nil {
			return domain.Task{}, fmt.Errorf("parse snoozed_until: %w", err)
		}
		tk.SnoozedUntil = &t
	}
	createdAt, err := time.Parse(time.RFC3339, row.CreatedAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("parse created_at: %w", err)
	}
	tk.CreatedAt = createdAt
	updatedAt, err := time.Parse(time.RFC3339, row.UpdatedAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("parse updated_at: %w", err)
	}
	tk.UpdatedAt = updatedAt
	return tk, nil
}

func taskFromFields(id, goalID int64, title, description, status, agent string, sortOrder int64, declareKey string, snoozedUntil sql.NullString, createdAt, updatedAt string) (domain.Task, error) {
	return taskFromRow(sqlcgen.Task{ID: id, GoalID: goalID, Title: title, Description: description, Status: status, Agent: agent, SortOrder: sortOrder, DeclareKey: declareKey, SnoozedUntil: snoozedUntil, CreatedAt: createdAt, UpdatedAt: updatedAt})
}

func taskQueries(s *Store) *sqlcgen.Queries {
	return sqlcgen.New(s.db)
}

// ListOpenTasksClaimedBy returns tasks that still need closing and belong to
// the supplied agent session. A blank agent session ID cannot identify a caller's own claims.
func (s *Store) ListOpenTasksClaimedBy(ctx context.Context, goalID int64, agentSessionID int64) ([]domain.Task, error) {
	if agentSessionID == 0 {
		return []domain.Task{}, nil
	}

	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		return nil, err
	}
	open := make([]domain.Task, 0)
	for _, task := range tasks {
		if task.Status == domain.TaskDone {
			continue
		}
		handoff, err := s.openTaskHandoff(ctx, task.ID)
		if err != nil {
			return nil, fmt.Errorf("lookup task handoff: %w", err)
		}
		if handoff == nil || handoff.ReceivedBy != agentSessionID {
			continue
		}
		open = append(open, task)
	}
	return open, nil
}

// UpdateTask treats the transition to done as a guarded operation.
// Allowing a task with an open Decision to become done would break the
// invariant that human-waiting tasks are derived from Decisions.
func (s *Store) UpdateTask(ctx context.Context, taskID int64, status domain.TaskStatus, agentSessionID int64) (domain.Task, error) {
	var releaseHandoff *TaskHandoff
	var err error
	if status == domain.TaskTodo || status == domain.TaskDone || status == domain.TaskDropped {
		releaseHandoff, err = s.openTaskHandoff(ctx, taskID)
		if err != nil {
			return domain.Task{}, err
		}
		if _, err = s.authorizeTaskStatusRelease(ctx, taskID, status, agentSessionID, releaseHandoff); err != nil {
			return domain.Task{}, err
		}
	}
	return s.updateTask(ctx, taskID, status, releaseHandoff)
}

func (s *Store) authorizeTaskContentUpdate(ctx context.Context, taskID, agentSessionID int64) error {
	goalID, err := sqlcgen.New(s.db).GetTaskGoalID(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	if err != nil {
		return fmt.Errorf("find goal for task %d: %w", taskID, err)
	}

	taskHandoff, err := s.openTaskHandoff(ctx, taskID)
	if err != nil {
		return fmt.Errorf("find live task handoff for task %d: %w", taskID, err)
	}
	if taskHandoff != nil && taskHandoff.ReceivedBy == agentSessionID && agentSessionID != 0 {
		return nil
	}

	goalHandoffErr := s.requireGoalHandoffForTask(ctx, taskID, agentSessionID)
	if goalHandoffErr == nil {
		return nil
	}
	if !errors.Is(goalHandoffErr, ErrTaskHandoffGoalNotHeld) {
		return fmt.Errorf("authorize task content update for task %d: %w", taskID, goalHandoffErr)
	}

	goalHandoff, err := s.openGoalHandoff(ctx, goalID)
	if err != nil {
		return fmt.Errorf("find live goal handoff for task %d: %w", taskID, err)
	}
	if taskHandoff == nil && goalHandoff == nil {
		return nil
	}

	return fmt.Errorf("%w: task %d can only be updated by the task or goal handoff holder", ErrTaskContentNotOwned, taskID)
}

func (s *Store) UpdateTaskContent(ctx context.Context, taskID int64, title, description *string, agentSessionID int64) (domain.Task, error) {
	if title == nil && description == nil {
		return domain.Task{}, errors.New("task content update requires at least one field")
	}
	if taskID == 0 {
		return domain.Task{}, fmt.Errorf("%w: empty id", ErrTaskNotFound)
	}
	if err := s.authorizeTaskContentUpdate(ctx, taskID, agentSessionID); err != nil {
		return domain.Task{}, err
	}

	var titleValue, descriptionValue sql.NullString
	if title != nil {
		titleValue = sql.NullString{String: *title, Valid: true}
	}
	if description != nil {
		descriptionValue = sql.NullString{String: *description, Valid: true}
	}
	result, err := sqlcgen.New(s.db).UpdateTaskContent(ctx, sqlcgen.UpdateTaskContentParams{
		Title:       titleValue,
		Description: descriptionValue,
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		ID:          taskID,
	})
	if err != nil {
		return domain.Task{}, fmt.Errorf("update task content: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("check updated task content: %w", err)
	}
	if affected == 0 {
		task, err := s.loadTask(ctx, taskID)
		if err != nil {
			return domain.Task{}, err
		}
		return domain.Task{}, fmt.Errorf("%w: task %d is %s, not todo or doing", ErrTaskNotEditable, taskID, task.Status)
	}
	return s.loadTask(ctx, taskID)
}

func listOpenTaskDecisions(ctx context.Context, q *sqlcgen.Queries, taskID int64) ([]openTaskDecision, error) {
	rows, err := q.ListAllOpenDecisions(ctx)
	if err != nil {
		return nil, err
	}

	var decisions []openTaskDecision
	for _, row := range rows {
		if !row.TaskID.Valid || row.TaskID.Int64 != taskID {
			continue
		}
		decisions = append(decisions, openTaskDecision{ID: row.ID, Question: row.Question})
	}
	return decisions, nil
}

func taskHasOpenDecisionsError(taskID int64, decisions []openTaskDecision) error {
	details := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		question := strings.TrimSpace(decision.Question)
		if len([]rune(question)) > maxOpenDecisionQuestionLength {
			question = string([]rune(question)[:maxOpenDecisionQuestionLength]) + "…"
		}
		details = append(details, fmt.Sprintf("decision %d asks %q", decision.ID, question))
	}
	if len(details) == 0 {
		return fmt.Errorf("%w: %d", ErrTaskHasOpenDecision, taskID)
	}
	return fmt.Errorf("%w: task %d is blocked by open decisions: %s; wait for a human answer, or if this was intended as a record, withdraw the decision and reissue it with default_option and default_after_ms=0", ErrTaskHasOpenDecision, taskID, strings.Join(details, "; "))
}

func (s *Store) updateTask(ctx context.Context, taskID int64, status domain.TaskStatus, releaseHandoff *TaskHandoff) (domain.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	if status == domain.TaskDone {
		open, err := q.CountOpenDecisionsForTask(ctx, sql.NullInt64{Int64: taskID, Valid: true})
		if err != nil {
			return domain.Task{}, fmt.Errorf("count open decisions: %w", err)
		}
		if open > 0 {
			decisions, err := listOpenTaskDecisions(ctx, q, taskID)
			if err != nil {
				return domain.Task{}, fmt.Errorf("list open decisions: %w", err)
			}
			return domain.Task{}, taskHasOpenDecisionsError(taskID, decisions)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := q.UpdateTaskStatus(ctx, sqlcgen.UpdateTaskStatusParams{
		Status:    string(status),
		UpdatedAt: now,
		ID:        taskID,
	})
	if err != nil {
		return domain.Task{}, fmt.Errorf("update task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		if _, lookupErr := q.TaskExists(ctx, taskID); errors.Is(lookupErr, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
		} else if lookupErr != nil {
			return domain.Task{}, fmt.Errorf("check task after update: %w", lookupErr)
		}
		return domain.Task{}, fmt.Errorf("task claim changed while updating %d; retry the update", taskID)
	}
	if err := tx.Commit(); err != nil {
		return domain.Task{}, fmt.Errorf("commit: %w", err)
	}
	if releaseHandoff != nil {
		if _, err := s.CompleteTaskHandoff(ctx, releaseHandoff.ID, taskID, taskHandoffReleasedReport); err != nil {
			return domain.Task{}, fmt.Errorf("complete task handoff after release: %w", err)
		}
	}

	goalID, err := taskQueries(s).GetTaskGoalID(ctx, taskID)
	if err != nil {
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
	return domain.Task{}, fmt.Errorf("%w after update: %d", ErrTaskNotFound, taskID)
}

func (s *Store) authorizeTaskStatusRelease(ctx context.Context, taskID int64, status domain.TaskStatus, agentSessionID int64, releaseHandoff *TaskHandoff) (string, error) {
	_, err := taskQueries(s).GetTaskForClaim(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
	}
	if err != nil {
		return "", fmt.Errorf("lookup task claim: %w", err)
	}

	if releaseHandoff != nil {
		ownerID := releaseHandoff.ReceivedBy
		isHolder := agentSessionID != 0 && ownerID == agentSessionID
		if isHolder {
			return "", nil
		}
		taskProjectID, err := s.ProjectIDForTask(ctx, taskID)
		if err != nil {
			return "", fmt.Errorf("lookup task project for release: %w", err)
		}
		callerProjectID, sessionProjectErr := s.ProjectIDForAgentSession(ctx, agentSessionID)
		isProjectBound := sessionProjectErr == nil && callerProjectID == taskProjectID
		if isProjectBound {
			return "", nil
		}
		if status == domain.TaskDone || status == domain.TaskDropped {
			return "", fmt.Errorf("task %d has a work lock held by another agent session; only the lock holder or a caller bound to the task's project can set it to %s; if that session is no longer running, return it to todo with atct_task_update, then acquire the work lock with atct_task_claim before retrying", taskID, status)
		}
		if ownerID == 0 || claimIsRunning(ctx, s, ownerID) {
			return "", fmt.Errorf("task %d has a work lock held by another agent session that is still running; only the lock holder or a caller bound to the task's project can release it; wait for it to finish or stop, then return it to todo with atct_task_update and acquire the work lock with atct_task_claim", taskID)
		}
		return "", nil
	}

	return "", nil
}

// ClaimTask atomically assigns a task to one agent session.
func (s *Store) ClaimTask(ctx context.Context, taskID int64, agentSessionID int64) (domain.Task, error) {
	q := taskQueries(s)
	task, err := q.GetTaskForClaim(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
		}
		return domain.Task{}, fmt.Errorf("lookup task claim: %w", err)
	}
	if task.GoalStatus != string(domain.GoalActive) {
		return domain.Task{}, goalNotActiveError(task.GoalID, domain.GoalStatus(task.GoalStatus), "claiming", "claim")
	}

	handoffID := uuid.NewString()
	if err := s.reclaimOpenTaskHandoff(ctx, handoffID, taskID); err != nil {
		return domain.Task{}, mapTaskClaimHandoffError(taskID, err)
	}

	if _, err := s.requestTaskHandoffForClaim(ctx, handoffID, taskID, agentSessionID); err != nil {
		return domain.Task{}, mapTaskClaimHandoffError(taskID, err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoffID, taskID, agentSessionID); err != nil {
		return domain.Task{}, fmt.Errorf("receive task claim handoff: %w", err)
	}
	return s.loadTask(ctx, taskID)
}

func mapTaskClaimHandoffError(taskID int64, err error) error {
	if errors.Is(err, ErrTaskHandoffAlreadyOpen) || strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
		return fmt.Errorf("%w: %d", ErrTaskAlreadyClaimed, taskID)
	}
	return err
}

// ReleaseTaskForHuman clears a task claim for the human stale-claim release path.
func (s *Store) ReleaseTaskForHuman(ctx context.Context, taskID int64) (domain.Task, error) {
	releaseHandoff, err := s.openTaskHandoff(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	return s.updateTask(ctx, taskID, domain.TaskTodo, releaseHandoff)
}

// ReleaseTaskAs clears a task claim through the agent authorization path.
func (s *Store) ReleaseTaskAs(ctx context.Context, taskID int64, agentSessionID int64) (domain.Task, error) {
	return s.UpdateTask(ctx, taskID, domain.TaskTodo, agentSessionID)
}

func (s *Store) loadTask(ctx context.Context, taskID int64) (domain.Task, error) {
	var goalID int64
	goalID, err := taskQueries(s).GetTaskGoalID(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
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
	return domain.Task{}, fmt.Errorf("%w after update: %d", ErrTaskNotFound, taskID)
}
