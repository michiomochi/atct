ALTER TABLE task_handoffs ADD COLUMN review_rejection_received_by INTEGER REFERENCES agent_sessions(id);
ALTER TABLE task_handoffs ADD COLUMN review_rejection_received_at TEXT;

ALTER TABLE goal_handoffs ADD COLUMN review_rejection_received_by INTEGER REFERENCES agent_sessions(id);
ALTER TABLE goal_handoffs ADD COLUMN review_rejection_received_at TEXT;

ALTER TABLE plan_handoffs ADD COLUMN review_rejection_received_by INTEGER REFERENCES agent_sessions(id);
ALTER TABLE plan_handoffs ADD COLUMN review_rejection_received_at TEXT;

CREATE TABLE task_create_handoffs (
  id             TEXT PRIMARY KEY,
  plan_handoff_id TEXT NOT NULL UNIQUE REFERENCES plan_handoffs(id),
  goal_id        INTEGER NOT NULL REFERENCES goals(id),
  requested_by   INTEGER REFERENCES agent_sessions(id),
  received_by    INTEGER REFERENCES agent_sessions(id),
  completed_by   INTEGER REFERENCES agent_sessions(id),
  requested_at   TEXT,
  received_at    TEXT,
  completed_at   TEXT,
  request_report TEXT,
  complete_report TEXT
);

CREATE INDEX idx_task_create_handoffs_goal_id
  ON task_create_handoffs(goal_id);

CREATE TABLE task_create_handoff_tasks (
  handoff_id TEXT NOT NULL REFERENCES task_create_handoffs(id),
  task_id    INTEGER NOT NULL REFERENCES tasks(id),
  PRIMARY KEY (handoff_id, task_id)
);
