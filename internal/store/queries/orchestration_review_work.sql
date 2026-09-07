-- name: InsertOrchestrationReviewWork :exec
INSERT INTO orchestration_review_work (
    review_work_id, project_id, goal_id, task_id, kind, handoff_id,
    requester_session_id, requester_scope_key, expected_reviewer_role,
    reviewer_scope_key, reviewer_session_id, state,
    review_requested_generation, review_received_generation,
    settlement_generation, active, action_role, action_scope_key,
    action_task_id, action_instruction, opened_at, updated_at, resolved_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 'requested', ?, NULL,
        NULL, 1, NULL, NULL, NULL, NULL, ?, ?, NULL);

-- name: ReceiveOrchestrationReviewWork :execresult
UPDATE orchestration_review_work
SET reviewer_session_id = ?,
    state = 'received',
    review_received_generation = ?,
    updated_at = ?
WHERE kind = ? AND handoff_id = ? AND review_requested_generation = ?
  AND active = 1 AND state = 'requested';

-- name: SettleOrchestrationReviewWork :execresult
UPDATE orchestration_review_work
SET state = ?,
    settlement_generation = ?,
    active = 0,
    action_role = ?,
    action_scope_key = ?,
    action_task_id = ?,
    action_instruction = ?,
    updated_at = ?,
    resolved_at = ?
WHERE kind = ? AND handoff_id = ? AND review_requested_generation = ?
  AND active = 1;

-- name: ListOrchestrationReviewWork :many
SELECT review_work_id, project_id, goal_id, task_id, kind, handoff_id,
       requester_session_id, requester_scope_key, expected_reviewer_role,
       reviewer_scope_key, reviewer_session_id, state,
       review_requested_generation, review_received_generation,
       settlement_generation, active, action_role, action_scope_key,
       action_task_id, action_instruction, opened_at, updated_at, resolved_at
FROM orchestration_review_work
WHERE project_id = ?
ORDER BY opened_at, review_work_id;

-- name: ListPendingOrchestrationReviewWork :many
SELECT review_work_id, project_id, goal_id, task_id, kind, handoff_id,
       requester_session_id, requester_scope_key, expected_reviewer_role,
       reviewer_scope_key, reviewer_session_id, state,
       review_requested_generation, review_received_generation,
       settlement_generation, active, action_role, action_scope_key,
       action_task_id, action_instruction, opened_at, updated_at, resolved_at
FROM orchestration_review_work AS work
WHERE work.project_id = ?
  AND (
      work.active = 1
      OR work.action_instruction IS NOT NULL
  )
  AND (
      work.state <> 'rejected'
      OR work.review_requested_generation = (
          SELECT MAX(newer.review_requested_generation)
          FROM orchestration_review_work AS newer
          WHERE newer.kind = work.kind AND newer.handoff_id = work.handoff_id
      )
  )
ORDER BY work.opened_at, work.review_work_id;

-- name: GetOpenGoalHandoffIDForReceiver :one
SELECT id
FROM goal_handoffs
WHERE goal_id = ? AND received_by = ? AND completed_report_at IS NULL
ORDER BY received_at DESC, id DESC
LIMIT 1;

-- name: GetNextTodoTaskID :one
SELECT id
FROM tasks
WHERE goal_id = ? AND status = 'todo'
ORDER BY sort_order, id
LIMIT 1;

-- name: GetOrchestrationReviewWorkReviewerScopeKey :one
SELECT reviewer_scope_key
FROM orchestration_review_work
WHERE kind = ? AND handoff_id = ? AND review_requested_generation = ?;
