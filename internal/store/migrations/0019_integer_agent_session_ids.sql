-- Replace the transport-generated session UUIDs with daemon-assigned row IDs.
-- Keep the old values only in this temporary map while all references are
-- rebuilt; agent_sessions intentionally has no legacy_id column afterwards.
CREATE TEMP TABLE agent_session_id_map (
  legacy_id TEXT PRIMARY KEY,
  id        INTEGER NOT NULL UNIQUE
);

INSERT INTO agent_session_id_map (legacy_id, id)
SELECT id, ROW_NUMBER() OVER (ORDER BY registered_at, rowid)
FROM agent_sessions;

CREATE TABLE projects_new (
  id         INTEGER PRIMARY KEY,
  legacy_id  TEXT,
  name       TEXT NOT NULL UNIQUE,
  root_path  TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  claimed_by INTEGER NOT NULL DEFAULT 0,
  claimed_at TEXT
);

INSERT INTO projects_new (id, legacy_id, name, root_path, created_at, claimed_by, claimed_at)
SELECT p.id, p.legacy_id, p.name, p.root_path, p.created_at,
       COALESCE((SELECT m.id FROM agent_session_id_map AS m WHERE m.legacy_id = p.claimed_by), 0),
       p.claimed_at
FROM projects AS p;

CREATE TABLE agent_sessions_new (
  id            INTEGER PRIMARY KEY,
  project_id    INTEGER REFERENCES projects(id),
  registered_at TEXT NOT NULL,
  pid           INTEGER NOT NULL DEFAULT 0,
  started_at    TEXT NOT NULL DEFAULT '',
  session_key   TEXT NOT NULL DEFAULT ''
);

INSERT INTO agent_sessions_new (id, project_id, registered_at, pid, started_at, session_key)
SELECT m.id, s.project_id, s.registered_at, s.pid, s.started_at, s.session_key
FROM agent_sessions AS s
JOIN agent_session_id_map AS m ON m.legacy_id = s.id
ORDER BY m.id;

CREATE TABLE decisions_new (
  id                       INTEGER PRIMARY KEY,
  legacy_id                TEXT,
  goal_id                  INTEGER NOT NULL REFERENCES goals(id),
  task_id                  INTEGER REFERENCES tasks(id),
  kind                     TEXT NOT NULL,
  question                 TEXT NOT NULL,
  options                  TEXT NOT NULL DEFAULT '[]',
  status                   TEXT NOT NULL,
  default_option           TEXT NOT NULL DEFAULT '',
  default_after_ms         INTEGER,
  default_applied_at       TEXT,
  answer_label             TEXT NOT NULL DEFAULT '',
  answer_text              TEXT NOT NULL DEFAULT '',
  answered_at              TEXT,
  applied_at               TEXT,
  agent_session_id         INTEGER NOT NULL DEFAULT 0,
  created_at               TEXT NOT NULL,
  CHECK (kind <> 'decision' OR status NOT IN ('open', 'answered') OR (task_id IS NOT NULL AND task_id <> ''))
);

INSERT INTO decisions_new (
  id, legacy_id, goal_id, task_id, kind, question, options, status, default_option,
  default_after_ms, default_applied_at, answer_label, answer_text, answered_at,
  applied_at, agent_session_id, created_at
)
SELECT d.id, d.legacy_id, d.goal_id, d.task_id, d.kind, d.question, d.options, d.status,
       d.default_option, d.default_after_ms, d.default_applied_at, d.answer_label,
       d.answer_text, d.answered_at, d.applied_at,
       COALESCE((SELECT m.id FROM agent_session_id_map AS m WHERE m.legacy_id = d.agent_session_id), 0),
       d.created_at
FROM decisions AS d;

CREATE TABLE task_handoffs_new (
  id                  TEXT PRIMARY KEY,
  task_id             INTEGER NOT NULL REFERENCES tasks(id),
  requested_by        INTEGER REFERENCES agent_sessions(id),
  received_by         INTEGER REFERENCES agent_sessions(id),
  requested_at        TEXT,
  received_at         TEXT,
  completed_report_at TEXT,
  request_report      TEXT,
  complete_report     TEXT
);

INSERT INTO task_handoffs_new (
  id, task_id, requested_by, received_by, requested_at, received_at,
  completed_report_at, request_report, complete_report
)
SELECT h.id, h.task_id,
       (SELECT m.id FROM agent_session_id_map AS m WHERE m.legacy_id = h.requested_by),
       (SELECT m.id FROM agent_session_id_map AS m WHERE m.legacy_id = h.received_by),
       h.requested_at, h.received_at, h.completed_report_at, h.request_report,
       h.complete_report
FROM task_handoffs AS h;

CREATE TABLE goal_handoffs_new (
  id                  TEXT PRIMARY KEY,
  goal_id             INTEGER NOT NULL REFERENCES goals(id),
  requested_by        INTEGER REFERENCES agent_sessions(id),
  received_by         INTEGER REFERENCES agent_sessions(id),
  requested_at        TEXT,
  received_at         TEXT,
  completed_report_at TEXT,
  request_report      TEXT,
  complete_report     TEXT
);

INSERT INTO goal_handoffs_new (
  id, goal_id, requested_by, received_by, requested_at, received_at,
  completed_report_at, request_report, complete_report
)
SELECT h.id, h.goal_id,
       (SELECT m.id FROM agent_session_id_map AS m WHERE m.legacy_id = h.requested_by),
       (SELECT m.id FROM agent_session_id_map AS m WHERE m.legacy_id = h.received_by),
       h.requested_at, h.received_at, h.completed_report_at, h.request_report,
       h.complete_report
FROM goal_handoffs AS h;

DROP TABLE task_handoffs;
DROP TABLE goal_handoffs;
DROP TABLE decisions;
DROP TABLE agent_sessions;
DROP TABLE projects;

ALTER TABLE projects_new RENAME TO projects;
ALTER TABLE agent_sessions_new RENAME TO agent_sessions;
ALTER TABLE decisions_new RENAME TO decisions;
ALTER TABLE task_handoffs_new RENAME TO task_handoffs;
ALTER TABLE goal_handoffs_new RENAME TO goal_handoffs;

CREATE INDEX idx_projects_legacy_id ON projects(legacy_id);
CREATE INDEX idx_agent_sessions_project_registered_at ON agent_sessions(project_id, registered_at DESC);
CREATE UNIQUE INDEX idx_agent_sessions_session_key ON agent_sessions(session_key) WHERE session_key <> '';
CREATE INDEX idx_decisions_open ON decisions(status, goal_id);
CREATE INDEX idx_decisions_legacy_id ON decisions(legacy_id);
CREATE INDEX idx_task_handoffs_task_id ON task_handoffs(task_id);
CREATE UNIQUE INDEX idx_task_handoffs_open_task_id ON task_handoffs(task_id) WHERE completed_report_at IS NULL;
CREATE INDEX idx_goal_handoffs_goal_id ON goal_handoffs(goal_id);
CREATE UNIQUE INDEX idx_goal_handoffs_open_goal_id ON goal_handoffs(goal_id) WHERE completed_report_at IS NULL;

DROP TABLE agent_session_id_map;
