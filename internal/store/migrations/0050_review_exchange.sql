CREATE TABLE IF NOT EXISTS plan_handoff_entries (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  handoff_id        TEXT NOT NULL REFERENCES plan_handoffs(id) ON DELETE RESTRICT,
  kind              TEXT NOT NULL CHECK (kind IN ('review_requested', 'review_rejected')),
  body              TEXT NOT NULL,
  author_session_id INTEGER REFERENCES agent_sessions(id) ON DELETE SET NULL,
  created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_plan_handoff_entries_handoff_sequence
  ON plan_handoff_entries(handoff_id, id);

CREATE TABLE IF NOT EXISTS handoff_history_gaps (
  handoff_id TEXT PRIMARY KEY,
  scope      TEXT NOT NULL CHECK (scope IN ('goal', 'task', 'plan'))
);

INSERT OR IGNORE INTO handoff_history_gaps (handoff_id, scope)
SELECT id, 'goal' FROM goal_handoffs WHERE review_requested_at IS NOT NULL;
INSERT OR IGNORE INTO handoff_history_gaps (handoff_id, scope)
SELECT id, 'task' FROM task_handoffs WHERE review_requested_at IS NOT NULL;
INSERT OR IGNORE INTO handoff_history_gaps (handoff_id, scope)
SELECT id, 'plan' FROM plan_handoffs WHERE review_requested_at IS NOT NULL;

INSERT INTO goal_handoff_entries (handoff_id, kind, body, author_session_id, created_at)
SELECT h.id, 'review_requested', h.review_request_report, h.review_requested_by, h.review_requested_at
FROM goal_handoffs AS h
WHERE h.review_requested_at IS NOT NULL
  AND trim(COALESCE(h.review_request_report, '')) <> ''
  AND NOT EXISTS (SELECT 1 FROM goal_handoff_entries AS e WHERE e.handoff_id = h.id AND e.kind = 'review_requested');
INSERT INTO goal_handoff_entries (handoff_id, kind, body, author_session_id, created_at)
SELECT h.id, 'review_rejected', h.review_reject_report, h.review_received_by, h.review_rejected_at
FROM goal_handoffs AS h
WHERE h.review_rejected_at IS NOT NULL
  AND trim(COALESCE(h.review_reject_report, '')) <> ''
  AND NOT EXISTS (SELECT 1 FROM goal_handoff_entries AS e WHERE e.handoff_id = h.id AND e.kind = 'review_rejected');

INSERT INTO task_handoff_entries (handoff_id, kind, body, author_session_id, created_at)
SELECT h.id, 'review_requested', h.review_request_report, h.review_requested_by, h.review_requested_at
FROM task_handoffs AS h
WHERE h.review_requested_at IS NOT NULL
  AND trim(COALESCE(h.review_request_report, '')) <> ''
  AND NOT EXISTS (SELECT 1 FROM task_handoff_entries AS e WHERE e.handoff_id = h.id AND e.kind = 'review_requested');
INSERT INTO task_handoff_entries (handoff_id, kind, body, author_session_id, created_at)
SELECT h.id, 'review_rejected', h.review_reject_report, h.review_received_by, h.review_rejected_at
FROM task_handoffs AS h
WHERE h.review_rejected_at IS NOT NULL
  AND trim(COALESCE(h.review_reject_report, '')) <> ''
  AND NOT EXISTS (SELECT 1 FROM task_handoff_entries AS e WHERE e.handoff_id = h.id AND e.kind = 'review_rejected');

INSERT INTO plan_handoff_entries (handoff_id, kind, body, author_session_id, created_at)
SELECT h.id, 'review_requested', h.review_request_report, h.review_requested_by, h.review_requested_at
FROM plan_handoffs AS h
WHERE h.review_requested_at IS NOT NULL
  AND trim(COALESCE(h.review_request_report, '')) <> ''
  AND NOT EXISTS (SELECT 1 FROM plan_handoff_entries AS e WHERE e.handoff_id = h.id AND e.kind = 'review_requested');
INSERT INTO plan_handoff_entries (handoff_id, kind, body, author_session_id, created_at)
SELECT h.id, 'review_rejected', h.review_reject_report, h.review_received_by, h.review_rejected_at
FROM plan_handoffs AS h
WHERE h.review_rejected_at IS NOT NULL
  AND trim(COALESCE(h.review_reject_report, '')) <> ''
  AND NOT EXISTS (SELECT 1 FROM plan_handoff_entries AS e WHERE e.handoff_id = h.id AND e.kind = 'review_rejected');
