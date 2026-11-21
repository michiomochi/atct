-- name: GetAgentSessionRecovery :one
SELECT id, project_id, pid, started_at, discarded_at, discarded_by,
       discarded_decision_id, discard_reason
FROM agent_sessions
WHERE id = ?;

-- name: DiscardAgentSession :execresult
UPDATE agent_sessions
SET discarded_at = ?, discarded_by = ?, discarded_decision_id = ?, discard_reason = ?
WHERE id = ? AND project_id = ? AND discarded_at IS NULL;
