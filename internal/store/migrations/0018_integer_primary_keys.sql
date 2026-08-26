-- A historical bootstrap can recreate this empty pre-0004 table. Removing it
-- makes those databases match a freshly migrated database.
DROP TABLE IF EXISTS runs;

CREATE TABLE projects_new (
  id         INTEGER PRIMARY KEY,
  legacy_id  TEXT,
  name       TEXT NOT NULL UNIQUE,
  root_path  TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  claimed_by TEXT NOT NULL DEFAULT '',
  claimed_at TEXT
);

INSERT INTO projects_new (legacy_id, name, root_path, created_at, claimed_by, claimed_at)
SELECT id, name, root_path, created_at, claimed_by, claimed_at
FROM projects
ORDER BY created_at, rowid;

CREATE TABLE agent_sessions_new (
  id            TEXT PRIMARY KEY,
  project_id    INTEGER REFERENCES projects(id),
  registered_at TEXT NOT NULL,
  pid           INTEGER NOT NULL DEFAULT 0,
  started_at    TEXT NOT NULL DEFAULT '',
  session_key   TEXT NOT NULL DEFAULT ''
);

INSERT INTO agent_sessions_new (id, project_id, registered_at, pid, started_at, session_key)
SELECT s.id, p.id, s.registered_at, s.pid, s.started_at, s.session_key
FROM agent_sessions AS s
LEFT JOIN projects_new AS p ON p.legacy_id = s.project_id;

