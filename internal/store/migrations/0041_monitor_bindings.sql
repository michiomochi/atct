CREATE TABLE monitor_bindings (
  token TEXT PRIMARY KEY,
  agent_session_id INTEGER NOT NULL REFERENCES agent_sessions(id),
  created_at TEXT NOT NULL
);

CREATE INDEX idx_monitor_bindings_agent_session_id ON monitor_bindings(agent_session_id);
