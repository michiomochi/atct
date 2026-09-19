-- name: BindMonitorToken :exec
INSERT INTO monitor_bindings (token, agent_session_id, created_at)
VALUES (sqlc.arg('token'), sqlc.arg('agent_session_id'), sqlc.arg('created_at'))
ON CONFLICT(token) DO UPDATE SET
  agent_session_id = excluded.agent_session_id,
  created_at = excluded.created_at;

-- name: GetMonitorBindingAgentSessionID :one
SELECT agent_session_id
FROM monitor_bindings
WHERE token = sqlc.arg('token');
