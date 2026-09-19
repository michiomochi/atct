CREATE TABLE goals_new (
  id             INTEGER PRIMARY KEY,
  project_id     INTEGER NOT NULL REFERENCES projects(id),
  derived_from_goal_id INTEGER REFERENCES goals(id),
  content        TEXT NOT NULL,
  spec           TEXT NOT NULL DEFAULT '',
  plan           TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL,
  creator        TEXT NOT NULL DEFAULT 'human',
  result_summary TEXT NOT NULL DEFAULT '',
  work_done      TEXT NOT NULL DEFAULT '',
  now_possible   TEXT NOT NULL DEFAULT '',
  how_to_verify  TEXT NOT NULL DEFAULT '',
  surprises      TEXT NOT NULL DEFAULT '',
  needs_review   TEXT NOT NULL DEFAULT '',
  legacy_next_steps TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  CHECK (
    status <> 'done' OR (
      length(trim(work_done)) > 0 AND length(work_done) <= 2000 AND
      length(trim(now_possible)) > 0 AND length(now_possible) <= 2000 AND
      length(trim(how_to_verify)) > 0 AND length(how_to_verify) <= 2000 AND
      length(trim(surprises)) > 0 AND length(surprises) <= 2000 AND
      length(trim(needs_review)) > 0 AND length(needs_review) <= 2000
    )
  )
);

INSERT INTO goals_new (
  id, project_id, derived_from_goal_id, content, spec, plan, status, creator,
  result_summary, work_done, now_possible, how_to_verify, surprises,
  needs_review, legacy_next_steps, created_at, updated_at
)
SELECT
  id, project_id, derived_from_goal_id, content, spec, plan, status, creator,
  result_summary, work_done, now_possible, how_to_verify, surprises,
  needs_review, next_steps, created_at, updated_at
FROM goals;

DROP TABLE goals;
ALTER TABLE goals_new RENAME TO goals;

CREATE TABLE next_goals (
  goal_id INTEGER NOT NULL REFERENCES goals(id),
  next_goal_id INTEGER NOT NULL REFERENCES goals(id),
  sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
  created_at TEXT NOT NULL,
  PRIMARY KEY (goal_id, next_goal_id),
  UNIQUE (goal_id, sort_order),
  CHECK (goal_id <> next_goal_id)
);

CREATE INDEX idx_next_goals_next_goal_id ON next_goals(next_goal_id);
