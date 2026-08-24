CREATE UNIQUE INDEX IF NOT EXISTS idx_task_handoffs_open_task_id
  ON task_handoffs(task_id)
  WHERE completed_report_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_goal_handoffs_open_goal_id
  ON goal_handoffs(goal_id)
  WHERE completed_report_at IS NULL;
