-- name: CreateGoal :one
INSERT INTO goals (
  project_id, derived_from_goal_id, content, status, creator,
  result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, '', '', '', '', '', '', '', ?, ?)
RETURNING id;

-- name: GetGoal :one
-- derived_from_goal_id is cast because a dangling reference can hold a value
-- SQLite could not coerce to INTEGER, and the goal detail view has to render
-- rather than fail. NULLIF keeps 0 meaning "no parent".
SELECT
  id, project_id, NULLIF(CAST(derived_from_goal_id AS INTEGER), 0) AS derived_from_goal_id,
  content, spec, plan, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
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

-- name: MarkGoalActive :execresult
UPDATE goals SET status = 'active', updated_at = ?
WHERE id = ? AND status = 'proposed';

-- name: UpdateGoalContent :execresult
UPDATE goals SET content = ?, updated_at = ?
WHERE id = ? AND status = 'proposed';

-- name: MarkGoalDropped :execresult
UPDATE goals SET status = 'dropped', updated_at = ?
WHERE id = ? AND status = 'proposed';

-- name: WithdrawActiveGoal :execresult
UPDATE goals SET status = 'dropped', result_summary = ?, updated_at = ?
WHERE id = ? AND status = 'active';

-- name: HasGoalWork :one
SELECT EXISTS(
  SELECT 1
  FROM goals AS g
  WHERE g.id = ? AND g.creator = 'agent'
    AND (
      EXISTS (SELECT 1 FROM tasks WHERE goal_id = g.id)
      OR EXISTS (SELECT 1 FROM goal_handoffs WHERE goal_id = g.id)
      OR EXISTS (SELECT 1 FROM plan_handoffs WHERE goal_id = g.id)
      OR EXISTS (SELECT 1 FROM task_create_handoffs WHERE goal_id = g.id)
      OR EXISTS (SELECT 1 FROM decisions WHERE goal_id = g.id AND kind <> 'goal_approval')
    )
);

-- name: ListOpenAgentGoalApprovals :many
SELECT d.id, d.goal_id, d.created_at, g.updated_at
FROM decisions AS d
JOIN goals AS g ON g.id = d.goal_id
WHERE g.status = 'proposed' AND g.creator = 'agent'
  AND d.kind = 'goal_approval' AND d.status = 'open'
ORDER BY d.id;

-- name: WithdrawProposedGoal :execresult
UPDATE goals SET status = 'dropped', result_summary = ?, updated_at = ?
WHERE goals.id = ? AND goals.status = 'proposed' AND goals.creator = 'agent'
  AND NOT EXISTS (SELECT 1 FROM tasks WHERE tasks.goal_id = goals.id)
  AND NOT EXISTS (SELECT 1 FROM goal_handoffs WHERE goal_handoffs.goal_id = goals.id)
  AND NOT EXISTS (SELECT 1 FROM plan_handoffs WHERE plan_handoffs.goal_id = goals.id)
  AND NOT EXISTS (SELECT 1 FROM task_create_handoffs WHERE task_create_handoffs.goal_id = goals.id)
  AND EXISTS (
    SELECT 1 FROM decisions
    WHERE decisions.goal_id = goals.id AND kind = 'goal_approval' AND status = 'open'
  )
  AND NOT EXISTS (
    SELECT 1 FROM decisions
    WHERE decisions.goal_id = goals.id AND kind <> 'goal_approval'
  );

-- name: GetGoalApprovalDecisionGoalID :one
SELECT goal_id
FROM decisions
WHERE id = ? AND kind = 'goal_approval' AND status = 'open';

-- name: ApplyGoalApprovalDecision :execresult
UPDATE decisions SET status = 'applied', answer_label = 'approve',
  answered_at = ?, applied_at = ?
WHERE id = ? AND kind = 'goal_approval' AND status = 'open';

-- name: RejectGoalApprovalDecision :execresult
UPDATE decisions SET status = 'answered', answer_label = 'reject',
  answer_text = ?, answered_at = ?
WHERE id = ? AND kind = 'goal_approval' AND status = 'open';

-- name: ListGoals :many
SELECT
  id, project_id, derived_from_goal_id, content, spec, plan, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
FROM goals
WHERE project_id = ?
ORDER BY created_at;

-- name: ListAllGoals :many
SELECT
  id, project_id, derived_from_goal_id, content, spec, plan, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
FROM goals
ORDER BY created_at;

-- name: ListDerivedGoals :many
SELECT
  id, project_id, derived_from_goal_id, content, spec, plan, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
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

-- name: FinalizeGoal :execresult
UPDATE goals
SET status = 'done', result_summary = ?, work_done = ?, now_possible = ?,
    how_to_verify = ?, surprises = ?, needs_review = ?, next_steps = ?, updated_at = ?
WHERE id = ? AND status = 'active';

-- name: UpdateGoalCompletionReport :execresult
UPDATE goals SET
  result_summary = ?,
  work_done = ?, now_possible = ?, how_to_verify = ?,
  surprises = ?, needs_review = ?, next_steps = ?, updated_at = ?
WHERE id = ? AND status = 'active';

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

-- name: FinalizeGoalReview :execresult
UPDATE goals SET status = 'done', updated_at = ?
WHERE id = ? AND status = 'active';

-- name: UpdateGoalRequestReport :execresult
UPDATE goals SET spec = ?, plan = ?, updated_at = ? WHERE id = ?;
