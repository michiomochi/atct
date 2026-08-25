-- name: ListOpenGoalHandoffs :many
SELECT id, goal_id, requested_by, received_by,
       requested_at, received_at, completed_report_at,
       request_report, complete_report
FROM goal_handoffs
WHERE completed_report_at IS NULL
ORDER BY id;

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
