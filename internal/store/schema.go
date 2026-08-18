package store

import "fmt"

const completionReportMaxLength = 2000

const completionReportCheckFormat = `CHECK (
  status <> 'done' OR (
    length(trim(work_done)) > 0 AND length(work_done) <= %[1]d AND
    length(trim(now_possible)) > 0 AND length(now_possible) <= %[1]d AND
    length(trim(how_to_verify)) > 0 AND length(how_to_verify) <= %[1]d AND
    length(trim(surprises)) > 0 AND length(surprises) <= %[1]d AND
    length(trim(needs_review)) > 0 AND length(needs_review) <= %[1]d AND
    length(trim(next_steps)) > 0 AND length(next_steps) <= %[1]d
  )
)`

func completionReportCheckSQL() string {
	return fmt.Sprintf(completionReportCheckFormat, completionReportMaxLength)
}

var schemaSQL = fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS projects (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  root_path  TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
  id            TEXT PRIMARY KEY,
  project_id    TEXT REFERENCES projects(id),
  registered_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_runs_project_registered_at
  ON runs(project_id, registered_at DESC);

CREATE TABLE IF NOT EXISTS goals (
  id             TEXT PRIMARY KEY,
  project_id     TEXT NOT NULL REFERENCES projects(id),
  title          TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL,
  result_summary TEXT NOT NULL DEFAULT '',
  work_done      TEXT NOT NULL DEFAULT '',
  now_possible   TEXT NOT NULL DEFAULT '',
  how_to_verify  TEXT NOT NULL DEFAULT '',
  surprises      TEXT NOT NULL DEFAULT '',
  needs_review   TEXT NOT NULL DEFAULT '',
  next_steps     TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  %s
);

CREATE TABLE IF NOT EXISTS tasks (
  id         TEXT PRIMARY KEY,
  goal_id    TEXT NOT NULL REFERENCES goals(id),
  title      TEXT NOT NULL,
  status     TEXT NOT NULL,
  agent      TEXT NOT NULL DEFAULT '',
  files      TEXT NOT NULL DEFAULT '[]',
  sort_order INTEGER NOT NULL DEFAULT 0,
  declare_key TEXT NOT NULL,
  claimed_by TEXT NOT NULL DEFAULT '',
  claimed_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_declare_key
  ON tasks(goal_id, declare_key);

CREATE TABLE IF NOT EXISTS decisions (
  id           TEXT PRIMARY KEY,
  goal_id      TEXT NOT NULL REFERENCES goals(id),
  task_id      TEXT REFERENCES tasks(id),
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
  run_id       TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  CHECK (kind <> 'decision' OR status NOT IN ('open', 'answered') OR (task_id IS NOT NULL AND task_id <> ''))
);

CREATE INDEX IF NOT EXISTS idx_decisions_open
  ON decisions(status, goal_id);
`, completionReportCheckSQL())
