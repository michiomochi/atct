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
  session_key   TEXT NOT NULL DEFAULT ''
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
  review_rejection_received_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_task_handoffs_task_id
  ON task_handoffs(task_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_task_handoffs_open_task_id
  ON task_handoffs(task_id)
  WHERE completed_report_at IS NULL;

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
  review_rejection_received_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_goal_handoffs_goal_id
  ON goal_handoffs(goal_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_goal_handoffs_open_goal_id
  ON goal_handoffs(goal_id)
  WHERE completed_report_at IS NULL;

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
  plan_handoff_id TEXT NOT NULL UNIQUE REFERENCES plan_handoffs(id),
  goal_id         INTEGER NOT NULL REFERENCES goals(id),
  requested_by    INTEGER REFERENCES agent_sessions(id),
  received_by     INTEGER REFERENCES agent_sessions(id),
  completed_by    INTEGER REFERENCES agent_sessions(id),
  requested_at    TEXT,
  received_at     TEXT,
  completed_at    TEXT,
  request_report  TEXT,
  complete_report TEXT
);

CREATE INDEX IF NOT EXISTS idx_task_create_handoffs_goal_id
  ON task_create_handoffs(goal_id);

CREATE TABLE IF NOT EXISTS task_create_handoff_tasks (
  handoff_id TEXT NOT NULL REFERENCES task_create_handoffs(id),
  task_id    INTEGER NOT NULL REFERENCES tasks(id),
  PRIMARY KEY (handoff_id, task_id)
);

CREATE TABLE IF NOT EXISTS orchestration_scope (
  scope_key         TEXT PRIMARY KEY,
  project_id        INTEGER NOT NULL REFERENCES projects(id),
  goal_id           INTEGER REFERENCES goals(id),
  task_id           INTEGER REFERENCES tasks(id),
  role              TEXT NOT NULL,
  agent_session_id  INTEGER NOT NULL DEFAULT 0,
  agent_key         TEXT NOT NULL DEFAULT '',
  source_generation TEXT NOT NULL,
  active            INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS orchestration_scope_project_active_idx ON orchestration_scope(project_id, active, scope_key);
CREATE INDEX IF NOT EXISTS orchestration_scope_goal_active_idx ON orchestration_scope(goal_id, active, scope_key);
CREATE INDEX IF NOT EXISTS orchestration_scope_task_active_idx ON orchestration_scope(task_id, active, scope_key);

CREATE TABLE IF NOT EXISTS orchestration_delivery_leases (
  scope_key TEXT NOT NULL REFERENCES orchestration_scope(scope_key), target_role TEXT NOT NULL,
  holder_monitor_id TEXT NOT NULL, fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
  expires_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY (scope_key, target_role)
);
CREATE INDEX IF NOT EXISTS orchestration_delivery_leases_holder_idx ON orchestration_delivery_leases(holder_monitor_id);

CREATE TABLE IF NOT EXISTS orchestration_delivery_receipts (
  scope_key TEXT NOT NULL REFERENCES orchestration_scope(scope_key), delivery_key TEXT NOT NULL,
  generation TEXT NOT NULL, target_role TEXT NOT NULL, holder_monitor_id TEXT NOT NULL,
  fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
  status TEXT NOT NULL CHECK (status IN ('reserved', 'accepted', 'unknown')),
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY (scope_key, delivery_key, generation)
);
CREATE INDEX IF NOT EXISTS orchestration_delivery_receipts_status_idx ON orchestration_delivery_receipts(status, updated_at);

CREATE TABLE IF NOT EXISTS orchestration_blockers (
  blocker_id TEXT PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects(id), goal_id INTEGER REFERENCES goals(id),
  task_id INTEGER REFERENCES tasks(id), scope_key TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('human_decision', 'dependency_merge')), source_id TEXT NOT NULL,
  generation TEXT NOT NULL, owner_role TEXT NOT NULL CHECK (owner_role IN ('commander', 'subcommander')),
  instruction TEXT NOT NULL, opened_at TEXT NOT NULL, resolved_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS orchestration_blockers_identity_idx ON orchestration_blockers(kind, source_id, generation);
CREATE INDEX IF NOT EXISTS orchestration_blockers_open_project_idx ON orchestration_blockers(project_id, resolved_at, opened_at, blocker_id);

CREATE TABLE IF NOT EXISTS orchestration_review_work (
  review_work_id TEXT PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects(id), goal_id INTEGER NOT NULL REFERENCES goals(id),
  task_id INTEGER REFERENCES tasks(id), kind TEXT NOT NULL CHECK (kind IN ('task', 'plan', 'goal')), handoff_id TEXT NOT NULL,
  requester_session_id INTEGER NOT NULL, requester_scope_key TEXT NOT NULL, expected_reviewer_role TEXT NOT NULL,
  reviewer_scope_key TEXT NOT NULL, reviewer_session_id INTEGER,
  state TEXT NOT NULL CHECK (state IN ('requested', 'received', 'rejected', 'completed')),
  review_requested_generation TEXT NOT NULL, review_received_generation TEXT, settlement_generation TEXT,
  active INTEGER NOT NULL CHECK (active IN (0, 1)), action_role TEXT, action_scope_key TEXT,
  action_task_id INTEGER REFERENCES tasks(id), action_instruction TEXT, opened_at TEXT NOT NULL, updated_at TEXT NOT NULL, resolved_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS orchestration_review_work_identity_idx ON orchestration_review_work(kind, handoff_id, review_requested_generation);
CREATE INDEX IF NOT EXISTS orchestration_review_work_project_idx ON orchestration_review_work(project_id, active, updated_at, review_work_id);
