-- name: GetAgentSessionKey :one
SELECT session_key
FROM agent_sessions
WHERE id = ?;

-- name: UpsertOrchestrationScope :exec
INSERT INTO orchestration_scope (
  scope_key, project_id, goal_id, task_id, role, agent_session_id, agent_key,
  source_generation, active, created_at, updated_at
)
VALUES (
  sqlc.arg('scope_key'), sqlc.arg('project_id'), sqlc.narg('goal_id'),
  sqlc.narg('task_id'), sqlc.arg('role'), sqlc.arg('agent_session_id'),
  sqlc.arg('agent_key'), sqlc.arg('source_generation'), 1,
  sqlc.arg('created_at'), sqlc.arg('updated_at')
)
ON CONFLICT(scope_key) DO UPDATE SET
  project_id = excluded.project_id,
  goal_id = excluded.goal_id,
  task_id = excluded.task_id,
  role = excluded.role,
  agent_session_id = excluded.agent_session_id,
  agent_key = excluded.agent_key,
  source_generation = excluded.source_generation,
  active = 1,
  updated_at = excluded.updated_at;

-- name: DeactivateOrchestrationScope :execresult
UPDATE orchestration_scope
SET active = 0, updated_at = ?
WHERE scope_key = ? AND active = 1;

-- name: DeactivateOrchestrationScopesForGoal :exec
UPDATE orchestration_scope
SET active = 0, updated_at = ?
WHERE goal_id = ? AND active = 1;

-- name: DeactivateOrchestrationScopesForTask :exec
UPDATE orchestration_scope
SET active = 0, updated_at = ?
WHERE task_id = ? AND active = 1;

-- name: ListOrchestrationScopes :many
SELECT scope_key, project_id, goal_id, task_id, role, agent_session_id, agent_key,
       source_generation, active, created_at, updated_at
FROM orchestration_scope
WHERE project_id = ?
ORDER BY created_at, scope_key;

-- name: ListActiveOrchestrationScopes :many
SELECT scope_key, project_id, goal_id, task_id, role, agent_session_id, agent_key,
       source_generation, active, created_at, updated_at
FROM orchestration_scope
WHERE project_id = ? AND active = 1
ORDER BY created_at, scope_key;