CREATE TABLE goals_new (
  id                   INTEGER PRIMARY KEY,
  legacy_id            TEXT,
  project_id           INTEGER NOT NULL REFERENCES projects(id),
  derived_from_goal_id INTEGER REFERENCES goals(id),
  content              TEXT NOT NULL,
  status               TEXT NOT NULL,
  creator              TEXT NOT NULL DEFAULT 'human',
  result_summary       TEXT NOT NULL DEFAULT '',
  work_done            TEXT NOT NULL DEFAULT '',
  now_possible         TEXT NOT NULL DEFAULT '',
  how_to_verify        TEXT NOT NULL DEFAULT '',
  surprises            TEXT NOT NULL DEFAULT '',
  needs_review         TEXT NOT NULL DEFAULT '',
  next_steps           TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL,
  updated_at           TEXT NOT NULL,
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

INSERT INTO goals_new (
  legacy_id, project_id, derived_from_goal_id, content, status, creator,
  result_summary, work_done, now_possible, how_to_verify, surprises,
  needs_review, next_steps, created_at, updated_at
)
SELECT
  g.id, p.id, NULL, g.content, g.status, g.creator,
  g.result_summary, g.work_done, g.now_possible, g.how_to_verify, g.surprises,
  g.needs_review, g.next_steps, g.created_at, g.updated_at
FROM goals AS g
JOIN projects_new AS p ON p.legacy_id = g.project_id
ORDER BY g.created_at, g.rowid;

UPDATE goals_new AS migrated
SET derived_from_goal_id = (
  SELECT parent.id
  FROM goals AS legacy
  JOIN goals_new AS parent ON parent.legacy_id = legacy.derived_from_goal_id
  WHERE legacy.id = migrated.legacy_id
)
WHERE EXISTS (
  SELECT 1
  FROM goals AS legacy
  WHERE legacy.id = migrated.legacy_id
    AND legacy.derived_from_goal_id IS NOT NULL
);

CREATE TABLE tasks_new (
  id            INTEGER PRIMARY KEY,
  legacy_id     TEXT,
  goal_id       INTEGER NOT NULL REFERENCES goals(id),
  title         TEXT NOT NULL,
  description   TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,
  agent         TEXT NOT NULL DEFAULT '',
  files         TEXT NOT NULL DEFAULT '[]',
  sort_order    INTEGER NOT NULL DEFAULT 0,
  declare_key   TEXT NOT NULL,
  snoozed_until TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

INSERT INTO tasks_new (
  legacy_id, goal_id, title, description, status, agent, files, sort_order,
  declare_key, snoozed_until, created_at, updated_at
)
SELECT t.id, g.id, t.title, t.description, t.status, t.agent, t.files, t.sort_order,
       t.declare_key, t.snoozed_until, t.created_at, t.updated_at
FROM tasks AS t
JOIN goals_new AS g ON g.legacy_id = t.goal_id
ORDER BY t.created_at, t.rowid;

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
  agent_session_id         TEXT NOT NULL DEFAULT '',
  created_at               TEXT NOT NULL,
  CHECK (kind <> 'decision' OR status NOT IN ('open', 'answered') OR (task_id IS NOT NULL AND task_id <> ''))
);

INSERT INTO decisions_new (
  legacy_id, goal_id, task_id, kind, question, options, status, default_option,
  default_after_ms, default_applied_at, answer_label, answer_text, answered_at,
  applied_at, agent_session_id, created_at
)
SELECT d.id, g.id, t.id, d.kind, d.question, d.options, d.status, d.default_option,
       d.default_after_ms, d.default_applied_at, d.answer_label, d.answer_text,
       d.answered_at, d.applied_at, d.agent_session_id, d.created_at
FROM decisions AS d
JOIN goals_new AS g ON g.legacy_id = d.goal_id
LEFT JOIN tasks_new AS t ON t.legacy_id = d.task_id
ORDER BY d.created_at, d.rowid;

CREATE TABLE task_commits_new (
  task_id       INTEGER NOT NULL REFERENCES tasks(id),
  sha           TEXT NOT NULL,
  subject       TEXT NOT NULL,
  files_changed INTEGER NOT NULL DEFAULT 0,
  insertions    INTEGER NOT NULL DEFAULT 0,
  deletions     INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  PRIMARY KEY (task_id, sha)
);

INSERT INTO task_commits_new (task_id, sha, subject, files_changed, insertions, deletions, created_at)
SELECT t.id, c.sha, c.subject, c.files_changed, c.insertions, c.deletions, c.created_at
FROM task_commits AS c
JOIN tasks_new AS t ON t.legacy_id = c.task_id;

CREATE TABLE task_handoffs_new (
  id                  TEXT PRIMARY KEY,
  task_id             INTEGER NOT NULL REFERENCES tasks(id),
  requested_by        TEXT REFERENCES agent_sessions(id),
  received_by         TEXT REFERENCES agent_sessions(id),
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
SELECT h.id, t.id, h.requested_by, h.received_by, h.requested_at, h.received_at,
       h.completed_report_at, h.request_report, h.complete_report
FROM task_handoffs AS h
JOIN tasks_new AS t ON t.legacy_id = h.task_id;

CREATE TABLE goal_handoffs_new (
  id                  TEXT PRIMARY KEY,
  goal_id             INTEGER NOT NULL REFERENCES goals(id),
  requested_by        TEXT REFERENCES agent_sessions(id),
  received_by         TEXT REFERENCES agent_sessions(id),
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
SELECT h.id, g.id, h.requested_by, h.received_by, h.requested_at, h.received_at,
       h.completed_report_at, h.request_report, h.complete_report
FROM goal_handoffs AS h
JOIN goals_new AS g ON g.legacy_id = h.goal_id;

DROP TABLE task_commits;
DROP TABLE task_handoffs;
DROP TABLE goal_handoffs;
DROP TABLE decisions;
DROP TABLE tasks;
DROP TABLE goals;
DROP TABLE agent_sessions;
DROP TABLE projects;

ALTER TABLE projects_new RENAME TO projects;
ALTER TABLE agent_sessions_new RENAME TO agent_sessions;
ALTER TABLE goals_new RENAME TO goals;
ALTER TABLE tasks_new RENAME TO tasks;
ALTER TABLE decisions_new RENAME TO decisions;
ALTER TABLE task_commits_new RENAME TO task_commits;
ALTER TABLE task_handoffs_new RENAME TO task_handoffs;
ALTER TABLE goal_handoffs_new RENAME TO goal_handoffs;

CREATE INDEX idx_agent_sessions_project_registered_at ON agent_sessions(project_id, registered_at DESC);
CREATE UNIQUE INDEX idx_agent_sessions_session_key ON agent_sessions(session_key) WHERE session_key <> '';
CREATE INDEX idx_projects_legacy_id ON projects(legacy_id);
CREATE INDEX idx_goals_legacy_id ON goals(legacy_id);
CREATE UNIQUE INDEX idx_tasks_declare_key ON tasks(goal_id, declare_key);
CREATE UNIQUE INDEX idx_tasks_goal_sort_order ON tasks(goal_id, sort_order);
CREATE INDEX idx_tasks_legacy_id ON tasks(legacy_id);
CREATE INDEX idx_decisions_open ON decisions(status, goal_id);
CREATE INDEX idx_decisions_legacy_id ON decisions(legacy_id);
CREATE INDEX idx_task_handoffs_task_id ON task_handoffs(task_id);
CREATE UNIQUE INDEX idx_task_handoffs_open_task_id ON task_handoffs(task_id) WHERE completed_report_at IS NULL;
CREATE INDEX idx_goal_handoffs_goal_id ON goal_handoffs(goal_id);
CREATE UNIQUE INDEX idx_goal_handoffs_open_goal_id ON goal_handoffs(goal_id) WHERE completed_report_at IS NULL;

-- Use a half-open range on legacy_id for 8-character UUID prefixes: unlike
-- LIKE, it uses these BINARY indexes regardless of case_sensitive_like.
