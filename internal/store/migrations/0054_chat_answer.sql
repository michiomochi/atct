-- Goal 365: a decision can be answered in the chat. The agent records the answer
-- with the source it came from. Dashboard answers leave these columns empty.
-- The daemon checks the quote against the harness transcript, so nothing else is stored.

ALTER TABLE decisions ADD COLUMN answered_via TEXT NOT NULL DEFAULT '';
ALTER TABLE decisions ADD COLUMN answer_quote TEXT NOT NULL DEFAULT '';
ALTER TABLE decisions ADD COLUMN answer_evidence TEXT NOT NULL DEFAULT '';
-- One human message answers at most one decision. Empty for dashboard answers.
ALTER TABLE decisions ADD COLUMN answer_prompt_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_decisions_answer_prompt
  ON decisions(answer_prompt_id) WHERE answer_prompt_id <> '';
