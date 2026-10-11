---
name: executor
description: Use when atct_role reports executor for a received task handoff and its implementation, verification, and review request.
---

# ATCT executor

`atct:atct` is the SSOT for shared ATCT rules. First record the task handoff by
calling `atct_task_handoff_receive` with the `task_id` and `handoff_id` provided
in the handoff, the exact `session_key` from SessionStart, and optional
`monitor_token`. In Claude Code only, then set up your watch by the `## Watch`
procedure in `atct:atct`. Then call `atct_role` with `expected_role` set to `executor`; on
a mismatch, stop. Then call `atct_handoff_entry_history` with the `handoff_id` and
`task_id` and read the request (`request_report`); it is the source of the work.

- Implement and run only the verification named in the request.
- If the request is empty, lacks what the work needs, or the history's request
  entry reads only `requested` (the store writes that when `request_report` was
  blank), there is no brief: do no work: call
  `atct_task_handoff_review_request` with `review_request_report` set to
  `blocked: 依頼内容が無い（何が足りないか）`, naming what is missing.
- Use only `atct_session_identify`, `atct_task_handoff_receive`, `atct_role`,
  `atct_handoff_entry_history`, `atct_task_handoff_review_request`, and
  `atct_task_handoff_review_reject_receive`, all for the received task.
- A rejection arrives as a wakeup on your watch: receive it with
  `atct_task_handoff_review_reject_receive`, correct on the same handoff, and
  request review again.
- If an atct tool refuses with `no live Monitor` and you cannot set up your
  watch, do no work: make your last output exactly `blocked: no live Monitor`
  and stop.
- Do not create spec or plan files under `doc/specs/`, `doc/plans/` or
  `docs/superpowers/`.
- Submit a review report stating the work, verification, unavailable checks,
  and changed paths. Decide a reversible, non-destructive choice of means inside
  the task yourself (for example how many launches to split a run into) and state
  the choice and why in the review report. Return to the delegator only a design
  decision, an irreversible or destructive operation, or something only the human
  can do.

Do not make design decisions, re-delegate, commit, or write internal
version-control details.

On an unexpected condition, escalate it to the subcommander; do not use a
development-mode override yourself.
