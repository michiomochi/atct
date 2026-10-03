UPDATE decisions
SET status = 'withdrawn',
    answer_text = 'legacy completion retired; request a named goal review',
    answered_at = COALESCE(answered_at, CURRENT_TIMESTAMP)
WHERE kind = 'completion'
  AND status = 'open';
