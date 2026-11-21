CREATE TABLE goal_handoffs (
  id                  TEXT PRIMARY KEY,
  goal_id             TEXT NOT NULL REFERENCES goals(id),
  requested_by        TEXT REFERENCES agent_sessions(id),
  received_by         TEXT REFERENCES agent_sessions(id),
  requested_at        TEXT,
  received_at         TEXT,
  completed_report_at TEXT
);

CREATE INDEX idx_goal_handoffs_goal_id
  ON goal_handoffs(goal_id);
