-- name: InsertHumanPrompt :exec
INSERT INTO human_prompts (session_key, prompt_id, transcript_path, prompt, recorded_at)
VALUES (?, ?, ?, ?, ?);

-- name: PruneHumanPrompts :exec
-- Keeps the newest 50 rows of the session: everything older than the 50th newest.
DELETE FROM human_prompts
WHERE human_prompts.session_key = ?
  AND human_prompts.id < (
    SELECT k.id FROM human_prompts AS k
    WHERE k.session_key = ?
    ORDER BY k.id DESC
    LIMIT 1 OFFSET 49
  );

-- name: PinHumanPromptTranscript :exec
INSERT INTO human_prompt_sessions (session_key, transcript_path, first_recorded_at)
VALUES (?, ?, ?)
ON CONFLICT(session_key) DO NOTHING;

-- name: GetPinnedTranscript :one
SELECT transcript_path FROM human_prompt_sessions WHERE session_key = ?;

-- name: ListHumanPromptsSince :many
SELECT id, session_key, prompt_id, transcript_path, prompt, recorded_at
FROM human_prompts
WHERE session_key = ? AND recorded_at >= ?
ORDER BY id DESC;
