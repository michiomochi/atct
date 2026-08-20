WITH ranked AS (
  SELECT id,
         ROW_NUMBER() OVER (
           PARTITION BY goal_id
           ORDER BY created_at, sort_order, id
         ) - 1 AS new_sort_order
  FROM tasks
)
UPDATE tasks
SET sort_order = (
  SELECT new_sort_order
  FROM ranked
  WHERE ranked.id = tasks.id
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_goal_sort_order
  ON tasks(goal_id, sort_order);
