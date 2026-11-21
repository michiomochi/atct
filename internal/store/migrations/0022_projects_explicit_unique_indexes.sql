CREATE TABLE projects_new (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  root_path  TEXT NOT NULL,
  created_at TEXT NOT NULL,
  claimed_by INTEGER NOT NULL DEFAULT 0,
  claimed_at TEXT
);

INSERT INTO projects_new (id, name, root_path, created_at, claimed_by, claimed_at)
SELECT id, name, root_path, created_at, claimed_by, claimed_at FROM projects;

DROP TABLE projects;
ALTER TABLE projects_new RENAME TO projects;

CREATE UNIQUE INDEX idx_projects_name ON projects(name);
CREATE UNIQUE INDEX idx_projects_root_path ON projects(root_path);
