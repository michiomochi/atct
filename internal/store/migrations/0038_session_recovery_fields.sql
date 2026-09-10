ALTER TABLE agent_sessions ADD COLUMN discarded_at TEXT;
ALTER TABLE agent_sessions ADD COLUMN discarded_by INTEGER REFERENCES agent_sessions(id);
ALTER TABLE agent_sessions ADD COLUMN discarded_decision_id INTEGER REFERENCES decisions(id);
ALTER TABLE agent_sessions ADD COLUMN discard_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE task_handoffs ADD COLUMN recovered_at TEXT;
ALTER TABLE task_handoffs ADD COLUMN recovery_report TEXT;
ALTER TABLE goal_handoffs ADD COLUMN recovered_at TEXT;
ALTER TABLE goal_handoffs ADD COLUMN recovery_report TEXT;

DROP INDEX IF EXISTS idx_task_handoffs_open_task_id;
CREATE UNIQUE INDEX idx_task_handoffs_open_task_id
  ON task_handoffs(task_id)
  WHERE completed_report_at IS NULL AND recovered_at IS NULL;

DROP INDEX IF EXISTS idx_goal_handoffs_open_goal_id;
CREATE UNIQUE INDEX idx_goal_handoffs_open_goal_id
  ON goal_handoffs(goal_id)
  WHERE completed_report_at IS NULL AND recovered_at IS NULL;
