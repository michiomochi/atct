---
name: atct
description: Use when working on a goal that a human is tracking - declaring the tasks you plan to do, claiming one before you start, and asking the human for a decision instead of guessing. Also use when you are about to finish and need approval.
---

# ATCT

ATCT records work and routes decisions. Follow the `next_step` in every ATCT
response; it is the current operation derived from the same state machine.
Read `doc/execution-flow.md` only when `next_step` does not answer the
question. The complete transition table is there.

## Roles

The daemon derives `expected_role` in this order of priority:

- `commander`: a project claim.
- `subcommander`: a received, uncompleted goal handoff.
- `executor`: a received, unfinished task handoff.

Role values for `expected_role`: `commander`, `subcommander`, `executor`.

| Layer | Does | Does not |
| --- | --- | --- |
| `commander` | triage incoming work / split goals / prepare a working area / review landed changes / publish / resolve conflicts / clean up | design the goal / implement the goal / edit executor deliverables |
| `subcommander` | design the goal / delegate the goal's work / review implementation / report completion for the goal / issue decisions to the human / commit the goal's work / close a task its worker cannot | inspect or manage other goals / publish / create another subcommander / claim the project |
| `executor` | implement / test / close the task it was given | make design decisions / re-delegate / commit / write internal version-control details |

Only `atct:dev:start` may enter development mode. In that mode an executor
escalates to its subcommander and a subcommander to its commander; resolution
creates a remediation goal, then returns to the normal spec -> plan -> review
flow.

After `atct_session_identify`, call `atct_role` before role-specific work. On
mismatch, stop and return the handoff or let the owning layer recover it. On a
match, load exactly the matching role skill (`atct:commander`,
`atct:subcommander`, or `atct:executor`); role skills own their operations and
must not replace these rules or load another role's skill.

## Spec and plan live in the goal

In ATCT-managed work, write the spec and plan in full into the goal's
`spec` / `plan` fields with `atct_goal_update_request_report`. Do not add files
under `doc/specs/`, `doc/plans/` or `docs/superpowers/`. Do not put only a reference
such as "see doc/plans/x.md" in a field; a reference-only field is refused.

When this conflicts with superpowers' default save location (brainstorming,
writing-plans) or a personal setting that points at `doc/specs` or `doc/plans`,
the goal's fields win under ATCT. Existing files in those directories are not
removed or migrated.

## Declare before you work

An active goal authorizes coordination; do not wait for another plan approval.
The accepted-plan order is mandatory:

1. The plan handoff is completed first.
2. The daemon creates the task-create handoff.
3. The subcommander receives it and calls `atct_task_create` with its same
   `handoff_id` and a stable `idempotency_key`; then work only on those tasks.

After a goal handoff review is rejected, the daemon issues a new task-create
handoff; receive it the same way if the fix needs new tasks.

**Out of order:** Tasks created before an accepted plan are never reviewed, and
work done before it is declared never reaches the dashboard.

## Fix a declared task

Fix a declared `todo` or `doing` task with `atct_task_update_content`; `done`
and `dropped` tasks are rejected. Re-declaring with the same `idempotency_key`
does not update the task.

## Watch

Claude Code only. A watch delivers the answers and wakeups addressed to this
session. Codex needs nothing here: the `atct codex monitor` supervisor owns it.

The default is the Monitor tool: an agent with many notifications gets each one
in a single turn, where a background Bash costs about three (woken, read the
output, re-arm, then act). A background Bash with `--once` is only for an agent
that has turned out to be waiting: it ends after the first event, so an idle
agent is not woken by expiry notices. A delivery record per token keeps a
re-armed watch from emitting the same notification twice, for both forms.

1. **Start with the Monitor tool.** Run `atct watch --monitor --token
   <monitor_token>` with `persistent: true` and `description` set to `ATCT
   answer watch`, using the `monitor_token` SessionStart printed (the one passed
   to `atct_session_identify`). Without `persistent: true`, `timeout_ms`
   defaults to `300000ms` (5 minutes) and monitoring stops silently. The server
   derives the scope from the assignment, so pass no goal, project, or role.
   Remember the task id the call returns: `TaskStop` needs it.
2. **When the Monitor expires,** the notice reads `[Monitor expired after 30m
   with N events delivered. ...]`. Make no other response; arm the next watch
   first. If N is 0, that 30 minutes was only waiting: switch to the Bash tool
   with `run_in_background: true` and `atct watch --monitor --token
   <monitor_token> --once`. If N is 1 or more, attach the Monitor again.
3. **When a `--once` Bash ends,** whether it printed an event, ended with no
   event, or failed, attach the Monitor first, then act on the event (its
   stdout). Do not re-arm the `--once` Bash.
4. **Keep one.** Hold exactly one watch per token; the one started later stops
   the earlier one.
5. **Stop** a watch with `TaskStop` and the task id of the Monitor or the
   background Bash (see `atct:stop`). If the task id is unknown, say so and do
   not call `TaskStop`; never guess one.

**Out of order:** Acting on the event before attaching the Monitor leaves the
session with no watch. The daemon holds the session live for 5 minutes after a
`--once` watch ends on its own, but that grace is insurance, not permission to
skip the Monitor: past it, ATCT calls are refused and `wakeup.monitor_lost` is
raised.

## Receive before you start

Every implementation task is delegated. The worker must receive its task handoff before touching the work, passing the
exact SessionStart `session_key` and `monitor_token` when one was issued.
Receipt is exclusive.

## Handoff review order

1. The executor calls `atct_task_handoff_review_request` when ready.
2. The subcommander receives and reviews it, then completes the task or rejects
   it; rejection returns the same handoff to the worker for another request.

