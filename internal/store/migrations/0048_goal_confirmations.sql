-- A commander's record that a proposed goal is still wanted after a review.
-- The latest confirmed_at pushes the goal's next review-due time out by 7 days.
-- confirmed_at uses the fixed-width timestamp format so MAX() orders correctly.
CREATE TABLE goal_confirmations (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  goal_id      INTEGER NOT NULL REFERENCES goals(id),
  note         TEXT NOT NULL,
  confirmed_at TEXT NOT NULL
);

CREATE INDEX idx_goal_confirmations_goal_id ON goal_confirmations(goal_id);
