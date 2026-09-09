-- name: PruneMonitorHealth :exec
DELETE FROM monitor_health
WHERE last_seen_at < sqlc.arg('cutoff')
   OR (stopped_at IS NOT NULL AND stopped_at < sqlc.arg('cutoff'));

-- name: UpsertMonitorHealth :exec
INSERT INTO monitor_health (
  monitor_id, agent_key, cwd, role, project_id, goal_id, task_id, pid,
  process_started_at, state, reason, transitioned_at, last_seen_at, stopped_at
)
VALUES (
  sqlc.arg('monitor_id'), sqlc.arg('agent_key'), sqlc.arg('cwd'), sqlc.arg('role'),
  sqlc.arg('project_id'), sqlc.narg('goal_id'), sqlc.narg('task_id'), sqlc.arg('pid'),
  sqlc.arg('process_started_at'), sqlc.arg('state'), sqlc.arg('reason'),
  sqlc.arg('transitioned_at'), sqlc.arg('last_seen_at'), NULL
)
ON CONFLICT(monitor_id) DO UPDATE SET
  agent_key = excluded.agent_key,
  cwd = excluded.cwd,
  role = excluded.role,
  project_id = excluded.project_id,
  goal_id = excluded.goal_id,
  task_id = excluded.task_id,
  pid = excluded.pid,
  process_started_at = excluded.process_started_at,
  state = excluded.state,
  reason = excluded.reason,
  transitioned_at = CASE WHEN excluded.transitioned_at = '' THEN monitor_health.transitioned_at ELSE excluded.transitioned_at END,
  last_seen_at = excluded.last_seen_at,
  stopped_at = NULL;

-- name: StopMonitorHealth :execresult
UPDATE monitor_health
SET state = 'stopped', reason = 'stopped', stopped_at = ?,
    transitioned_at = ?, last_seen_at = ?
WHERE monitor_id = ?;

-- name: ListMonitorHealth :many
SELECT monitor_id, agent_key, cwd, role, project_id, goal_id, task_id, pid,
       process_started_at, state, reason, transitioned_at, last_seen_at, stopped_at
FROM monitor_health
WHERE project_id = ? AND last_seen_at >= ? AND stopped_at IS NULL
ORDER BY transitioned_at DESC, monitor_id;
