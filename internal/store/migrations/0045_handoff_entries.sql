CREATE TABLE task_handoff_entries (
  entry_id          TEXT PRIMARY KEY,
  handoff_id        TEXT NOT NULL REFERENCES task_handoffs(id) ON DELETE RESTRICT,
  sequence          INTEGER NOT NULL CHECK (sequence > 0),
  kind              TEXT NOT NULL CHECK (kind IN (
    'request', 'received', 'progress', 'question', 'answer',
    'review_request', 'review_response', 'complete', 'amend', 'system'
  )),
  body              TEXT NOT NULL,
  author_session_id INTEGER REFERENCES agent_sessions(id) ON DELETE SET NULL,
  relates_to        TEXT,
  source            TEXT NOT NULL DEFAULT '',
  created_at        TEXT NOT NULL,
  CHECK (relates_to IS NULL OR relates_to <> entry_id),
  UNIQUE (handoff_id, sequence)
);

CREATE INDEX idx_task_handoff_entries_handoff_sequence
  ON task_handoff_entries(handoff_id, sequence);

CREATE TRIGGER task_handoff_entries_no_update
BEFORE UPDATE ON task_handoff_entries
BEGIN
  SELECT RAISE(ABORT, 'task handoff entries are append-only');
END;

CREATE TRIGGER task_handoff_entries_no_delete
BEFORE DELETE ON task_handoff_entries
BEGIN
  SELECT RAISE(ABORT, 'task handoff entries are append-only');
END;

CREATE TABLE goal_handoff_entries (
  entry_id          TEXT PRIMARY KEY,
  handoff_id        TEXT NOT NULL REFERENCES goal_handoffs(id) ON DELETE RESTRICT,
  sequence          INTEGER NOT NULL CHECK (sequence > 0),
  kind              TEXT NOT NULL CHECK (kind IN (
    'request', 'received', 'progress', 'question', 'answer',
    'review_request', 'review_response', 'complete', 'amend', 'system'
  )),
  body              TEXT NOT NULL,
  author_session_id INTEGER REFERENCES agent_sessions(id) ON DELETE SET NULL,
  relates_to        TEXT,
  source            TEXT NOT NULL DEFAULT '',
  created_at        TEXT NOT NULL,
  CHECK (relates_to IS NULL OR relates_to <> entry_id),
  UNIQUE (handoff_id, sequence)
);

CREATE INDEX idx_goal_handoff_entries_handoff_sequence
  ON goal_handoff_entries(handoff_id, sequence);

CREATE TRIGGER goal_handoff_entries_no_update
BEFORE UPDATE ON goal_handoff_entries
BEGIN
  SELECT RAISE(ABORT, 'goal handoff entries are append-only');
END;

CREATE TRIGGER goal_handoff_entries_no_delete
BEFORE DELETE ON goal_handoff_entries
BEGIN
  SELECT RAISE(ABORT, 'goal handoff entries are append-only');
END;
