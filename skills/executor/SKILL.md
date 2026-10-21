---
name: executor
description: Use when atct_role reports executor for a received goal handoff and its design, implementation, verification, and completion report. Also covers a task_worker finishing a task handoff received before the two-layer change.
---

# ATCT executor

`atct:atct` is the SSOT for shared ATCT rules. First record the goal handoff by
calling `atct_goal_handoff_receive` with the `goal_id` provided in the handoff
and the exact `session_key` from SessionStart; optional `handoff_id` and
`monitor_token` may be included. In Claude Code only, then set up your watch by
the `## Watch` procedure in `atct:atct`.
Then call `atct_role` with `expected_role` set to `executor`; on a mismatch, stop.

- When the only thing you hold is an answer you are waiting for, wait on a `--once` background Bash rather than the Monitor, as `atct:atct` ## Watch step 6 says.
- Design only the received goal; write its spec and plan, then submit its plan
  handoff for commander review. Spec and plan go in full into the goal's `spec`
  and `plan` fields, never into files. Do not start the implementation before the
  plan is accepted.
- After plan acceptance, implement and verify the goal yourself. You decide
  whether to start sub-agents for part of the work. A sub-agent never calls an
  ATCT tool: every record is yours to write.
- Do not create tasks. In this version a task makes the daemon keep prompting
  you to delegate it with `atct_task_handoff_request`; the goal completes
  without any. Write progress in the review request report.
- Take only the parts of a goal you need from `atct_goal_get` with `fields` (for
  example `fields: ["task_summary"]` for task status alone); omit it for everything.
- Commit the goal's work and submit its goal handoff for commander review. You
  cannot call `atct_decision_ask`. Ask the human only through the commander, with
  a `review_requested` entry on your goal handoff in the format `atct:atct` gives
  under `### An executor's decision request`, and only in the cases `atct:atct`
  lists under `## Decisions and unsafe operations`; decide a reversible,
  non-destructive choice of means yourself and record it in `surprises` /
  `needs_review`. After asking, wait for the commander's `review_received` reply
  on a `--once` background Bash (`atct:atct` `## Watch` step 6) and read it with
  `atct_goal_handoff_entry_history`.
- Before the goal review request, run `git merge main --no-edit` in the goal
  worktree and the verification set; the daemon refuses the request when the goal
  branch does not contain main.
- Request review with `atct_goal_handoff_review_request`, passing the received
  `handoff_id`, the `goal_id`, and a non-empty `review_request_report`. The report
  states the work, every command you ran and its result, the checks you could not
  run (say "could not run"), and the changed paths.
- Use the advisor, when the advisor tool is available in your session (skip this
  where it is not): consult it before you settle a design or implementation
  approach, and again before you call `atct_goal_handoff_review_request`. Give it
  the goal's content, your approach or your diff, and the verification results. Fix
  what it finds that you agree with. In the review request report, record in one or
  two lines what you asked the advisor and what you changed because of it. This
  adds no tool call and no record of its own.
- A rejection arrives as a wakeup on your watch: receive it, correct on the same
  handoff, and request review again.
- An approval that only matters after the merge (a production deploy, a production
  re-run) is a different judgment from the goal's review. Do not leave it open next
  to your goal handoff review request: an open decision refuses the commander's
  `atct_goal_review_request` (`goal has an open decision`), and the human then
  cannot receive the review at all. `atct_goal_handoff_review_request` is refused the
  same way, naming each open decision. Do not file it as a decision: write it in
  `needs_review` and `next_goal_ids`, and the commander tells the human at approval.
  Decisions you asked the commander to file are answered, or withdrawn at your
  request, before you request the review.
- If an atct tool refuses with `no live Monitor` and you cannot set up your
  watch, do no work: make your last output exactly `blocked: no live Monitor`
  and stop.
- Do not create spec or plan files under `doc/specs/`, `doc/plans/` or
  `docs/superpowers/`.

Do not inspect other goals, publish, or claim the project. Never call
`atct_goal_complete`, `atct_goal_release`, `atct_goal_handoff_complete` or
`atct_goal_review_request`: they belong to the commander.

On an unexpected condition, escalate to the commander. In `atct:dev:start`
development mode only, resolve an escalated condition in this goal if needed,
then create a remediation goal recording cause, resolution, and verification.

## task_worker: finishing a task handoff received before the two-layer change

Only a session that `atct_role` reports as `task_worker` reads this section. It
holds a task handoff received before this release and finishes it by the old
procedure; it does not start a new task handoff.

1. Record the task handoff by calling `atct_task_handoff_receive` with the
   `task_id` and `handoff_id` provided in the handoff, the exact `session_key` from
   SessionStart, and optional `monitor_token`. In Claude Code only, then set up
   your watch by the `## Watch` procedure in `atct:atct`.
2. Call `atct_role` with `expected_role` set to `task_worker`; `executor` also
   matches. On a mismatch, stop.
3. Call `atct_handoff_entry_history` with the `handoff_id` and `task_id` and read the
   request (`request_report`); it is the source of the work. If it is empty or reads
   only `requested`, do no work: call `atct_task_handoff_review_request` with
   `review_request_report` set to `blocked: 依頼内容が無い（何が足りないか）`.
4. Implement and run only the verification named in the request. Use only
   `atct_session_identify`, `atct_task_handoff_receive`, `atct_role`,
   `atct_handoff_entry_history`, `atct_task_handoff_review_request`, and
   `atct_task_handoff_review_reject_receive`, all for the received task.
5. Submit a review report stating the work, verification, unavailable checks, and
   changed paths with `atct_task_handoff_review_request`. A rejection arrives as a
   wakeup: receive it with `atct_task_handoff_review_reject_receive`, correct on the
   same handoff, and request review again.

**Out of order:** Implementing before the task handoff is received leaves the work
with no recorded owner, and a review requested before the work is verified hands
the reviewer an unproven report.

A task_worker does not make design decisions, re-delegate, commit, open a human
decision (`atct_decision_ask`) or record a human's answer (`atct_decision_answer`).
It returns a design decision, an irreversible or destructive operation, or
something only the human can do to the session that sent the request.
