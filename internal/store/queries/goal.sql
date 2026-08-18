-- name: CreateGoal :exec
INSERT INTO goals (
  id, project_id, title, description, status,
  result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, '', '', '', '', '', '', '', ?, ?);

-- name: GetGoal :one
SELECT
  id, project_id, title, description, status, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
FROM goals
WHERE id = ?;

-- name: ListGoals :many
SELECT
  id, project_id, title, description, status, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
FROM goals
WHERE project_id = ?
ORDER BY created_at;

-- name: ListAllGoals :many
SELECT
  id, project_id, title, description, status, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
FROM goals
ORDER BY created_at;

-- name: CountOpenDecisionsForGoal :one
SELECT COUNT(*)
FROM decisions
WHERE goal_id = ? AND status = 'open';

-- name: UpdateGoalCompletionReport :execresult
UPDATE goals SET
  result_summary = ?,
  work_done = ?, now_possible = ?, how_to_verify = ?,
  surprises = ?, needs_review = ?, next_steps = ?, updated_at = ?
WHERE id = ?;

-- name: GetCompletionDecisionGoalID :one
SELECT goal_id
FROM decisions
WHERE id = ? AND kind = 'completion' AND status = 'open';

-- name: ApplyCompletionDecision :execresult
UPDATE decisions SET status = 'applied', answer_label = 'approve',
  answered_at = ?, applied_at = ?
WHERE id = ?;

-- name: MarkGoalDone :execresult
UPDATE goals SET status = 'done', updated_at = ?
WHERE id = ?;
