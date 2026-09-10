-- name: GetAgentSessionRecovery :one
SELECT id, project_id, pid, started_at, discarded_at, discarded_by,
       discarded_decision_id, discard_reason
FROM agent_sessions
WHERE id = ?;

-- name: DiscardAgentSession :execresult
UPDATE agent_sessions
SET discarded_at = ?, discarded_by = ?, discarded_decision_id = ?, discard_reason = ?
WHERE id = ? AND project_id = ? AND discarded_at IS NULL;

-- name: GetHandoffRecovery :one
SELECT id, handoff_kind, handoff_id, goal_id, task_id, recovered_phase,
       stale_session_id, proof_kind, discard_decision_id, recovered_by,
       replacement_id, reason, created_at
FROM handoff_recoveries
WHERE handoff_kind = ?
  AND handoff_id = ?
  AND recovered_phase = ?
  AND stale_session_id = ?;

-- name: CreateHandoffRecovery :one
INSERT INTO handoff_recoveries (
  handoff_kind, handoff_id, goal_id, task_id, recovered_phase,
  stale_session_id, proof_kind, discard_decision_id, recovered_by,
  replacement_id, reason, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(handoff_kind, handoff_id, recovered_phase, stale_session_id)
DO UPDATE SET id = handoff_recoveries.id
RETURNING id;

-- name: ListHandoffRecoveriesForGoal :many
SELECT id, handoff_kind, handoff_id, goal_id, task_id, recovered_phase,
       stale_session_id, proof_kind, discard_decision_id, recovered_by,
       replacement_id, reason, created_at
FROM handoff_recoveries
WHERE goal_id = ?
ORDER BY created_at, id;

-- name: ListHandoffRecoveriesForTask :many
SELECT id, handoff_kind, handoff_id, goal_id, task_id, recovered_phase,
       stale_session_id, proof_kind, discard_decision_id, recovered_by,
       replacement_id, reason, created_at
FROM handoff_recoveries
WHERE task_id = ?
ORDER BY created_at, id;
