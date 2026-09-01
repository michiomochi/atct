ALTER TABLE task_handoffs ADD COLUMN review_requested_by INTEGER REFERENCES agent_sessions(id);
ALTER TABLE task_handoffs ADD COLUMN review_requested_at TEXT;
ALTER TABLE task_handoffs ADD COLUMN review_request_report TEXT;
ALTER TABLE task_handoffs ADD COLUMN review_received_by INTEGER REFERENCES agent_sessions(id);
ALTER TABLE task_handoffs ADD COLUMN review_received_at TEXT;
ALTER TABLE task_handoffs ADD COLUMN review_rejected_at TEXT;
ALTER TABLE task_handoffs ADD COLUMN review_reject_report TEXT;

ALTER TABLE goal_handoffs ADD COLUMN review_requested_by INTEGER REFERENCES agent_sessions(id);
ALTER TABLE goal_handoffs ADD COLUMN review_requested_at TEXT;
ALTER TABLE goal_handoffs ADD COLUMN review_request_report TEXT;
ALTER TABLE goal_handoffs ADD COLUMN review_received_by INTEGER REFERENCES agent_sessions(id);
ALTER TABLE goal_handoffs ADD COLUMN review_received_at TEXT;
ALTER TABLE goal_handoffs ADD COLUMN review_rejected_at TEXT;
ALTER TABLE goal_handoffs ADD COLUMN review_reject_report TEXT;

CREATE TABLE plan_handoffs (
  id                  TEXT PRIMARY KEY,
  goal_id             INTEGER NOT NULL REFERENCES goals(id),
  review_requested_by INTEGER REFERENCES agent_sessions(id),
  review_requested_at TEXT,
  review_request_report TEXT,
  review_received_by  INTEGER REFERENCES agent_sessions(id),
  review_received_at  TEXT,
  review_rejected_at  TEXT,
  review_reject_report TEXT,
  completed_report_at TEXT,
  complete_report     TEXT
);

CREATE INDEX idx_plan_handoffs_goal_id ON plan_handoffs(goal_id);
CREATE UNIQUE INDEX idx_plan_handoffs_open_goal_id
ON plan_handoffs(goal_id)
WHERE completed_report_at IS NULL;
