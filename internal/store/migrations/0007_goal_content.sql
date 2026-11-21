CREATE TABLE goals_new (
  id             TEXT PRIMARY KEY,
  project_id     TEXT NOT NULL REFERENCES projects(id),
  content        TEXT NOT NULL,
  status         TEXT NOT NULL,
  creator        TEXT NOT NULL DEFAULT 'human',
  result_summary TEXT NOT NULL DEFAULT '',
  work_done      TEXT NOT NULL DEFAULT '',
  now_possible   TEXT NOT NULL DEFAULT '',
  how_to_verify  TEXT NOT NULL DEFAULT '',
  surprises      TEXT NOT NULL DEFAULT '',
  needs_review   TEXT NOT NULL DEFAULT '',
  next_steps     TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  CHECK (
    status <> 'done' OR (
      length(trim(work_done)) > 0 AND length(work_done) <= 2000 AND
      length(trim(now_possible)) > 0 AND length(now_possible) <= 2000 AND
      length(trim(how_to_verify)) > 0 AND length(how_to_verify) <= 2000 AND
      length(trim(surprises)) > 0 AND length(surprises) <= 2000 AND
      length(trim(needs_review)) > 0 AND length(needs_review) <= 2000 AND
      length(trim(next_steps)) > 0 AND length(next_steps) <= 2000
    )
  )
);

INSERT INTO goals_new (
  id, project_id, content, status, creator, result_summary,
  work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
  created_at, updated_at
)
SELECT
  id,
  project_id,
  CASE
    WHEN trim(description) = '' THEN title
    ELSE title || char(10) || char(10) || description
  END,
  status,
  creator,
  result_summary,
  work_done,
  now_possible,
  how_to_verify,
  surprises,
  needs_review,
  next_steps,
  created_at,
  updated_at
FROM goals;

DROP TABLE goals;
ALTER TABLE goals_new RENAME TO goals;
