package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
var ErrTaskFileConflict = errors.New("task file conflict")

const maxTaskFileConflictCandidates = 8

// TaskConflictCandidate identifies a task that can be claimed instead of the
// task that hit a file conflict.
type TaskConflictCandidate struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
}

// TaskFileConflictError keeps the conflict details and actionable alternatives
// while preserving errors.Is(err, ErrTaskFileConflict) for existing callers.
type TaskFileConflictError struct {
	TaskID                string
	Title                 string
	ConflictTaskID        string
	ConflictTaskTitle     string
	File                  string
	Candidates            []TaskConflictCandidate
	OmittedCandidateCount int
}

func (e *TaskFileConflictError) Error() string {
	candidates := []byte("[]")
	if len(e.Candidates) > 0 {
		encoded, err := json.Marshal(e.Candidates)
		if err == nil {
			candidates = encoded
		}
	}
	message := fmt.Sprintf(
		"%s: task %q (%s) conflicts with task %q (%s) on file %q; alternatives: %s",
		ErrTaskFileConflict,
		e.TaskID,
		e.Title,
		e.ConflictTaskID,
		e.ConflictTaskTitle,
		e.File,
		candidates,
	)
	if e.OmittedCandidateCount > 0 {
		message += fmt.Sprintf("; omitted_candidates: %d", e.OmittedCandidateCount)
	}
	return message
}

func (e *TaskFileConflictError) Unwrap() error { return ErrTaskFileConflict }

func goalNotActiveError(goalID string, status domain.GoalStatus, beforeAction, action string) error {
	var stateMessage string
	switch status {
	case domain.GoalProposed:
		stateMessage = fmt.Sprintf("goal %q is not approved; obtain human approval before %s its tasks (承認されていないため、先に人間の承認を得てください)", goalID, beforeAction)
	case domain.GoalDone:
		stateMessage = fmt.Sprintf("goal %q is complete; cannot %s its tasks (ゴールが完了しているため、タスク操作はできません)", goalID, action)
	case domain.GoalDropped:
		stateMessage = fmt.Sprintf("goal %q has been withdrawn; cannot %s its tasks (ゴールが取り下げられているため、タスク操作はできません)", goalID, action)
	default:
		stateMessage = fmt.Sprintf("goal %q has status %q and is not active; cannot %s its tasks (ゴールの状態 %q はアクティブではないため、タスク操作はできません)", goalID, status, action, status)
	}
	return fmt.Errorf("%w: %s", ErrGoalNotActive, stateMessage)
}

