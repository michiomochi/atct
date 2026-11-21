INSERT INTO task_handoffs (
  id, task_id, requested_by, received_by, requested_at, received_at
)
SELECT
  'legacy-task-claim-' || t.id,
  t.id,
  t.claimed_by,
  t.claimed_by,
  COALESCE(t.claimed_at, t.updated_at, t.created_at),
  t.claimed_at
FROM tasks AS t
JOIN agent_sessions AS s ON s.id = t.claimed_by
WHERE trim(t.claimed_by) <> ''
  AND NOT EXISTS (
    SELECT 1
    FROM task_handoffs AS h
    WHERE h.task_id = t.id
      AND h.completed_report_at IS NULL
  );

INSERT INTO goal_handoffs (
  id, goal_id, requested_by, received_by, requested_at, received_at
)
SELECT
  'legacy-goal-claim-' || g.id,
  g.id,
  g.claimed_by,
  g.claimed_by,
  COALESCE(g.claimed_at, g.updated_at, g.created_at),
  g.claimed_at
FROM goals AS g
JOIN agent_sessions AS s ON s.id = g.claimed_by
WHERE trim(g.claimed_by) <> ''
  AND NOT EXISTS (
    SELECT 1
    FROM goal_handoffs AS h
    WHERE h.goal_id = g.id
      AND h.completed_report_at IS NULL
  );

ALTER TABLE tasks DROP COLUMN claimed_by;
ALTER TABLE tasks DROP COLUMN claimed_at;
ALTER TABLE goals DROP COLUMN claimed_by;
ALTER TABLE goals DROP COLUMN claimed_at;
