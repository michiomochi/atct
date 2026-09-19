CREATE TABLE orchestration_blockers (
    blocker_id  TEXT PRIMARY KEY,
    project_id  INTEGER NOT NULL REFERENCES projects(id),
    goal_id     INTEGER REFERENCES goals(id),
    task_id     INTEGER REFERENCES tasks(id),
    scope_key   TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('human_decision', 'dependency_merge')),
    source_id   TEXT NOT NULL,
    generation  TEXT NOT NULL,
    owner_role  TEXT NOT NULL CHECK (owner_role IN ('commander', 'subcommander')),
    instruction TEXT NOT NULL,
    opened_at   TEXT NOT NULL,
    resolved_at TEXT
);

CREATE UNIQUE INDEX orchestration_blockers_identity_idx
    ON orchestration_blockers(kind, source_id, generation);

CREATE INDEX orchestration_blockers_open_project_idx
    ON orchestration_blockers(project_id, resolved_at, opened_at, blocker_id);
