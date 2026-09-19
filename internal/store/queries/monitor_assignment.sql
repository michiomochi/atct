-- name: GetMonitorCommanderProjectID :one
SELECT id
FROM projects
WHERE claimed_by = sqlc.arg('agent_session_id')
ORDER BY created_at, id
LIMIT 1;

-- name: GetMonitorSubcommanderAssignment :one
SELECT g.project_id, gh.goal_id
FROM goal_handoffs AS gh
JOIN goals AS g ON g.id = gh.goal_id
WHERE gh.received_by = sqlc.arg('agent_session_id')
  AND gh.received_at IS NOT NULL
  AND gh.completed_report_at IS NULL
  AND gh.recovered_at IS NULL
ORDER BY g.created_at, g.id
LIMIT 1;

-- name: ListMonitorExecutorAssignments :many
SELECT g.project_id, t.goal_id, th.task_id
FROM task_handoffs AS th
JOIN tasks AS t ON t.id = th.task_id
JOIN goals AS g ON g.id = t.goal_id
WHERE th.received_by = sqlc.arg('agent_session_id')
  AND th.received_at IS NOT NULL
  AND th.completed_report_at IS NULL
  AND th.recovered_at IS NULL
ORDER BY th.task_id;
