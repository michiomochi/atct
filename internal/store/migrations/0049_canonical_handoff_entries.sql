CREATE TABLE task_handoff_entries_new (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  handoff_id        TEXT NOT NULL REFERENCES task_handoffs(id) ON DELETE RESTRICT,
  kind              TEXT NOT NULL CHECK (kind IN (
    'request', 'received', 'review_requested', 'review_received',
    'review_rejected', 'completed'
  )),
  body              TEXT NOT NULL,
  author_session_id INTEGER REFERENCES agent_sessions(id) ON DELETE SET NULL,
  in_reply_to_id    INTEGER,
  created_at        TEXT NOT NULL,
  CHECK (in_reply_to_id IS NULL OR in_reply_to_id <> id),
  UNIQUE (handoff_id, id),
  FOREIGN KEY (handoff_id, in_reply_to_id)
    REFERENCES task_handoff_entries_new(handoff_id, id) ON DELETE RESTRICT
);

INSERT INTO task_handoff_entries_new (
  handoff_id, kind, body, author_session_id, created_at
)
SELECT handoff_id,
       CASE kind
         WHEN 'review_request' THEN 'review_requested'
         WHEN 'review_response' THEN 'review_received'
         WHEN 'complete' THEN 'completed'
         ELSE kind
       END,
       body, author_session_id, created_at
FROM task_handoff_entries
WHERE kind IN (
  'request', 'received', 'review_request', 'review_response', 'review_rejected',
  'review_requested', 'review_received', 'completed', 'complete'
)
ORDER BY handoff_id, sequence;

DROP TABLE task_handoff_entries;
ALTER TABLE task_handoff_entries_new RENAME TO task_handoff_entries;

CREATE INDEX idx_task_handoff_entries_handoff_sequence
  ON task_handoff_entries(handoff_id, id);

CREATE TABLE goal_handoff_entries_new (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  handoff_id        TEXT NOT NULL REFERENCES goal_handoffs(id) ON DELETE RESTRICT,
  kind              TEXT NOT NULL CHECK (kind IN (
    'request', 'received', 'review_requested', 'review_received',
    'review_rejected', 'completed'
  )),
  body              TEXT NOT NULL,
  author_session_id INTEGER REFERENCES agent_sessions(id) ON DELETE SET NULL,
  in_reply_to_id    INTEGER,
  created_at        TEXT NOT NULL,
  CHECK (in_reply_to_id IS NULL OR in_reply_to_id <> id),
  UNIQUE (handoff_id, id),
  FOREIGN KEY (handoff_id, in_reply_to_id)
    REFERENCES goal_handoff_entries_new(handoff_id, id) ON DELETE RESTRICT
);

INSERT INTO goal_handoff_entries_new (
  handoff_id, kind, body, author_session_id, created_at
)
SELECT handoff_id,
       CASE kind
         WHEN 'review_request' THEN 'review_requested'
         WHEN 'review_response' THEN 'review_received'
         WHEN 'complete' THEN 'completed'
         ELSE kind
       END,
       body, author_session_id, created_at
FROM goal_handoff_entries
WHERE kind IN (
  'request', 'received', 'review_request', 'review_response', 'review_rejected',
  'review_requested', 'review_received', 'completed', 'complete'
)
ORDER BY handoff_id, sequence;

DROP TABLE goal_handoff_entries;
ALTER TABLE goal_handoff_entries_new RENAME TO goal_handoff_entries;

CREATE INDEX idx_goal_handoff_entries_handoff_sequence
  ON goal_handoff_entries(handoff_id, id);
