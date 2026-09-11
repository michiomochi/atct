CREATE TABLE IF NOT EXISTS projects (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  root_path  TEXT NOT NULL,
  created_at TEXT NOT NULL,
  claimed_by INTEGER NOT NULL DEFAULT 0,
  claimed_at TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_name
  ON projects(name);

CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_root_path
  ON projects(root_path);

CREATE TABLE IF NOT EXISTS agent_sessions (
  id            INTEGER PRIMARY KEY,
  project_id    INTEGER REFERENCES projects(id),
  registered_at TEXT NOT NULL,
  pid           INTEGER NOT NULL DEFAULT 0,
  started_at    TEXT NOT NULL DEFAULT '',
  session_key   TEXT NOT NULL DEFAULT '',
  discarded_at  TEXT,
  discarded_by  INTEGER REFERENCES agent_sessions(id),
  discarded_decision_id INTEGER REFERENCES decisions(id),
  discard_reason TEXT NOT NULL DEFAULT '',
  development_mode INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_project_registered_at
  ON agent_sessions(project_id, registered_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_sessions_session_key
  ON agent_sessions(session_key) WHERE session_key <> '';

CREATE TABLE IF NOT EXISTS goals (
  id             INTEGER PRIMARY KEY,
  project_id     INTEGER NOT NULL REFERENCES projects(id),
  derived_from_goal_id INTEGER REFERENCES goals(id),
  content        TEXT NOT NULL,
  spec           TEXT NOT NULL DEFAULT '',
  plan           TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL,
  creator        TEXT NOT NULL DEFAULT 'human',
  result_summary TEXT NOT NULL DEFAULT '',
  work_done      TEXT NOT NULL DEFAULT '',
  now_possible   TEXT NOT NULL DEFAULT '',
  how_to_verify  TEXT NOT NULL DEFAULT '',
  surprises      TEXT NOT NULL DEFAULT '',
  needs_review   TEXT NOT NULL DEFAULT '',
  next_steps     TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  CHECK (
  status <> 'done' OR (
    length(trim(work_done)) > 0 AND length(work_done) <= 2000 AND
    length(trim(now_possible)) > 0 AND length(now_possible) <= 2000 AND
    length(trim(how_to_verify)) > 0 AND length(how_to_verify) <= 2000 AND
    length(trim(surprises)) > 0 AND length(surprises) <= 2000 AND
    length(trim(needs_review)) > 0 AND length(needs_review) <= 2000 AND
    length(trim(next_steps)) > 0 AND length(next_steps) <= 2000
  )
)
);

CREATE TABLE IF NOT EXISTS tasks (
  id         INTEGER PRIMARY KEY,
  goal_id    INTEGER NOT NULL REFERENCES goals(id),
  title      TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL,
  agent      TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  declare_key TEXT NOT NULL,
  snoozed_until TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_declare_key
  ON tasks(goal_id, declare_key);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_goal_sort_order
  ON tasks(goal_id, sort_order);

CREATE TABLE IF NOT EXISTS decisions (
  id           INTEGER PRIMARY KEY,
  goal_id      INTEGER NOT NULL REFERENCES goals(id),
  task_id      INTEGER REFERENCES tasks(id),
  kind         TEXT NOT NULL,
  question     TEXT NOT NULL,
  options      TEXT NOT NULL DEFAULT '[]',
  status       TEXT NOT NULL,
  default_option TEXT NOT NULL DEFAULT '',
  default_after_ms INTEGER,
  default_applied_at TEXT,
  answer_label TEXT NOT NULL DEFAULT '',
  answer_text  TEXT NOT NULL DEFAULT '',
  answered_at  TEXT,
  applied_at   TEXT,
  agent_session_id INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_decisions_open
  ON decisions(status, goal_id);

CREATE TABLE IF NOT EXISTS task_commits (
  task_id      INTEGER NOT NULL REFERENCES tasks(id),
  sha          TEXT NOT NULL,
  subject      TEXT NOT NULL,
  files_changed INTEGER NOT NULL DEFAULT 0,
  insertions   INTEGER NOT NULL DEFAULT 0,
  deletions    INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (task_id, sha)
);

CREATE TABLE IF NOT EXISTS task_handoffs (
  id                  TEXT PRIMARY KEY,
  task_id             INTEGER NOT NULL REFERENCES tasks(id),
  requested_by        INTEGER REFERENCES agent_sessions(id),
  received_by         INTEGER REFERENCES agent_sessions(id),
  requested_at        TEXT,
  received_at         TEXT,
  completed_report_at TEXT,
  request_report      TEXT,
  complete_report     TEXT,
  review_requested_by INTEGER REFERENCES agent_sessions(id),
  review_requested_at TEXT,
  review_request_report TEXT,
  review_received_by  INTEGER REFERENCES agent_sessions(id),
  review_received_at  TEXT,
  review_rejected_at  TEXT,
  review_reject_report TEXT,
  review_rejection_received_by INTEGER REFERENCES agent_sessions(id),
  review_rejection_received_at TEXT,
  recovered_at        TEXT,
  recovery_report     TEXT
);

CREATE INDEX IF NOT EXISTS idx_task_handoffs_task_id
  ON task_handoffs(task_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_task_handoffs_open_task_id
  ON task_handoffs(task_id)
  WHERE completed_report_at IS NULL AND recovered_at IS NULL;

CREATE TABLE IF NOT EXISTS goal_handoffs (
  id                  TEXT PRIMARY KEY,
  goal_id             INTEGER NOT NULL REFERENCES goals(id),
  requested_by        INTEGER REFERENCES agent_sessions(id),
  received_by         INTEGER REFERENCES agent_sessions(id),
  requested_at        TEXT,
  received_at         TEXT,
  completed_report_at TEXT,
  request_report      TEXT,
  complete_report     TEXT,
  review_requested_by INTEGER REFERENCES agent_sessions(id),
  review_requested_at TEXT,
  review_request_report TEXT,
  review_received_by  INTEGER REFERENCES agent_sessions(id),
  review_received_at  TEXT,
  review_rejected_at  TEXT,
  review_reject_report TEXT,
  review_rejection_received_by INTEGER REFERENCES agent_sessions(id),
  review_rejection_received_at TEXT,
  recovered_at        TEXT,
  recovery_report     TEXT
);

CREATE INDEX IF NOT EXISTS idx_goal_handoffs_goal_id
  ON goal_handoffs(goal_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_goal_handoffs_open_goal_id
  ON goal_handoffs(goal_id)
  WHERE completed_report_at IS NULL AND recovered_at IS NULL;

CREATE TABLE IF NOT EXISTS plan_handoffs (
  id                  TEXT PRIMARY KEY,
  goal_id             INTEGER NOT NULL REFERENCES goals(id),
  review_requested_by INTEGER REFERENCES agent_sessions(id),
  review_requested_at TEXT,
  review_request_report TEXT,
  review_received_by  INTEGER REFERENCES agent_sessions(id),
  review_received_at  TEXT,
  review_rejected_at  TEXT,
  review_reject_report TEXT,
  review_rejection_received_by INTEGER REFERENCES agent_sessions(id),
  review_rejection_received_at TEXT,
  completed_report_at TEXT,
  complete_report     TEXT
);

CREATE INDEX IF NOT EXISTS idx_plan_handoffs_goal_id
  ON plan_handoffs(goal_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_handoffs_open_goal_id
  ON plan_handoffs(goal_id)
  WHERE completed_report_at IS NULL;

CREATE TABLE IF NOT EXISTS monitor_health (
  monitor_id         TEXT PRIMARY KEY,
  agent_key          TEXT NOT NULL DEFAULT '',
  scope_key          TEXT NOT NULL DEFAULT '',
  agent_session_id   INTEGER NOT NULL DEFAULT 0,
  cwd                TEXT NOT NULL,
  role               TEXT NOT NULL,
  project_id         INTEGER NOT NULL,
  goal_id            INTEGER,
  task_id            INTEGER,
  pid                INTEGER NOT NULL,
  process_started_at TEXT NOT NULL,
  state              TEXT NOT NULL,
  reason             TEXT NOT NULL DEFAULT '',
  transitioned_at    TEXT NOT NULL,
  last_seen_at       TEXT NOT NULL,
  stopped_at         TEXT
);

CREATE INDEX IF NOT EXISTS monitor_health_project_idx
  ON monitor_health(project_id);

CREATE INDEX IF NOT EXISTS monitor_health_last_seen_idx
  ON monitor_health(last_seen_at);

CREATE TABLE IF NOT EXISTS task_create_handoffs (
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

CREATE INDEX IF NOT EXISTS idx_task_create_handoffs_goal_id
  ON task_create_handoffs(goal_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_task_create_handoffs_open_goal_id
  ON task_create_handoffs(goal_id)
  WHERE completed_at IS NULL AND recovered_at IS NULL;
