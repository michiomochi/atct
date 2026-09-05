-- name: GetTaskHandoffEntryState :one
SELECT requested_by, received_by, completed_report_at
FROM task_handoffs
WHERE id = ?;

-- name: GetGoalHandoffEntryState :one
SELECT requested_by, received_by, completed_report_at
FROM goal_handoffs
WHERE id = ?;

-- name: CountTaskHandoffRequestEntries :one
SELECT COUNT(*)
FROM task_handoff_entries
WHERE handoff_id = ? AND kind = 'request';

-- name: CountGoalHandoffRequestEntries :one
SELECT COUNT(*)
FROM goal_handoff_entries
WHERE handoff_id = ? AND kind = 'request';

-- name: TaskHandoffEntryExists :one
SELECT COUNT(*)
FROM task_handoff_entries
WHERE handoff_id = ? AND id = ?;

-- name: GoalHandoffEntryExists :one
SELECT COUNT(*)
FROM goal_handoff_entries
WHERE handoff_id = ? AND id = ?;

-- name: CreateTaskHandoffEntry :one
INSERT INTO task_handoff_entries (
  handoff_id, kind, body, author_session_id, in_reply_to_id, created_at
)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: CreateGoalHandoffEntry :one
INSERT INTO goal_handoff_entries (
  handoff_id, kind, body, author_session_id, in_reply_to_id, created_at
)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListTaskHandoffEntries :many
SELECT id, handoff_id, kind, body, author_session_id, in_reply_to_id, created_at
FROM task_handoff_entries
WHERE handoff_id = sqlc.arg('handoff_id') AND id > sqlc.arg('cursor')
ORDER BY id ASC
LIMIT sqlc.arg('limit');

-- name: ListGoalHandoffEntries :many
SELECT id, handoff_id, kind, body, author_session_id, in_reply_to_id, created_at
FROM goal_handoff_entries
WHERE handoff_id = sqlc.arg('handoff_id') AND id > sqlc.arg('cursor')
ORDER BY id ASC
LIMIT sqlc.arg('limit');