// DeclareTasks derives declare_key from the idempotency key and task position.
// The unique (goal_id, declare_key) constraint absorbs duplicate declarations.
// Agents retry and repeat declarations after context compaction, so this prevents task multiplication.
func (s *Store) DeclareTasks(ctx context.Context, goalID, agent, idempotencyKey string, titles []string, descriptions []string, declaredFiles ...[][]string) ([]domain.Task, error) {
	if len(descriptions) != len(titles) {
		return nil, fmt.Errorf("declare tasks: descriptions count %d does not match titles count %d", len(descriptions), len(titles))
	}
	for i, description := range descriptions {
		if strings.TrimSpace(description) == "" {
			return nil, fmt.Errorf("declare tasks: description %d is empty", i)
		}
	}

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
	maxSortOrder, err := q.MaxTaskSortOrder(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("get max task sort order: %w", err)
	}
	for i, title := range titles {
		declareKey := fmt.Sprintf("%s#%d", idempotencyKey, i)
		filesJSON, err := marshalTaskFiles(filesByTask[i])
		if err != nil {
			return nil, fmt.Errorf("encode task %d files: %w", i, err)
		}
		err = q.CreateTask(ctx, sqlcgen.CreateTaskParams{
			ID:           uuid.NewString(),
			GoalID:       goalID,
			Title:        title,
			Description:  descriptions[i],
			Status:       string(domain.TaskTodo),
			Agent:        agent,
			Files:        filesJSON,
			SortOrder:    maxSortOrder + 1 + int64(i),
			DeclareKey:   declareKey,
			ClaimedBy:    "",
			ClaimedAt:    sql.NullString{},
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

func unmarshalTaskFiles(filesJSON string) ([]string, error) {
	if filesJSON == "" {
		return []string{}, nil
	}
	var files []string
	if err := json.Unmarshal([]byte(filesJSON), &files); err != nil {
		return nil, err
	}
	if files == nil {
		return []string{}, nil
	}
	return files, nil
}

func (s *Store) ListTasks(ctx context.Context, goalID string) ([]domain.Task, error) {
	rows, err := taskQueries(s).ListTasks(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}

	var out []domain.Task
	for _, row := range rows {
		tk, err := taskFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, tk)
	}
	return out, nil
}

// SnoozeTask sets or clears the absolute deadline that temporarily hides a
// todo task from wakeup detection. A nil deadline clears the snooze.
func (s *Store) SnoozeTask(ctx context.Context, taskID string, until *time.Time) (domain.Task, error) {
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
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		}
		if lookupErr != nil {
			return domain.Task{}, fmt.Errorf("lookup task after snooze: %w", lookupErr)
		}
		return domain.Task{}, fmt.Errorf("snooze task affected no rows: %s", taskID)
	}
	return s.loadTask(ctx, taskID)
}

func (s *Store) LinkTaskCommit(ctx context.Context, taskID string, c domain.TaskCommit) error {
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

func (s *Store) ListTaskCommits(ctx context.Context, taskID string) ([]domain.TaskCommit, error) {
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

func (s *Store) GetTaskGoalID(ctx context.Context, taskID string) (string, error) {
	goalID, err := taskQueries(s).GetTaskGoalID(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if err != nil {
		return "", fmt.Errorf("lookup goal_id: %w", err)
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
		ClaimedBy:   row.ClaimedBy,
	}
	files, err := unmarshalTaskFiles(row.Files)
	if err != nil {
		return domain.Task{}, fmt.Errorf("parse files: %w", err)
	}
	tk.Files = files
	if row.ClaimedAt.Valid {
		t, err := time.Parse(time.RFC3339, row.ClaimedAt.String)
		if err != nil {
			return domain.Task{}, fmt.Errorf("parse claimed_at: %w", err)
		}
		tk.ClaimedAt = &t
	}
	if row.SnoozedUntil.Valid {
		t, err := time.Parse(time.RFC3339Nano, row.SnoozedUntil.String)
		if err != nil {
			return domain.Task{}, fmt.Errorf("parse snoozed_until: %w", err)
		}
		tk.SnoozedUntil = &t
	}
	if tk.CreatedAt, err = time.Parse(time.RFC3339, row.CreatedAt); err != nil {
		return domain.Task{}, fmt.Errorf("parse created_at: %w", err)
	}
	if tk.UpdatedAt, err = time.Parse(time.RFC3339, row.UpdatedAt); err != nil {
		return domain.Task{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return tk, nil
}

func taskQueries(s *Store) *sqlcgen.Queries {
	return sqlcgen.New(s.db)
}

// ListOpenTasksClaimedBy returns tasks that still need closing and belong to
// the supplied agent session. A blank agent session ID cannot identify a caller's own claims.
func (s *Store) ListOpenTasksClaimedBy(ctx context.Context, goalID, agentSessionID string) ([]domain.Task, error) {
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agentSessionID == "" {
		return []domain.Task{}, nil
	}

	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		return nil, err
	}
	open := make([]domain.Task, 0)
	for _, task := range tasks {
		if task.Status == domain.TaskDone || strings.TrimSpace(task.ClaimedBy) != agentSessionID {
			continue
		}
		open = append(open, task)
	}
	return open, nil
}

// UpdateTask treats the transition to done as a guarded operation.
// Allowing a task with an open Decision to become done would break the
// invariant that human-waiting tasks are derived from Decisions.
func (s *Store) UpdateTask(ctx context.Context, taskID string, status domain.TaskStatus, agentSessionID string) (domain.Task, error) {
	agentSessionID = strings.TrimSpace(agentSessionID)
	var releaseClaim string
	var err error
	if status == domain.TaskTodo || status == domain.TaskDone || status == domain.TaskDropped {
		releaseClaim, err = s.authorizeTaskStatusRelease(ctx, taskID, status, agentSessionID)
		if err != nil {
			return domain.Task{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	if status == domain.TaskDone {
		open, err := q.CountOpenDecisionsForTask(ctx, sql.NullString{String: taskID, Valid: true})
		if err != nil {
			return domain.Task{}, fmt.Errorf("count open decisions: %w", err)
		}
		if open > 0 {
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskHasOpenDecision, taskID)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var res sql.Result
	if status == domain.TaskTodo || status == domain.TaskDone || status == domain.TaskDropped {
		res, err = q.UpdateTaskStatusAndReleaseClaim(ctx, sqlcgen.UpdateTaskStatusAndReleaseClaimParams{
			Status:    string(status),
			UpdatedAt: now,
			ID:        taskID,
			ClaimedBy: releaseClaim,
		})
	} else {
		res, err = q.UpdateTaskStatus(ctx, sqlcgen.UpdateTaskStatusParams{
			Status:    string(status),
			UpdatedAt: now,
			ID:        taskID,
		})
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("update task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		if _, lookupErr := q.TaskExists(ctx, taskID); errors.Is(lookupErr, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		} else if lookupErr != nil {
			return domain.Task{}, fmt.Errorf("check task after update: %w", lookupErr)
		}
		return domain.Task{}, fmt.Errorf("task claim changed while updating %s; retry the update", taskID)
	}
	if err := tx.Commit(); err != nil {
		return domain.Task{}, fmt.Errorf("commit: %w", err)
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
	return domain.Task{}, fmt.Errorf("%w after update: %s", ErrTaskNotFound, taskID)
}

func (s *Store) authorizeTaskStatusRelease(ctx context.Context, taskID string, status domain.TaskStatus, agentSessionID string) (string, error) {
	task, err := taskQueries(s).GetTaskForClaim(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if err != nil {
		return "", fmt.Errorf("lookup task claim: %w", err)
	}

	claimedBy := strings.TrimSpace(task.ClaimedBy)
	if claimedBy == "" || claimedBy == agentSessionID {
		return claimedBy, nil
	}

	if status == domain.TaskDone || status == domain.TaskDropped {
		return "", fmt.Errorf("task %q has a work lock held by another agent session; only the lock holder can set it to %s; if that session is no longer running, return it to todo with atct_task_update, then acquire the work lock with atct_task_claim before retrying", taskID, status)
	}

	projectID, err := s.ProjectIDForTask(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("find project for task claim: %w", err)
	}
	running, stale, err := ClaimLiveness(ctx, s, projectID)
	if err != nil {
		return "", fmt.Errorf("check task claim liveness: %w", err)
	}
	if taskClaimMatches(running, taskID, claimedBy) {
		return "", fmt.Errorf("task %q has a work lock held by another agent session that is still running; wait for it to finish or stop, then return it to todo with atct_task_update and acquire the work lock with atct_task_claim", taskID)
	}
	if taskClaimMatches(stale, taskID, claimedBy) {
		return claimedBy, nil
	}
	return "", fmt.Errorf("task %q has a work lock held by another agent session, but its work lock changed while checking whether it is running; retry the update after confirming the work lock is stale", taskID)
}

func taskClaimMatches(tasks []domain.Task, taskID, claimedBy string) bool {
	for _, task := range tasks {
		if task.ID == taskID && strings.TrimSpace(task.ClaimedBy) == claimedBy {
			return true
		}
	}
	return false
}

// ClaimTask atomically assigns a task to one agent session.
func (s *Store) ClaimTask(ctx context.Context, taskID, agentSessionID string) (domain.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin claim tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	task, err := q.GetTaskForClaim(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		}
		return domain.Task{}, fmt.Errorf("lookup task claim: %w", err)
	}
	if task.GoalStatus != string(domain.GoalActive) {
		return domain.Task{}, goalNotActiveError(task.GoalID, domain.GoalStatus(task.GoalStatus), "claiming", "claim")
	}
	if task.ClaimedBy != "" {
		return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskAlreadyClaimed, taskID)
	}

	files, err := unmarshalTaskFiles(task.Files)
	if err != nil {
		return domain.Task{}, fmt.Errorf("parse task files: %w", err)
	}
	if len(files) > 0 {
		if err := rejectTaskFileConflict(ctx, q, task.GoalID, taskID, task.Title, files, agentSessionID); err != nil {
			return domain.Task{}, err
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := q.ClaimTask(ctx, sqlcgen.ClaimTaskParams{
		ClaimedBy: agentSessionID,
		ClaimedAt: sql.NullString{String: now, Valid: true},
		UpdatedAt: now,
		ID:        taskID,
	})
	if err != nil {
		return domain.Task{}, fmt.Errorf("claim task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("claim rows affected: %w", err)
	}
	if n == 0 {
		currentClaim, err := q.GetTaskClaimedBy(ctx, taskID)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
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

type taskClaimConflictInfo struct {
	ID    string
	Title string
	Files []string
}

func rejectTaskFileConflict(ctx context.Context, q *sqlcgen.Queries, goalID, taskID, title string, files []string, agentSessionID string) error {
	rows, err := q.ListClaimedTasksForConflict(ctx, sqlcgen.ListClaimedTasksForConflictParams{
		ID:        taskID,
		ClaimedBy: agentSessionID,
		Status:    string(domain.TaskDone),
		Status_2:  string(domain.TaskDropped),
	})
	if err != nil {
		return fmt.Errorf("query claimed tasks: %w", err)
	}

	var claimedTasks []taskClaimConflictInfo
	for _, row := range rows {
		otherFiles, err := unmarshalTaskFiles(row.Files)
		if err != nil {
			return fmt.Errorf("parse claimed task files: %w", err)
		}
		claimedTasks = append(claimedTasks, taskClaimConflictInfo{
			ID: row.ID, Title: row.Title, Files: otherFiles,
		})
	}

	for _, other := range claimedTasks {
		if file, ok := firstOverlappingFile(files, other.Files); ok {
			candidates, omitted, err := taskFileConflictCandidates(ctx, q, goalID, taskID, claimedTasks)
			if err != nil {
				return err
			}
			return &TaskFileConflictError{
				TaskID:                taskID,
				Title:                 title,
				ConflictTaskID:        other.ID,
				ConflictTaskTitle:     other.Title,
				File:                  file,
				Candidates:            candidates,
				OmittedCandidateCount: omitted,
			}
		}
	}
	return nil
}

func taskFileConflictCandidates(ctx context.Context, q *sqlcgen.Queries, goalID, taskID string, claimedTasks []taskClaimConflictInfo) ([]TaskConflictCandidate, int, error) {
	rows, err := q.ListTaskAlternatives(ctx, sqlcgen.ListTaskAlternativesParams{
		GoalID: goalID,
		ID:     taskID,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("query task alternatives: %w", err)
	}

	candidates := make([]TaskConflictCandidate, 0, maxTaskFileConflictCandidates)
	var omitted int
	for _, row := range rows {
		if row.ClaimedBy != "" || row.Status == string(domain.TaskDone) || row.Status == string(domain.TaskDropped) {
			continue
		}
		files, err := unmarshalTaskFiles(row.Files)
		if err != nil {
			return nil, 0, fmt.Errorf("parse task alternative files: %w", err)
		}
		candidate := taskClaimConflictInfo{ID: row.ID, Title: row.Title, Files: files}
		blocked := false
		for _, claimed := range claimedTasks {
			if _, ok := firstOverlappingFile(candidate.Files, claimed.Files); ok {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		if len(candidates) >= maxTaskFileConflictCandidates {
			omitted++
			continue
		}
		candidates = append(candidates, TaskConflictCandidate{TaskID: candidate.ID, Title: candidate.Title})
	}
	return candidates, omitted, nil
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
	res, err := taskQueries(s).ReleaseTask(ctx, sqlcgen.ReleaseTaskParams{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		ID:        taskID,
	})
	if err != nil {
		return domain.Task{}, fmt.Errorf("release task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("release rows affected: %w", err)
	}
	if n == 0 {
		_, err := taskQueries(s).TaskExists(ctx, taskID)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		}
		if err != nil {
			return domain.Task{}, fmt.Errorf("lookup task: %w", err)
		}
	}
	return s.loadTask(ctx, taskID)
}

func (s *Store) loadTask(ctx context.Context, taskID string) (domain.Task, error) {
	var goalID string
	goalID, err := taskQueries(s).GetTaskGoalID(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
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
	return domain.Task{}, fmt.Errorf("%w after update: %s", ErrTaskNotFound, taskID)
}