**Out of order:** The worker that received a handoff must never close it; only
the delegating/reviewing layer does, for task, goal, and plan handoffs. No task
becomes `done` while a decision on it is still open.

## Decisions and unsafe operations

Human decisions go through ATCT only: use `atct_decision_ask` with concrete
options and a recommended default, never a conversational question. Apply an
answer with `atct_decision_poll` before continuing dependent work; withdraw a
decision that is no longer relevant. Ask immediately before an irreversible or
destructive operation. If a design choice changes the work and cannot be
settled from the code, ask instead of guessing.

## What the delegator answers

A delegator that answers a question about the inside of a goal is guessing; it
has not read that goal's code. On 2026-08-27 a commander answered two such
questions and both answers were wrong: that `tools.go` had to change before a
tool was reachable from MCP, and that `wakeup.go` read a file it does not read.
The subcommander that had read the code corrected both.

Four kinds of question belong to the delegator, because only it can see them:

- which of two goals owns a piece of work both could claim
- which other goal is editing a file this goal needs to edit
- whether a change is already on the main branch
- when the work is released

Four kinds look similar and belong to the subcommander, because it has read the
code and the delegator has not:

- which of two designs this goal takes
- what a function in this goal's code actually does
- how this goal's work is split into commits, and in what order
- how this goal's work is divided among its executors

A subcommander that brings the delegator one of the second four is asking the
wrong reader. A delegator that answers one of them is inventing the answer.
## Where an unsent report goes

Silence upward is only safe when nothing is lost. Every kind of message a
subcommander used to speak has a place in the record instead, and the record
reaches the human without passing through the delegator's context.

| What used to be spoken | Where it goes |
|---|---|
| receipt of the goal | the `atct_goal_handoff_receive` record itself |
| progress on the work | tasks: `atct_task_create`, then `done` as each one lands |
| the design and why | the goal's `spec` and `plan` fields, in full, and `work_done` |
| something found inside this goal | `surprises` and `needs_review` |
| something found that is another goal | `atct_decision_ask`, addressed to the human |
| what was left undone | `next_goal_ids`, the ids of the goals to proceed with next |
| the goal is ready for commander review | `atct_goal_handoff_review_request`, the one message |

`atct_goal_handoff_review_request` is terminal: call it once, and only after
every declared task is accepted and committed and the goal is ready for
commander review. Work that is unfinished or blocked stays open and goes to
`atct_decision_ask` with concrete options and the consequence of each; it is
never reported through a goal-handoff review request.
`atct_goal_review_request` applies only to an active goal after the commander
has received the goal-handoff review.

A subcommander that stops working sends nothing at all, and the old habit caught
that only because a delegator noticed a quiet pane. The record catches it
instead: a goal with tasks and no commits, or a closed handoff with nothing
committed, each raises a Wakeup on the delegator's watch. On 2026-08-27 goal
172 stalled with three tasks still `todo` and eight files uncommitted, and goal
144 closed its handoff with no commits and four tasks still `todo`. Both
Wakeups had already fired; nobody had been told to read them.

## Fill in a report on a handoff that is already closed

Only a subcommander or commander uses this repair path when a closed handoff
has no report. It is not part of normal executor completion.

1. Confirm the handoff is already closed and carries no report.
2. Call `atct_task_handoff_report_amend` with the specific `handoff_id`, its
   `task_id`, and a non-empty `complete_report`; for a goal handoff call
   `atct_goal_handoff_report_amend` with `handoff_id`, `goal_id`, and
   `complete_report`.

**Out of order:** Amending an open handoff hides the missing completion instead
of exposing it, and the worker that owed `atct_task_handoff_complete` never
learns it owed anything.

## Recover when your role comes back wrong

If `atct_role` returns `executor` while you still hold work that should be yours, stop working and read this section.

Closing a subcommander's goal handoff drops that subcommander to `executor`
with no announcement, and the dashboard shows the goal completed while its work
is still uncommitted.

1. Stop working; what you do now is recorded against a layer you do not hold.
2. The first recovery path is `atct_session_identify`; follow `### Session keys` first.
3. Only if the session key does not restore your role, recover each layer:

   - project: `atct_project_release` → `atct_project_claim`
   - goal: `atct_goal_handoff_complete` → `atct_goal_handoff_request` (legacy/out-of-order recovery; the commander must issue the handoff again)
   - task: after the stale lock is released, have the subcommander request a fresh `atct_task_handoff_request`

**Out of order:** Layer repair before the session key closes a handoff that did
not need closing; the subcommander cannot reissue it, and the goal waits on the
commander.

| Trigger | Handoff |
|---|---|
| daemon restart | remains open; only the session record was lost |
| transport/session key correspondence lost | remains open |
| rejection of a completion report | remains closed; automatically reissued |
| out-of-order `atct_goal_handoff_complete`, or an executor calling it | remains closed; not reissued |

Rejection is automatic, so the goal step above that asks the commander to reissue the handoff is needed only for the last trigger.
Do not use a development-mode override or close another worker's handoff.

## Completion

Workers implement, run the task's specified verification, and request review.
Reviewers receive, review, and complete or reject; subcommanders commit goal
work only after its tasks are done, and commanders handle merge and publish.
When committing the goal's work, name the paths explicitly; never use `git add -A`.

Completion reports have five required text fields (`work_done`, `now_possible`,
`how_to_verify`, `surprises`, `needs_review`) plus an optional `next_goal_ids`
list of existing goal ids (empty or omitted when none). Keep the text fields
concise and say `none` where one does not apply. A legacy `next_steps` input is
rejected, not ignored. A goal is complete only after its tasks
and decisions are resolved and the appropriate ATCT approval flow is recorded.
