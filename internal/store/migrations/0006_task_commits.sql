CREATE TABLE IF NOT EXISTS task_commits (
  task_id      TEXT NOT NULL REFERENCES tasks(id),
  sha          TEXT NOT NULL,
  subject      TEXT NOT NULL,
  files_changed INTEGER NOT NULL DEFAULT 0,
  insertions   INTEGER NOT NULL DEFAULT 0,
  deletions    INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (task_id, sha)
);
