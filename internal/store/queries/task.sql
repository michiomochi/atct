-- name: CreateTask :exec
INSERT INTO tasks (
  id, goal_id, title, description, status, agent, files, sort_order, declare_key,
  claimed_by, claimed_at, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(goal_id, declare_key) DO NOTHING;

-- name: ListTasks :many
SELECT
  id, goal_id, title, description, status, agent, files, sort_order, declare_key,
  claimed_by, claimed_at, created_at, updated_at
FROM tasks
WHERE goal_id = ?
ORDER BY sort_order;

-- name: CountOpenDecisionsForTask :one
SELECT COUNT(*)
FROM decisions
WHERE task_id = ? AND status = 'open';

-- name: UpdateTaskStatus :execresult
UPDATE tasks
SET status = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateTaskStatusAndReleaseClaim :execresult
UPDATE tasks
SET status = ?, claimed_by = '', claimed_at = NULL, updated_at = ?
WHERE id = ?;

-- name: GetTaskGoalID :one
SELECT goal_id
FROM tasks
WHERE id = ?;

-- name: GetTaskForClaim :one
SELECT goal_id, title, description, status, claimed_by, files
FROM tasks
WHERE id = ?;

-- name: ListClaimedTasksForConflict :many
SELECT id, title, description, status, claimed_by, files
FROM tasks
WHERE id <> ?
  AND claimed_by <> ''
  AND claimed_by <> ?
  AND status NOT IN (?, ?)
ORDER BY sort_order, id;

-- name: ClaimTask :execresult
UPDATE tasks
SET claimed_by = ?, claimed_at = ?, updated_at = ?
WHERE id = ? AND claimed_by = '';

-- name: GetTaskClaimedBy :one
SELECT claimed_by
FROM tasks
WHERE id = ?;

-- name: ListTaskAlternatives :many
SELECT id, title, description, status, claimed_by, files
FROM tasks
WHERE goal_id = ?
  AND id <> ?
ORDER BY sort_order, id;

-- name: ReleaseTask :execresult
UPDATE tasks
SET claimed_by = '', claimed_at = NULL, updated_at = ?
WHERE id = ?;

-- name: TaskExists :one
SELECT id
FROM tasks
WHERE id = ?;
