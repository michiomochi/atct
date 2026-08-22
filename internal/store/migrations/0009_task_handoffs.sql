CREATE TABLE task_handoffs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  requested_by TEXT REFERENCES agent_sessions(id),
  received_by TEXT REFERENCES agent_sessions(id),
  requested_at TEXT,
  received_at TEXT,
  completed_report_at TEXT
);

CREATE INDEX idx_task_handoffs_task_id ON task_handoffs(task_id);
