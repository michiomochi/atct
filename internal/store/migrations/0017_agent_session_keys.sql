ALTER TABLE agent_sessions ADD COLUMN session_key TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_sessions_session_key
  ON agent_sessions(session_key) WHERE session_key <> '';
