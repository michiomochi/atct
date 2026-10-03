CREATE TABLE goal_review_state_snapshots (
  decision_id   INTEGER PRIMARY KEY REFERENCES decisions(id),
  goal_id       INTEGER NOT NULL REFERENCES goals(id),
  result_summary TEXT NOT NULL,
  work_done     TEXT NOT NULL,
  now_possible  TEXT NOT NULL,
  how_to_verify TEXT NOT NULL,
  surprises     TEXT NOT NULL,
  needs_review  TEXT NOT NULL,
  next_goal_ids TEXT NOT NULL,
  created_at    TEXT NOT NULL
);

CREATE INDEX idx_goal_review_state_snapshots_goal_id
  ON goal_review_state_snapshots(goal_id);
