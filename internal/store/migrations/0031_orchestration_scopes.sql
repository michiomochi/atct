ALTER TABLE monitor_health ADD COLUMN scope_key TEXT NOT NULL DEFAULT '';
ALTER TABLE monitor_health ADD COLUMN agent_session_id INTEGER NOT NULL DEFAULT 0;

CREATE TABLE orchestration_scope (
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

CREATE INDEX orchestration_scope_project_active_idx
    ON orchestration_scope(project_id, active, scope_key);

CREATE INDEX orchestration_scope_goal_active_idx
    ON orchestration_scope(goal_id, active, scope_key);

CREATE INDEX orchestration_scope_task_active_idx
    ON orchestration_scope(task_id, active, scope_key);
