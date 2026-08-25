-- name: CreateDecision :exec
INSERT INTO decisions (
  id, goal_id, task_id, kind, question, options, status,
  default_option, default_after_ms, agent_session_id, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetDecision :one
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE id = ?;

-- name: ListOpenDecisions :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE goal_id = ? AND status = 'open'
ORDER BY created_at;

-- name: ListAllOpenDecisions :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE status = 'open'
ORDER BY created_at;

-- name: AnswerDecision :execresult
UPDATE decisions
SET status = 'answered', answer_label = ?, answer_text = ?, answered_at = ?
WHERE id = ? AND status = 'open';

-- name: ListExpiredDecisions :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE status = 'open' AND default_after_ms IS NOT NULL AND default_option != ''
ORDER BY created_at;

-- name: ApplyDecisionDefault :execresult
UPDATE decisions
SET status = 'answered', answer_label = ?, answered_at = ?, default_applied_at = ?
WHERE id = ? AND status = 'open';

-- name: WithdrawDecision :execresult
UPDATE decisions
SET status = 'withdrawn', answer_text = ?
WHERE id = ? AND status = 'open';

-- name: ListAnsweredDecisionsForAgentSession :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE status = 'answered' AND agent_session_id = ?
ORDER BY answered_at;

-- name: ListAnsweredDecisionForID :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE status = 'answered' AND id = ?
ORDER BY answered_at;

-- name: MarkDecisionApplied :exec
UPDATE decisions
SET status = 'applied', applied_at = ?
WHERE id = ? AND status = 'answered';

-- name: ListUnappliedDecisions :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE status = 'answered'
ORDER BY answered_at;

-- name: ListUnappliedDecisionsForProject :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE status = 'answered'
  AND goal_id IN (SELECT id FROM goals WHERE project_id = ?)
ORDER BY answered_at;

-- name: ListUnappliedDecisionsForGoal :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE status = 'answered'
  AND goal_id = ?
ORDER BY answered_at;

-- name: CountAppliedDecisions :one
SELECT COUNT(*)
FROM decisions
WHERE goal_id = ? AND status = 'applied';

-- name: CountAppliedDecisionsForTask :one
SELECT COUNT(*)
FROM decisions
WHERE goal_id = ? AND task_id = ? AND status = 'applied';

-- name: ListAppliedDecisions :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE goal_id = ? AND status = 'applied'
ORDER BY answered_at DESC, applied_at DESC, id DESC
LIMIT sqlc.arg(history_limit);

-- name: ListAppliedDecisionsForTask :many
SELECT
  id, goal_id, COALESCE(task_id, '') AS task_id, kind, question, options, status,
  default_option, default_after_ms, default_applied_at,
  answer_label, answer_text, answered_at, applied_at, agent_session_id, created_at
FROM decisions
WHERE goal_id = ? AND task_id = ? AND status = 'applied'
ORDER BY answered_at DESC, applied_at DESC, id DESC
LIMIT sqlc.arg(history_limit);
