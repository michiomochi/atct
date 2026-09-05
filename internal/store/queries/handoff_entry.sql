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
WHERE handoff_id = ? AND entry_id = ?;

-- name: GoalHandoffEntryExists :one
SELECT COUNT(*)
FROM goal_handoff_entries
WHERE handoff_id = ? AND entry_id = ?;

-- name: MaxTaskHandoffEntrySequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM task_handoff_entries
WHERE handoff_id = ?;

-- name: MaxGoalHandoffEntrySequence :one
SELECT COALESCE(MAX(sequence), 0) + 1
FROM goal_handoff_entries
WHERE handoff_id = ?;

-- name: CreateTaskHandoffEntry :exec
INSERT INTO task_handoff_entries (
  entry_id, handoff_id, sequence, kind, body, author_session_id,
  relates_to, source, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateGoalHandoffEntry :exec
INSERT INTO goal_handoff_entries (
  entry_id, handoff_id, sequence, kind, body, author_session_id,
  relates_to, source, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListTaskHandoffEntries :many
SELECT entry_id, handoff_id, sequence, kind, body, author_session_id,
       relates_to, source, created_at
FROM task_handoff_entries
WHERE handoff_id = sqlc.arg('handoff_id') AND sequence > sqlc.arg('cursor')
ORDER BY sequence ASC
LIMIT sqlc.arg('limit');

-- name: ListGoalHandoffEntries :many
SELECT entry_id, handoff_id, sequence, kind, body, author_session_id,
       relates_to, source, created_at
FROM goal_handoff_entries
WHERE handoff_id = sqlc.arg('handoff_id') AND sequence > sqlc.arg('cursor')
ORDER BY sequence ASC
LIMIT sqlc.arg('limit');
