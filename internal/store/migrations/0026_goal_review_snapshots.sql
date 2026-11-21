CREATE TABLE goal_review_snapshots (
  decision_id   INTEGER PRIMARY KEY REFERENCES decisions(id),
  goal_id       INTEGER NOT NULL REFERENCES goals(id),
  work_done     TEXT NOT NULL,
  now_possible  TEXT NOT NULL,
  how_to_verify TEXT NOT NULL,
  surprises     TEXT NOT NULL,
  needs_review  TEXT NOT NULL,
  next_steps    TEXT NOT NULL,
  created_by    INTEGER NOT NULL,
  created_at    TEXT NOT NULL
);

CREATE INDEX idx_goal_review_snapshots_goal_id
ON goal_review_snapshots(goal_id);

-- Only open legacy reviews can still be acted on. Recover a report when the
-- active goal already contains every canonical field; do not infer values
-- from a decision question or an unstructured handoff report.
INSERT INTO goal_review_snapshots (
  decision_id, goal_id, work_done, now_possible, how_to_verify,
  surprises, needs_review, next_steps, created_by, created_at
)
SELECT
  d.id, d.goal_id, g.work_done, g.now_possible, g.how_to_verify,
  g.surprises, g.needs_review, g.next_steps, d.agent_session_id, d.created_at
FROM decisions AS d
JOIN goals AS g ON g.id = d.goal_id
WHERE d.kind = 'goal_review'
  AND d.status = 'open'
  AND length(trim(g.work_done)) > 0
  AND length(trim(g.now_possible)) > 0
  AND length(trim(g.how_to_verify)) > 0
  AND length(trim(g.surprises)) > 0
  AND length(trim(g.needs_review)) > 0
  AND length(trim(g.next_steps)) > 0;

-- Open reviews without a recoverable snapshot are unsafe to approve. Leave
-- closed history untouched and require an explicit commander reissue.
UPDATE decisions
SET status = 'withdrawn',
    answer_text = 'goal review snapshot was not recoverable during migration; commander must reissue the review'
WHERE kind = 'goal_review'
  AND status = 'open'
  AND NOT EXISTS (
    SELECT 1
    FROM goal_review_snapshots AS s
    WHERE s.decision_id = decisions.id
  );
