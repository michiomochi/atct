---
name: atct
description: Use when working on a goal that a human is tracking - declaring the tasks you plan to do, claiming one before you start, and asking the human for a decision instead of guessing. Also use when you are about to finish and need approval.
---

# ATCT

ATCT records work and routes decisions. Follow the `next_step` in every ATCT
response; it is the current operation derived from the same state machine. The
complete transition table is in `doc/execution-flow.md`.

## Roles and session

The daemon derives `expected_role` in this order:

1. A project claim means `commander`.
2. A received, unfinished goal handoff means `subcommander`.
3. A received, unfinished task handoff means `executor`.

| Role | Does | Does not |
| --- | --- | --- |
| `commander` | split goals, provide worktrees, review landed work, merge, publish, clean up | design or implement goals; edit executor deliverables |
| `subcommander` | design, delegate, review implementation, report the goal, issue decisions, commit goal work | manage other goals; publish; claim the project; implement executor work |
| `executor` | implement and test the received task; request review | design, re-delegate, commit, or write internal VCS details |

Only `atct:dev:start` may enter development mode. In that mode an executor
escalates to its subcommander and a subcommander to its commander; resolution
creates a remediation goal, then returns to the normal spec -> plan -> review
flow.

After `atct_session_identify`, call `atct_role` before role-specific work. On
mismatch, stop and return the handoff or let the owning layer recover it. On a
match, load exactly the matching role skill (`atct:commander`,
`atct:subcommander`, or `atct:executor`); role skills own their operations and
must not replace these rules or load another role's skill.

## Declare and receive before work

An active goal authorizes coordination; do not wait for another plan approval.
Declare only the tasks you will do with `atct_task_create` and a stable
`idempotency_key`, then work only on those tasks. Fix a declared `todo` or
`doing` task with `atct_task_update_content`; re-declaration does not update it.

The accepted-plan order is mandatory: the plan handoff is completed first, the
daemon creates the task-create handoff, then the subcommander receives that
handoff and calls `atct_task_create` with its same `handoff_id`. Do not create
tasks before an accepted plan.

Every implementation task is delegated. The worker must receive its task
handoff before touching the work, passing the exact SessionStart `session_key`
and `monitor_token` when one was issued. Receipt is exclusive.

## Handoff review order

The normal task order is:

`request -> receive -> review_request -> review_receive -> complete`

The executor requests review when ready. The subcommander receives and reviews
that request, then completes the task or rejects it. Rejection returns the same
handoff to the worker for correction and another review request. The worker
that received a handoff must never close it or call its completion operation;
only the delegating/reviewing layer closes it. The same rule applies to goal
and plan handoffs. No task becomes `done` while a decision on it is still open.

## Decisions and unsafe operations

Human decisions go through ATCT only: use `atct_decision_ask` with concrete
options and a recommended default, never a conversational question. Apply an
answer with `atct_decision_poll` before continuing dependent work; withdraw a
decision that is no longer relevant. Ask immediately before an irreversible or
destructive operation. If a design choice changes the work and cannot be
settled from the code, ask instead of guessing.

## Recovery

If role or ownership is wrong, stop. First retry `atct_session_identify` with
the exact session key. Only the stale-owner recovery for the affected layer is
valid: the owning layer may release/reclaim a stale project, goal, or task;
otherwise the delegator must issue a fresh handoff after releasing the stale
lock. Do not use a development-mode override, close another worker's handoff,
or self-repair a received handoff.

## Completion

Workers implement, run the task's specified verification, and request review.
Reviewers receive, review, and complete or reject; subcommanders commit goal
work only after its tasks are done, and commanders handle merge and publish.
Use explicit paths when staging; never use `git add -A`.

Completion reports state: `work_done`, `now_possible`, `how_to_verify`,
`surprises`, `needs_review`, and `next_steps`. Keep them concise and say
`none` where a field does not apply. A goal is complete only after its tasks
and decisions are resolved and the appropriate ATCT approval flow is recorded.
