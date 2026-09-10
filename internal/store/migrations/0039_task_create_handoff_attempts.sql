CREATE TABLE task_create_handoffs_new (
  id              TEXT PRIMARY KEY,
  goal_id         INTEGER NOT NULL REFERENCES goals(id),
  requested_by    INTEGER REFERENCES agent_sessions(id),
  received_by     INTEGER REFERENCES agent_sessions(id),
  completed_by    INTEGER REFERENCES agent_sessions(id),
  requested_at    TEXT,
  received_at     TEXT,
  completed_at    TEXT,
  request_report  TEXT,
  complete_report TEXT,
  recovered_at    TEXT,
  recovery_report TEXT
);

INSERT INTO task_create_handoffs_new (
  id, goal_id, requested_by, received_by, completed_by,
  requested_at, received_at, completed_at, request_report, complete_report
)
SELECT id, goal_id, requested_by, received_by, completed_by,
  requested_at, received_at, completed_at, request_report, complete_report
FROM task_create_handoffs;

DROP TABLE task_create_handoffs;
ALTER TABLE task_create_handoffs_new RENAME TO task_create_handoffs;

CREATE INDEX idx_task_create_handoffs_goal_id
  ON task_create_handoffs(goal_id);

CREATE UNIQUE INDEX idx_task_create_handoffs_open_goal_id
  ON task_create_handoffs(goal_id)
  WHERE completed_at IS NULL AND recovered_at IS NULL;
