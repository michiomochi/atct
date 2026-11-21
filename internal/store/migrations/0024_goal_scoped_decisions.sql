-- Goal-scoped decisions are valid without a task. This supports commander
-- decisions and the durable human goal-review lifecycle.
CREATE TABLE decisions_new (
  id                 INTEGER PRIMARY KEY,
  goal_id            INTEGER NOT NULL REFERENCES goals(id),
  task_id            INTEGER REFERENCES tasks(id),
  kind               TEXT NOT NULL,
  question           TEXT NOT NULL,
  options            TEXT NOT NULL DEFAULT '[]',
  status             TEXT NOT NULL,
  default_option     TEXT NOT NULL DEFAULT '',
  default_after_ms   INTEGER,
  default_applied_at TEXT,
  answer_label       TEXT NOT NULL DEFAULT '',
  answer_text        TEXT NOT NULL DEFAULT '',
  answered_at        TEXT,
  applied_at         TEXT,
  agent_session_id   INTEGER NOT NULL DEFAULT 0,
  created_at         TEXT NOT NULL
);

INSERT INTO decisions_new (
  id, goal_id, task_id, kind, question, options, status, default_option,
  default_after_ms, default_applied_at, answer_label, answer_text, answered_at,
  applied_at, agent_session_id, created_at
)
SELECT id, goal_id, task_id, kind, question, options, status, default_option,
       default_after_ms, default_applied_at, answer_label, answer_text, answered_at,
       applied_at, agent_session_id, created_at
FROM decisions;

DROP TABLE decisions;
ALTER TABLE decisions_new RENAME TO decisions;

CREATE INDEX idx_decisions_open ON decisions(status, goal_id);
