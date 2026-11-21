CREATE TABLE orchestration_review_work (
    review_work_id              TEXT PRIMARY KEY,
    project_id                  INTEGER NOT NULL REFERENCES projects(id),
    goal_id                     INTEGER NOT NULL REFERENCES goals(id),
    task_id                     INTEGER REFERENCES tasks(id),
    kind                        TEXT NOT NULL CHECK (kind IN ('task', 'plan', 'goal')),
    handoff_id                  TEXT NOT NULL,
    requester_session_id        INTEGER NOT NULL,
    requester_scope_key         TEXT NOT NULL,
    expected_reviewer_role      TEXT NOT NULL,
    reviewer_scope_key          TEXT NOT NULL,
    reviewer_session_id         INTEGER,
    state                       TEXT NOT NULL CHECK (state IN ('requested', 'received', 'rejected', 'completed')),
    review_requested_generation TEXT NOT NULL,
    review_received_generation  TEXT,
    settlement_generation       TEXT,
    active                      INTEGER NOT NULL CHECK (active IN (0, 1)),
    action_role                 TEXT,
    action_scope_key            TEXT,
    action_task_id              INTEGER REFERENCES tasks(id),
    action_instruction          TEXT,
    opened_at                   TEXT NOT NULL,
    updated_at                  TEXT NOT NULL,
    resolved_at                 TEXT
);

CREATE UNIQUE INDEX orchestration_review_work_identity_idx
    ON orchestration_review_work(kind, handoff_id, review_requested_generation);

CREATE INDEX orchestration_review_work_project_idx
    ON orchestration_review_work(project_id, active, updated_at, review_work_id);
