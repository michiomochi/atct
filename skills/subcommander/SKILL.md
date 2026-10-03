---
name: subcommander
description: Use when atct_role reports subcommander for a received goal handoff and its design, delegation, review, and completion.
---

# ATCT subcommander

`atct:atct` is the SSOT for shared ATCT rules. First record the goal handoff by
calling `atct_goal_handoff_receive` with the `goal_id` provided in the handoff
and the exact `session_key` from SessionStart; optional `handoff_id` and
`monitor_token` may be included. Then call `atct_role` with `expected_role` set to `subcommander`; on a mismatch, stop.

- Design only the received goal; write its spec and plan, then submit its plan
  handoff for commander review.
- After plan acceptance, declare and delegate each task, review executor
  handoffs, and accept or reject them.
- Commit the goal's accepted work and submit its goal handoff for commander
  review. Ask the human only through ATCT decisions.

## Executor workspace bootstrap

Before applying the reuse rule in `## Delegate a task`, classify the goal space:

- If the space has no executor pane, create the first executor pane in that same space. This is the required bootstrap path, not an exception to the additional-pane rule.
- If an idle executor exists, reuse it for the next unassigned task.
- Only after an executor exists may an additional pane be created, and only for parallel work, worktree isolation, context exhaustion, or a topic change.

After the pane is prepared, request the task handoff before starting the monitored worker. The worker must receive the recorded handoff before it starts implementation.

Do not inspect other goals, publish, create another subcommander, or claim the
project.

On an unexpected condition, escalate to the commander. In `atct:dev:start`
development mode only, resolve an escalated executor condition in this Goal if
needed, then create a remediation Goal recording cause, resolution, and
verification.
