-- name: ListOpenGoalHandoffs :many
SELECT id, goal_id, requested_by, received_by,
       requested_at, received_at, completed_report_at,
       request_report, complete_report
FROM goal_handoffs
WHERE completed_report_at IS NULL
ORDER BY id;

-- The partial unique index idx_goal_handoffs_open_goal_id guarantees at most
-- one open handoff per goal, so filtering completed_report_at is equivalent
-- to selecting the first open handoff in the legacy implementation.
-- name: ListOpenGoalHandoffClaims :many
SELECT g.id, g.project_id, g.derived_from_goal_id, g.content, g.status,
       g.creator, g.result_summary, g.work_done, g.now_possible,
       g.how_to_verify, g.surprises, g.needs_review, g.next_steps,
       g.created_at, g.updated_at,
       gh.requested_by, gh.received_by
FROM goal_handoffs AS gh
JOIN goals AS g ON g.id = gh.goal_id
WHERE g.project_id = ?
  AND gh.completed_report_at IS NULL
ORDER BY g.created_at, g.id;

-- name: ListGoalSessionKeys :many
WITH sessions AS (
  SELECT s.session_key AS session_key,
         0 AS role_rank,
         CASE WHEN gh.completed_report_at IS NULL THEN 1 ELSE 0 END AS handoff_open
  FROM goal_handoffs gh
  JOIN agent_sessions s ON s.id = gh.received_by
  WHERE gh.goal_id = @goal_id AND s.session_key <> ''
  UNION ALL
  SELECT s.session_key,
         1,
         CASE WHEN th.completed_report_at IS NULL THEN 1 ELSE 0 END
  FROM tasks t
  JOIN task_handoffs th ON th.task_id = t.id
  JOIN agent_sessions s ON s.id = th.received_by
  WHERE t.goal_id = @goal_id AND s.session_key <> ''
)
SELECT session_key,
       CAST(CASE MIN(role_rank) WHEN 0 THEN 'subcommander' ELSE 'executor' END AS TEXT) AS role,
       CAST(MAX(handoff_open) AS INTEGER) AS handoff_open
FROM sessions
GROUP BY session_key
ORDER BY MIN(role_rank), session_key;
