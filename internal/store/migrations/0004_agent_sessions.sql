ALTER TABLE runs RENAME TO agent_sessions;

ALTER TABLE agent_sessions ADD COLUMN pid INTEGER NOT NULL DEFAULT 0;

ALTER TABLE agent_sessions ADD COLUMN started_at TEXT NOT NULL DEFAULT '';

ALTER TABLE decisions RENAME COLUMN run_id TO agent_session_id;

DROP INDEX IF EXISTS idx_runs_project_registered_at;

CREATE INDEX IF NOT EXISTS idx_agent_sessions_project_registered_at
  ON agent_sessions(project_id, registered_at DESC);
