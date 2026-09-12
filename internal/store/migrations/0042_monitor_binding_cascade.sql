CREATE TABLE monitor_bindings_new (
  token TEXT PRIMARY KEY,
  agent_session_id INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL
);

INSERT INTO monitor_bindings_new (token, agent_session_id, created_at)
SELECT token, agent_session_id, created_at
FROM monitor_bindings;

DROP TABLE monitor_bindings;

ALTER TABLE monitor_bindings_new RENAME TO monitor_bindings;

CREATE INDEX idx_monitor_bindings_agent_session_id ON monitor_bindings(agent_session_id);

CREATE INDEX idx_projects_claimed_by
  ON projects(claimed_by) WHERE claimed_by <> 0;

CREATE INDEX idx_goal_handoffs_open_receiver
  ON goal_handoffs(received_by, goal_id)
  WHERE received_at IS NOT NULL AND completed_report_at IS NULL AND recovered_at IS NULL;

CREATE INDEX idx_task_handoffs_open_receiver
  ON task_handoffs(received_by, task_id)
  WHERE received_at IS NOT NULL AND completed_report_at IS NULL AND recovered_at IS NULL;
