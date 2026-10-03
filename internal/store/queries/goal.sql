-- name: CreateGoal :one
INSERT INTO goals (
  project_id, derived_from_goal_id, content, status, creator,
  result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review,
  created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, '', '', '', '', '', '', ?, ?)
RETURNING id;

-- name: GetGoal :one
-- derived_from_goal_id is cast because a dangling reference can hold a value
-- SQLite could not coerce to INTEGER, and the goal detail view has to render
-- rather than fail. NULLIF keeps 0 meaning "no parent".
SELECT
  id, project_id, NULLIF(CAST(derived_from_goal_id AS INTEGER), 0) AS derived_from_goal_id,
  content, spec, plan, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review,
  created_at, updated_at
FROM goals
WHERE id = ?;

-- name: GetGoalProjectID :one
SELECT project_id
FROM goals
WHERE id = ?;

-- name: HasGoalReview :one
SELECT EXISTS(
  SELECT 1 FROM decisions
  WHERE goal_id = ? AND kind = 'goal_review'
);

-- name: GetLatestGoalReviewID :one
SELECT id
FROM decisions
WHERE goal_id = ? AND kind = 'goal_review'
ORDER BY id DESC
LIMIT 1;

-- name: GetOpenGoalReviewGoalID :one
SELECT goal_id
FROM decisions
WHERE id = ? AND kind = 'goal_review' AND status = 'open';

-- name: GetGoalStatus :one
SELECT status
FROM goals
WHERE id = ?;

-- name: ApproveGoalReviewDecision :execresult
UPDATE decisions
SET status = 'applied', answer_label = 'approve', answered_at = ?, applied_at = ?
WHERE id = ? AND kind = 'goal_review' AND status = 'open';

-- name: RejectGoalReviewDecision :execresult
UPDATE decisions
SET status = 'answered', answer_label = 'reject', answer_text = ?, answered_at = ?
WHERE id = ? AND kind = 'goal_review' AND status = 'open';

-- name: WithdrawActiveGoal :execresult
UPDATE goals SET status = 'dropped', result_summary = ?, updated_at = ?
WHERE id = ? AND status = 'active';

-- name: ListGoals :many
SELECT
  id, project_id, derived_from_goal_id, content, spec, plan, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review,
  created_at, updated_at
FROM goals
WHERE project_id = ?
  AND project_id NOT IN (SELECT id FROM projects WHERE archived_at IS NOT NULL)
ORDER BY created_at;

-- name: ListAllGoals :many
SELECT
  id, project_id, derived_from_goal_id, content, spec, plan, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review,
  created_at, updated_at
FROM goals
WHERE project_id NOT IN (SELECT id FROM projects WHERE archived_at IS NOT NULL)
ORDER BY created_at;

-- name: ListDerivedGoals :many
SELECT
  id, project_id, derived_from_goal_id, content, spec, plan, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review,
  created_at, updated_at
FROM goals
WHERE derived_from_goal_id = ?
ORDER BY created_at;

-- name: SetGoalDerivedFrom :execresult
UPDATE goals SET derived_from_goal_id = ?, updated_at = ?
WHERE id = ?;

-- name: CountOpenDecisionsForGoal :one
SELECT COUNT(*)
FROM decisions
WHERE goal_id = ? AND status = 'open';

-- name: CountApprovedGoalReviewsForGoal :one
SELECT COUNT(*)
FROM decisions
WHERE goal_id = ? AND kind = 'goal_review' AND status = 'applied' AND answer_label = 'approve';

-- name: DeleteNextGoals :exec
DELETE FROM next_goals
WHERE goal_id = ?;

-- name: InsertNextGoal :exec
INSERT INTO next_goals (goal_id, next_goal_id, created_at)
VALUES (?, ?, ?);

-- name: ListNextGoalIDs :many
SELECT next_goal_id
FROM next_goals
WHERE goal_id = ?
ORDER BY next_goal_id;

-- name: ListNextGoals :many
SELECT g.id, g.content, g.status
FROM next_goals AS ng
JOIN goals AS g ON g.id = ng.next_goal_id
WHERE ng.goal_id = ?
ORDER BY ng.next_goal_id;

-- name: UpdateGoalCompletionReport :execresult
UPDATE goals SET
  result_summary = ?,
  work_done = ?, now_possible = ?, how_to_verify = ?,
  surprises = ?, needs_review = ?, updated_at = ?
WHERE id = ? AND status = 'active';

-- name: FinalizeGoalReview :execresult
UPDATE goals SET status = 'done', updated_at = ?
WHERE id = ? AND status = 'active';

-- name: UpdateGoalRequestReport :execresult
UPDATE goals SET spec = ?, plan = ?, updated_at = ? WHERE id = ?;
