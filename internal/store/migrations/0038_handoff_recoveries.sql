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

CREATE TABLE handoff_recoveries (
  id                  INTEGER PRIMARY KEY,
  handoff_kind        TEXT NOT NULL,
  handoff_id          TEXT NOT NULL,
  goal_id             INTEGER REFERENCES goals(id),
  task_id             INTEGER REFERENCES tasks(id),
  recovered_phase     TEXT NOT NULL,
  stale_session_id    INTEGER NOT NULL REFERENCES agent_sessions(id),
  proof_kind          TEXT NOT NULL,
  discard_decision_id INTEGER REFERENCES decisions(id),
  recovered_by        INTEGER NOT NULL REFERENCES agent_sessions(id),
  replacement_id      TEXT,
  reason              TEXT NOT NULL,
  created_at          TEXT NOT NULL,
  UNIQUE (handoff_kind, handoff_id, recovered_phase, stale_session_id)
);

CREATE INDEX idx_handoff_recoveries_goal_id
  ON handoff_recoveries(goal_id, created_at, id);

CREATE INDEX idx_handoff_recoveries_task_id
  ON handoff_recoveries(task_id, created_at, id);
