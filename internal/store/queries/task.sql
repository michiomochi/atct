-- name: CreateTask :one
INSERT INTO tasks (
  goal_id, title, description, status, agent, sort_order, declare_key,
  snoozed_until, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(goal_id, declare_key) DO UPDATE SET id = tasks.id
RETURNING id;

-- name: GetTaskCreateHandoff :one
SELECT * FROM task_create_handoffs WHERE id = ?;

-- name: GetTaskCreateHandoffForPlan :one
SELECT * FROM task_create_handoffs WHERE plan_handoff_id = ?;

-- name: ListTaskCreateHandoffs :many
SELECT * FROM task_create_handoffs WHERE goal_id = ? ORDER BY requested_at, id;

-- name: ListTaskCreateHandoffTaskIDs :many
SELECT task_id FROM task_create_handoff_tasks WHERE handoff_id = ? ORDER BY task_id;

-- name: CreateTaskCreateHandoff :exec
INSERT INTO task_create_handoffs (id, plan_handoff_id, goal_id, requested_by, requested_at, request_report)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ReceiveTaskCreateHandoff :execresult
UPDATE task_create_handoffs SET received_by = ?, received_at = ?
WHERE id = ? AND received_by IS NULL;

-- name: LinkTaskCreateHandoffTask :exec
INSERT OR IGNORE INTO task_create_handoff_tasks (handoff_id, task_id) VALUES (?, ?);

-- name: CountUndelegatedTaskCreateHandoffTasks :one
SELECT COUNT(*) FROM task_create_handoff_tasks m WHERE m.handoff_id = ?
AND NOT EXISTS (SELECT 1 FROM task_handoffs h WHERE h.task_id = m.task_id AND h.requested_at IS NOT NULL);

-- name: CompleteTaskCreateHandoff :exec
UPDATE task_create_handoffs SET completed_by = ?, completed_at = ?, complete_report = ? WHERE id = ?;

-- name: ListTasks :many
SELECT
  id, goal_id, title, description, status, agent, sort_order, declare_key,
  snoozed_until, created_at, updated_at
FROM tasks
WHERE goal_id = ?
ORDER BY sort_order, id;

-- name: UpdateTaskSnooze :execresult
UPDATE tasks
SET snoozed_until = ?, updated_at = ?
WHERE id = ?;

-- name: DropOpenTasksForGoal :execresult
UPDATE tasks SET status = 'dropped', updated_at = ?
WHERE goal_id = ? AND status IN ('todo', 'doing');

-- name: MaxTaskSortOrder :one
SELECT CAST(COALESCE(MAX(sort_order), -1) AS INTEGER) AS sort_order
FROM tasks
WHERE goal_id = ?;

-- name: CountOpenDecisionsForTask :one
SELECT COUNT(*)
FROM decisions
WHERE task_id = ? AND status = 'open';

-- name: UpdateTaskStatus :execresult
UPDATE tasks
SET status = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateTaskContent :execresult
UPDATE tasks
SET title = COALESCE(sqlc.narg('title'), title),
    description = COALESCE(sqlc.narg('description'), description),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND status IN ('todo', 'doing');

-- name: LinkTaskCommit :exec
INSERT OR REPLACE INTO task_commits (
  task_id, sha, subject, files_changed, insertions, deletions, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListTaskCommits :many
SELECT sha, subject, files_changed, insertions, deletions, created_at
FROM task_commits
WHERE task_id = ?
ORDER BY created_at ASC;

-- name: GetTaskGoalID :one
SELECT goal_id
FROM tasks
WHERE id = ?;

-- name: GetTaskForClaim :one
SELECT t.goal_id, t.title, t.description, t.status,
       g.status AS goal_status
FROM tasks AS t
JOIN goals AS g ON g.id = t.goal_id
WHERE t.id = ?;

-- name: ReleaseTask :execresult
UPDATE tasks
SET status = 'todo', updated_at = ?
WHERE id = ?;

-- name: TaskExists :one
SELECT id
FROM tasks
WHERE id = ?;

-- name: RegisterAgentSession :one
INSERT INTO agent_sessions (project_id, pid, started_at, registered_at)
VALUES (NULL, ?, ?, ?)
RETURNING id;

-- name: RegisterAgentSessionWithProject :one
INSERT INTO agent_sessions (project_id, pid, started_at, registered_at)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: GetAgentSessionIDByKey :one
SELECT id
FROM agent_sessions
WHERE session_key = ?;

-- name: UpdateAgentSessionKey :exec
UPDATE agent_sessions
SET session_key = ?
WHERE id = ?;

-- name: UpdateAgentSessionProcessIdentity :exec
UPDATE agent_sessions
SET pid = ?, started_at = ?, registered_at = ?
WHERE id = ?;

-- name: DeleteExpiredAgentSessions :exec
DELETE FROM agent_sessions
WHERE registered_at < ?;

-- name: UpdateAgentSessionProject :execresult
UPDATE agent_sessions
SET project_id = ?
WHERE id = ?;

-- name: InsertAgentSessionAssociation :exec
INSERT INTO agent_sessions (id, project_id, registered_at)
VALUES (?, ?, ?);

-- name: DeleteExpiredAgentSessionsExcept :exec
DELETE FROM agent_sessions
WHERE id <> ? AND registered_at < ?;

-- name: GetLatestAgentSessionID :one
SELECT id
FROM agent_sessions
WHERE project_id = ?
ORDER BY registered_at DESC, id DESC
LIMIT 1;

-- name: GetAgentSessionProjectID :one
SELECT project_id
FROM agent_sessions
WHERE id = ?;

-- name: GetTaskProjectID :one
SELECT g.project_id
FROM tasks AS t
JOIN goals AS g ON g.id = t.goal_id
WHERE t.id = ?;

-- name: GetAgentSessionLiveness :one
SELECT pid, started_at
FROM agent_sessions
WHERE id = ?;

-- name: GetTaskHandoff :one
SELECT id, task_id, requested_by, received_by,
       requested_at, received_at, completed_report_at,
       request_report, complete_report,
       review_requested_by, review_requested_at, review_request_report,
       review_received_by, review_received_at,
       review_rejected_at, review_reject_report, review_rejection_received_by, review_rejection_received_at
FROM task_handoffs
WHERE id = ?;

-- name: ListTaskHandoffs :many
SELECT id, task_id, requested_by, received_by,
       requested_at, received_at, completed_report_at,
       request_report, complete_report,
       review_requested_by, review_requested_at, review_request_report,
       review_received_by, review_received_at,
       review_rejected_at, review_reject_report, review_rejection_received_by, review_rejection_received_at
FROM task_handoffs
WHERE task_id = ?
ORDER BY id;

-- name: ListOpenTaskHandoffsForGoal :many
SELECT th.id, th.task_id, th.requested_by, th.received_by,
       th.requested_at, th.received_at, th.completed_report_at,
       th.request_report, th.complete_report,
       th.review_requested_by, th.review_requested_at, th.review_request_report,
       th.review_received_by, th.review_received_at,
       th.review_rejected_at, th.review_reject_report, th.review_rejection_received_by, th.review_rejection_received_at
FROM task_handoffs AS th
JOIN tasks AS t ON t.id = th.task_id
WHERE t.goal_id = ?
  AND th.completed_report_at IS NULL
ORDER BY th.id;

-- The partial unique index idx_task_handoffs_open_task_id guarantees at most
-- one open handoff per task, so filtering completed_report_at is equivalent
-- to selecting the first open handoff in the legacy implementation.
-- name: ListOpenTaskHandoffClaims :many
SELECT t.id, t.goal_id, t.title, t.description, t.status, t.agent,
       t.sort_order, t.declare_key, t.snoozed_until, t.created_at, t.updated_at,
       th.requested_by, th.received_by
FROM task_handoffs AS th
JOIN tasks AS t ON t.id = th.task_id
JOIN goals AS g ON g.id = t.goal_id
WHERE g.project_id = ?
  AND th.completed_report_at IS NULL
ORDER BY g.created_at, g.id, t.sort_order, t.id;

-- name: GetTaskHandoffTaskID :one
SELECT task_id
FROM task_handoffs
WHERE id = ?;

-- name: RequestTaskHandoff :exec
INSERT INTO task_handoffs (id, task_id, requested_by, requested_at, request_report)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  requested_by = COALESCE(task_handoffs.requested_by, excluded.requested_by),
  requested_at = COALESCE(task_handoffs.requested_at, excluded.requested_at),
  request_report = COALESCE(task_handoffs.request_report, excluded.request_report);

-- name: ReceiveTaskHandoff :execresult
UPDATE task_handoffs
SET received_by = ?, received_at = ?
WHERE id = ? AND task_id = ? AND requested_at IS NOT NULL;

-- name: RequestTaskHandoffReview :execresult
UPDATE task_handoffs
SET review_requested_by = ?,
    review_requested_at = ?,
    review_request_report = ?,
    review_received_by = NULL,
    review_received_at = NULL,
    review_rejected_at = NULL,
    review_reject_report = NULL,
    review_rejection_received_by = NULL,
    review_rejection_received_at = NULL
WHERE id = ? AND task_id = ?
  AND requested_at IS NOT NULL
  AND received_at IS NOT NULL
  AND completed_report_at IS NULL
  AND (review_requested_at IS NULL OR (review_rejected_at IS NOT NULL AND review_rejection_received_at IS NOT NULL));

-- name: ReceiveTaskHandoffReview :execresult
UPDATE task_handoffs
SET review_received_by = ?, review_received_at = ?
WHERE id = ? AND task_id = ?
  AND review_requested_at IS NOT NULL
  AND review_received_at IS NULL
  AND completed_report_at IS NULL;

-- name: RejectTaskHandoffReview :execresult
UPDATE task_handoffs
SET review_received_by = NULL,
    review_received_at = NULL,
    review_rejected_at = ?,
    review_reject_report = ?
WHERE id = ? AND task_id = ?
  AND review_received_at IS NOT NULL
  AND completed_report_at IS NULL;

-- name: ReceiveTaskHandoffReviewRejection :execresult
UPDATE task_handoffs
SET review_rejection_received_by = ?, review_rejection_received_at = ?
WHERE id = ? AND task_id = ?
  AND review_rejected_at IS NOT NULL
  AND review_rejection_received_at IS NULL
  AND received_by = ?;

-- name: CompleteTaskHandoffByReviewer :execresult
UPDATE task_handoffs
SET completed_report_at = ?, complete_report = ?
WHERE id = ? AND task_id = ?
  AND requested_at IS NOT NULL
  AND received_at IS NOT NULL
  AND review_received_by = ?
  AND review_received_at IS NOT NULL
  AND completed_report_at IS NULL;

-- name: CompleteTaskHandoff :execresult
UPDATE task_handoffs
SET completed_report_at = ?, complete_report = ?
WHERE id = ? AND task_id = ? AND requested_at IS NOT NULL AND completed_report_at IS NULL;

-- name: AmendTaskHandoffReport :execresult
UPDATE task_handoffs
SET complete_report = ?
WHERE id = ? AND task_id = ? AND completed_report_at IS NOT NULL;

-- name: GetGoalHandoff :one
SELECT id, goal_id, requested_by, received_by,
       requested_at, received_at, completed_report_at,
       request_report, complete_report,
       review_requested_by, review_requested_at, review_request_report,
       review_received_by, review_received_at,
       review_rejected_at, review_reject_report, review_rejection_received_by, review_rejection_received_at
FROM goal_handoffs
WHERE id = ?;

-- name: ListGoalHandoffs :many
SELECT id, goal_id, requested_by, received_by,
       requested_at, received_at, completed_report_at,
       request_report, complete_report,
       review_requested_by, review_requested_at, review_request_report,
       review_received_by, review_received_at,
       review_rejected_at, review_reject_report, review_rejection_received_by, review_rejection_received_at
FROM goal_handoffs
WHERE goal_id = ?
ORDER BY id;

-- name: GetGoalHandoffGoalID :one
SELECT goal_id
FROM goal_handoffs
WHERE id = ?;

-- name: RequestGoalHandoff :exec
INSERT INTO goal_handoffs (id, goal_id, requested_by, requested_at, request_report)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  requested_by = COALESCE(goal_handoffs.requested_by, excluded.requested_by),
  requested_at = COALESCE(goal_handoffs.requested_at, excluded.requested_at),
  request_report = COALESCE(goal_handoffs.request_report, excluded.request_report);

-- name: ReceiveGoalHandoff :execresult
UPDATE goal_handoffs
SET received_by = ?, received_at = ?
WHERE id = ? AND goal_id = ? AND requested_at IS NOT NULL;

-- name: RequestGoalHandoffReview :execresult
UPDATE goal_handoffs
SET review_requested_by = ?,
    review_requested_at = ?,
    review_request_report = ?,
    review_received_by = NULL,
    review_received_at = NULL,
    review_rejected_at = NULL,
    review_reject_report = NULL,
    review_rejection_received_by = NULL,
    review_rejection_received_at = NULL
WHERE id = ? AND goal_id = ?
  AND requested_at IS NOT NULL
  AND received_at IS NOT NULL
  AND completed_report_at IS NULL
  AND (review_requested_at IS NULL OR (review_rejected_at IS NOT NULL AND review_rejection_received_at IS NOT NULL));

-- name: ReceiveGoalHandoffReview :execresult
UPDATE goal_handoffs
SET review_received_by = ?, review_received_at = ?
WHERE id = ? AND goal_id = ?
  AND review_requested_at IS NOT NULL
  AND review_received_at IS NULL
  AND completed_report_at IS NULL;

-- name: RejectGoalHandoffReview :execresult
UPDATE goal_handoffs
SET review_received_by = NULL,
    review_received_at = NULL,
    review_rejected_at = ?,
    review_reject_report = ?
WHERE id = ? AND goal_id = ?
  AND review_received_at IS NOT NULL
  AND completed_report_at IS NULL;

-- name: ReceiveGoalHandoffReviewRejection :execresult
UPDATE goal_handoffs
SET review_rejection_received_by = ?, review_rejection_received_at = ?
WHERE id = ? AND goal_id = ?
  AND review_rejected_at IS NOT NULL
  AND review_rejection_received_at IS NULL
  AND received_by = ?;

-- name: CompleteGoalHandoffByReviewer :execresult
UPDATE goal_handoffs
SET completed_report_at = ?, complete_report = ?
WHERE id = ? AND goal_id = ?
  AND requested_at IS NOT NULL
  AND received_at IS NOT NULL
  AND review_received_by = ?
  AND review_received_at IS NOT NULL
  AND completed_report_at IS NULL;

-- name: CompleteGoalHandoff :execresult
UPDATE goal_handoffs
SET completed_report_at = ?, complete_report = ?
WHERE id = ? AND goal_id = ? AND requested_at IS NOT NULL AND completed_report_at IS NULL;

-- name: AmendGoalHandoffReport :execresult
UPDATE goal_handoffs
SET complete_report = ?
WHERE id = ? AND goal_id = ? AND completed_report_at IS NOT NULL;

-- name: GetPlanHandoff :one
SELECT plan_handoffs.*
FROM plan_handoffs
WHERE id = ?;

-- name: ListPlanHandoffs :many
SELECT plan_handoffs.*
FROM plan_handoffs
WHERE goal_id = ?
ORDER BY id;

-- name: RequestPlanHandoffReview :execresult
INSERT INTO plan_handoffs (
  id, goal_id, review_requested_by, review_requested_at, review_request_report
)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  review_requested_by = excluded.review_requested_by,
  review_requested_at = excluded.review_requested_at,
  review_request_report = excluded.review_request_report,
  review_received_by = NULL,
  review_received_at = NULL,
  review_rejected_at = NULL,
  review_reject_report = NULL,
  review_rejection_received_by = NULL,
  review_rejection_received_at = NULL
WHERE plan_handoffs.goal_id = excluded.goal_id
  AND plan_handoffs.completed_report_at IS NULL
  AND plan_handoffs.review_rejected_at IS NOT NULL
  AND plan_handoffs.review_rejection_received_at IS NOT NULL;

-- name: ReceivePlanHandoffReview :execresult
UPDATE plan_handoffs
SET review_received_by = ?, review_received_at = ?
WHERE id = ? AND goal_id = ?
  AND review_requested_at IS NOT NULL
  AND review_received_at IS NULL
  AND completed_report_at IS NULL;

-- name: RejectPlanHandoffReview :execresult
UPDATE plan_handoffs
SET review_received_by = NULL,
    review_received_at = NULL,
    review_rejected_at = ?,
    review_reject_report = ?
WHERE id = ? AND goal_id = ?
  AND review_received_at IS NOT NULL
  AND completed_report_at IS NULL;

-- name: ReceivePlanHandoffReviewRejection :execresult
UPDATE plan_handoffs
SET review_rejection_received_by = ?, review_rejection_received_at = ?
WHERE id = ? AND goal_id = ?
  AND review_rejected_at IS NOT NULL
  AND review_rejection_received_at IS NULL
  AND review_requested_by = ?;

-- name: CompletePlanHandoff :execresult
UPDATE plan_handoffs
SET completed_report_at = ?, complete_report = ?
WHERE id = ? AND goal_id = ?
  AND review_received_by = ?
  AND review_received_at IS NOT NULL
  AND completed_report_at IS NULL;
