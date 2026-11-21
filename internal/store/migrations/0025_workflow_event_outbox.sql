-- Durable, project-scoped workflow notifications. The sequence row is
-- allocated in the same transaction as the state transition that emits the
-- event, so an event can never be visible without its corresponding state.
CREATE TABLE project_event_sequences (
  project_id    INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  last_sequence INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE workflow_event_outbox (
  project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  sequence    INTEGER NOT NULL,
  event_id    TEXT NOT NULL UNIQUE,
  event_name  TEXT NOT NULL,
  goal_id     INTEGER REFERENCES goals(id) ON DELETE CASCADE,
  task_id     INTEGER REFERENCES tasks(id) ON DELETE CASCADE,
  decision_id INTEGER REFERENCES decisions(id) ON DELETE CASCADE,
  handoff_id  TEXT,
  payload     TEXT NOT NULL,
  occurred_at TEXT NOT NULL,
  PRIMARY KEY (project_id, sequence)
);

CREATE INDEX idx_workflow_event_outbox_project_time
ON workflow_event_outbox(project_id, occurred_at, sequence);

CREATE INDEX idx_workflow_event_outbox_goal_sequence
ON workflow_event_outbox(project_id, goal_id, sequence);

CREATE INDEX idx_workflow_event_outbox_task_sequence
ON workflow_event_outbox(project_id, task_id, sequence);

-- goal_id=0 represents a project-wide watcher. This keeps the composite key
-- non-null while the public cursor API can continue to expose a nullable goal.
CREATE TABLE watch_delivery_cursors (
  watcher_key TEXT NOT NULL,
  project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  goal_id     INTEGER NOT NULL DEFAULT 0,
  sequence    INTEGER NOT NULL DEFAULT 0,
  updated_at  TEXT NOT NULL,
  PRIMARY KEY (watcher_key, project_id, goal_id)
);

CREATE INDEX idx_watch_delivery_cursors_project
ON watch_delivery_cursors(project_id, goal_id, sequence);
