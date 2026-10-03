-- Goal 321: the awaiting-approval state is gone. Goals are active at creation.
-- A proposed goal touched within the last 7 days is in use, so it becomes active
-- (7 days is the window the removed review_due_goals used). An older one is stale
-- and is dropped. The same rule applies to every project.
-- julianday() compares the instants, so it holds for stored timestamps with or
-- without a fractional second.
--
-- Decisions first: their treatment depends on the goal still being proposed.

-- Same state ApplyGoalApprovalDecision gave an approved goal_approval.
UPDATE decisions
SET status = 'applied',
    answer_label = 'approve',
    answered_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000000Z',
    applied_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000000Z'
WHERE kind = 'goal_approval' AND status = 'open'
  AND goal_id IN (SELECT id FROM goals
                  WHERE status = 'proposed' AND julianday(updated_at) >= julianday('now', '-7 days'));

-- Same state WithdrawDecision gives open decisions, so they leave the unanswered list.
UPDATE decisions
SET status = 'withdrawn',
    answer_text = '承認待ち状態の廃止（Goal 321）に伴い取り下げ。必要なら作り直す'
WHERE kind = 'goal_approval' AND status = 'open'
  AND goal_id IN (SELECT id FROM goals
                  WHERE status = 'proposed' AND julianday(updated_at) < julianday('now', '-7 days'));

UPDATE goals
SET status = 'dropped',
    result_summary = '承認待ち状態の廃止（Goal 321）に伴い取り下げ。必要なら作り直す',
    updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000000Z'
WHERE status = 'proposed' AND julianday(updated_at) < julianday('now', '-7 days');

UPDATE goals
SET status = 'active',
    updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000000Z'
WHERE status = 'proposed';
