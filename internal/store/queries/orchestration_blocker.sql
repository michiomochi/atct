-- name: GetOrchestrationBlocker :one
SELECT blocker_id, project_id, goal_id, task_id, scope_key, kind, source_id,
       generation, owner_role, instruction, opened_at, resolved_at
FROM orchestration_blockers
WHERE blocker_id = ?;

-- name: GetOrchestrationBlockerByKey :one
SELECT blocker_id, project_id, goal_id, task_id, scope_key, kind, source_id,
       generation, owner_role, instruction, opened_at, resolved_at
FROM orchestration_blockers
WHERE kind = ? AND source_id = ? AND generation = ?;

-- name: ListOrchestrationBlockers :many
SELECT blocker_id, project_id, goal_id, task_id, scope_key, kind, source_id,
       generation, owner_role, instruction, opened_at, resolved_at
FROM orchestration_blockers
WHERE project_id = ?
ORDER BY opened_at, blocker_id;

-- name: ListOpenOrchestrationBlockers :many
SELECT blocker_id, project_id, goal_id, task_id, scope_key, kind, source_id,
       generation, owner_role, instruction, opened_at, resolved_at
FROM orchestration_blockers
WHERE project_id = ? AND resolved_at IS NULL
ORDER BY opened_at, blocker_id;

-- name: InsertOrchestrationBlocker :exec
INSERT INTO orchestration_blockers (
    blocker_id, project_id, goal_id, task_id, scope_key, kind, source_id,
    generation, owner_role, instruction, opened_at, resolved_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL);

-- name: UpdateOrchestrationBlocker :exec
UPDATE orchestration_blockers
SET project_id = ?, goal_id = ?, task_id = ?, scope_key = ?, owner_role = ?,
    instruction = ?
WHERE kind = ? AND source_id = ? AND generation = ? AND resolved_at IS NULL;

-- name: ResolveOrchestrationBlocker :execresult
UPDATE orchestration_blockers
SET resolved_at = ?
WHERE blocker_id = ? AND resolved_at IS NULL;

-- name: ResolveHumanDecisionBlocker :execresult
UPDATE orchestration_blockers
SET resolved_at = ?
WHERE kind = 'human_decision' AND source_id = ? AND resolved_at IS NULL;
