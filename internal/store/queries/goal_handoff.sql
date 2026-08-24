-- name: ListOpenGoalHandoffs :many
SELECT id, goal_id, requested_by, received_by,
       requested_at, received_at, completed_report_at,
       request_report, complete_report
FROM goal_handoffs
WHERE completed_report_at IS NULL
ORDER BY id;
