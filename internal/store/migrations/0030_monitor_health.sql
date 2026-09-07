CREATE TABLE monitor_health (
    monitor_id TEXT PRIMARY KEY,
    agent_key TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL,
    role TEXT NOT NULL,
    project_id INTEGER NOT NULL,
    goal_id INTEGER,
    task_id INTEGER,
    pid INTEGER NOT NULL,
    process_started_at TEXT NOT NULL,
    state TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    transitioned_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    stopped_at TEXT
);

CREATE INDEX monitor_health_project_idx ON monitor_health(project_id);
CREATE INDEX monitor_health_last_seen_idx ON monitor_health(last_seen_at);
