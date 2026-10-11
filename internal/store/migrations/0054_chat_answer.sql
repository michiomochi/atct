-- Goal 365: a decision can be answered in the chat. The agent records the answer
-- with the source it came from. Dashboard answers leave these columns empty.
-- human_prompts keeps the human's recent prompts so the chat answer can be matched
-- back to the decision it answers.

CREATE TABLE human_prompts (
  id              INTEGER PRIMARY KEY,
  session_key     TEXT NOT NULL,
  prompt_id       TEXT NOT NULL,
  transcript_path TEXT NOT NULL,
  prompt          TEXT NOT NULL,
  recorded_at     TEXT NOT NULL
);

CREATE INDEX idx_human_prompts_session
  ON human_prompts(session_key, recorded_at);

ALTER TABLE decisions ADD COLUMN answered_via TEXT NOT NULL DEFAULT '';
ALTER TABLE decisions ADD COLUMN answer_quote TEXT NOT NULL DEFAULT '';
ALTER TABLE decisions ADD COLUMN answer_evidence TEXT NOT NULL DEFAULT '';
-- One human prompt answers at most one decision. Empty for dashboard answers.
ALTER TABLE decisions ADD COLUMN answer_prompt_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_decisions_answer_prompt
  ON decisions(answer_prompt_id) WHERE answer_prompt_id <> '';

-- The transcript path named by a session's first recorded prompt. A prompt that
-- names another path is never matched, so the agent cannot move a session to a
-- file it wrote itself. Kept apart from human_prompts so pruning never drops it.
CREATE TABLE human_prompt_sessions (
  session_key       TEXT PRIMARY KEY,
  transcript_path   TEXT NOT NULL,
  first_recorded_at TEXT NOT NULL
);
