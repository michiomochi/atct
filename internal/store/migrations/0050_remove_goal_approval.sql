-- Goal 321: the awaiting-approval state is gone. Goals are active at creation.
UPDATE goals
SET status = 'dropped',
    result_summary = '承認待ち状態の廃止（Goal 321）に伴い取り下げ。必要なら作り直す',
    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE status = 'proposed';

-- Same state WithdrawDecision gives open decisions, so they leave the unanswered list.
UPDATE decisions
SET status = 'withdrawn',
    answer_text = '承認待ち状態の廃止（Goal 321）に伴い取り下げ。必要なら作り直す'
WHERE kind = 'goal_approval' AND status = 'open';
