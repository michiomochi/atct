-- name: CreatePlanHandoffEntry :one
INSERT INTO plan_handoff_entries (
  handoff_id, kind, body, author_session_id, created_at
)
VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: ListGoalReviewExchangeEntries :many
SELECT e.id, e.handoff_id, e.kind, e.body, e.author_session_id, e.created_at
FROM goal_handoff_entries AS e
JOIN goal_handoffs AS h ON h.id = e.handoff_id
WHERE h.goal_id = ? AND e.kind IN ('review_requested', 'review_rejected')
ORDER BY e.created_at, e.id;

-- name: ListPlanReviewExchangeEntries :many
SELECT e.id, e.handoff_id, e.kind, e.body, e.author_session_id, e.created_at
FROM plan_handoff_entries AS e
JOIN plan_handoffs AS h ON h.id = e.handoff_id
WHERE h.goal_id = ?
ORDER BY e.created_at, e.id;

-- name: ListTaskReviewExchangeEntries :many
SELECT h.task_id, e.id, e.handoff_id, e.kind, e.body, e.author_session_id, e.created_at
FROM task_handoff_entries AS e
JOIN task_handoffs AS h ON h.id = e.handoff_id
JOIN tasks AS t ON t.id = h.task_id
WHERE t.goal_id = sqlc.arg('goal_id')
  AND (sqlc.arg('task_id') = 0 OR h.task_id = sqlc.arg('task_id'))
  AND e.kind IN ('review_requested', 'review_rejected')
ORDER BY e.created_at, e.id;

-- name: ListGoalReviewDecisions :many
SELECT id, status, answer_label, answer_text, answered_at, created_at, agent_session_id
FROM decisions
WHERE goal_id = ? AND kind = 'goal_review'
  AND (answer_label = 'reject' OR status = 'withdrawn')
ORDER BY id;

-- name: ListGoalReviewGaps :many
SELECT g.handoff_id,
       (SELECT MIN(e.created_at) FROM goal_handoff_entries AS e
         WHERE e.handoff_id = g.handoff_id
           AND e.kind IN ('review_requested', 'review_received', 'review_rejected')) AS before_at
FROM handoff_history_gaps AS g
JOIN goal_handoffs AS h ON h.id = g.handoff_id
WHERE g.scope = 'goal' AND h.goal_id = ?
ORDER BY g.handoff_id;

-- name: ListPlanReviewGaps :many
SELECT g.handoff_id,
       (SELECT MIN(e.created_at) FROM plan_handoff_entries AS e
         WHERE e.handoff_id = g.handoff_id) AS before_at
FROM handoff_history_gaps AS g
JOIN plan_handoffs AS h ON h.id = g.handoff_id
WHERE g.scope = 'plan' AND h.goal_id = ?
ORDER BY g.handoff_id;

-- name: ListTaskReviewGaps :many
SELECT g.handoff_id, h.task_id,
       (SELECT MIN(e.created_at) FROM task_handoff_entries AS e
         WHERE e.handoff_id = g.handoff_id
           AND e.kind IN ('review_requested', 'review_received', 'review_rejected')) AS before_at
FROM handoff_history_gaps AS g
JOIN task_handoffs AS h ON h.id = g.handoff_id
JOIN tasks AS t ON t.id = h.task_id
WHERE g.scope = 'task' AND t.goal_id = sqlc.arg('goal_id')
  AND (sqlc.arg('task_id') = 0 OR h.task_id = sqlc.arg('task_id'))
ORDER BY g.handoff_id;
