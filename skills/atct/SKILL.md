---
name: atct
description: Use when working on a goal that a human is tracking - declaring the tasks you plan to do, claiming one before you start, and asking the human for a decision instead of guessing. Also use when you are about to finish and need approval.
---

# ATCT

ATCT records work and routes decisions.
Before any ATCT work, run `atct execution-flow` and follow its procedure.
After you have read it, follow the `next_step` in every ATCT response; it is
the current operation derived from the same state machine.

## Roles

The daemon derives `expected_role` in this order of priority:

- `commander`: a project claim.
- `subcommander`: a received, uncompleted goal handoff.
- `executor`: a received, unfinished task handoff.

Role values for `expected_role`: `commander`, `subcommander`, `executor`.

| Layer | Does | Does not |
| --- | --- | --- |
| `commander` | triage incoming work / split goals / prepare a working area / review landed changes / publish / resolve conflicts / clean up | design the goal / implement the goal / edit executor deliverables |
| `subcommander` | design the goal / delegate the goal's work / review implementation / report completion for the goal / ask the commander to file decisions for the human / commit the goal's work / close a task its worker cannot | inspect or manage other goals / publish / create another subcommander / claim the project / file decisions to the human directly |
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

Write a goal's `content`, `spec` and `plan` in Markdown. The dashboard renders them as Markdown; raw HTML is not rendered.

## Language of ATCT records

Write ATCT records in the language of the `ui_locale` (`en` or `ja`) that
`atct_role` returns. After the context is reset (compaction or clear), read it
again with `atct_role`.

**Write in the configured language:** a goal's `content`, `spec`, `plan`,
`result_summary`, `work_done`, `now_possible`, `next_steps`, `surprises`,
`needs_review` and `how_to_verify`; a task's title and description; a decision's
question and options (`label`, `description`, `consequence`); the text of every
handoff request, report, review, complete, reject and recovery.

**Keep in English:** tool names, parameters, identifiers and keys (`handoff_id`,
`declare_key`, `idempotency_key`), enum values, code, commands, file paths, branch
names, commit messages, and quoted errors or logs.

The daemon does not check the language; this is a convention to follow.

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
   stdout). Do not re-arm the `--once` Bash. While the only thing you wait for
   is an answer (a plan review, a goal review, or an executor starting or
   working), wait on `--once` rather than the Monitor: the PreToolUse gate
   refuses the Monitor form then.
4. **Keep one.** Hold exactly one watch per token; the one started later stops
   the earlier one.
5. **Stop** a watch with `TaskStop` and the task id of the Monitor or the
   background Bash (see `atct:stop`). If the task id is unknown, say so and do
   not call `TaskStop`; never guess one.
6. **Waiting for an answer.** When the only thing you hold is an answer you are
   waiting for (a plan review, a goal review after
   `atct_goal_handoff_review_request`, or an executor starting or working), stop
   the Monitor with `TaskStop`, then wait on a `--once` background Bash (step 2's
   form), which never expires, so no 30-minute notice wakes it for nothing. When
   it ends, follow step 3. While you wait, arm no other wakeup (`ScheduleWakeup`,
   cron): the `--once` Bash's end already wakes the session, and an extra wakeup
   only adds notices that change nothing. In a `/loop`, end the loop with
   `ScheduleWakeup` and `stop: true` instead. Keep the Monitor while notifications
   are many and you hold work of your own.

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

Human decisions go through ATCT only: the commander files them with
`atct_decision_ask`, with concrete options and a recommended default, never a
conversational question. Apply an answer with `atct_decision_poll` before
continuing dependent work; withdraw a decision that is no longer relevant. A
subcommander cannot file one (see `### A subcommander's decision request`).

A goal with an open decision, of any kind, is refused both `atct_goal_handoff_review_request`
and `atct_goal_review_request`; the refusal names each decision. Answer or withdraw
them first. An action that only matters after the merge (a production deploy) is not a
decision to file: write it in `needs_review` and `next_goal_ids` of the completion report,
and the commander passes it to the human at approval.

Ask the human only when one of these holds:

- (a) the operation cannot be undone or is destructive; ask immediately before it
- (b) it needs something only the human can do: a permission, a credential, an
  external account, or approval of a cost the human has not already approved (a
  new spend, or one beyond the approved amount)
- (c) it changes an outcome the human asked for or approved

Everything else is yours to decide: a reversible, non-destructive choice of means
that leaves the approved outcome unchanged. Decide it, carry on, and
do not call `atct_decision_ask`. Record what you chose and why in `surprises` /
`needs_review` of the completion report (a worker with no completion report
records it in its review request report).

Example: the human approved a trial of 10 stocks. Launching the 10 in one call or
in three calls of 4, 4 and 2 yields the same approved outcome, so the number of
launches is not a change to the approved scope. Its small API cost was part of the
approved trial, so it is not a new cost to ask about either. Do not ask.

### A subcommander's decision request

`atct_decision_ask` is refused to a subcommander. When (a), (b) or (c) holds, or
a fact spans another goal, append one entry of kind `review_requested` per
decision to your own goal handoff with `atct_goal_handoff_entry_append`:

```
人間への決定の依頼
質問: <one sentence>
選択肢:
- <label>: <description> / 結果: <consequence>
推奨: <label> / 理由: <why>
```

An action that only matters after the merge is not asked this way (see above): it
goes in `needs_review` and `next_goal_ids`. The commander files the decision,
the human answers in the commander's chat, and the commander replies on the same
handoff with a `review_received` entry whose `in_reply_to_id` is your request
entry's id and whose body is `decision <id>: <label> <answer_text>`. Your watch
delivers it as `handoff_entry_added`; read it with
`atct_goal_handoff_entry_history`. After asking, wait on a `--once` background
Bash (`## Watch` step 6). Before `atct_goal_handoff_review_request`, each
decision you asked for is answered, or the commander is asked to withdraw it.

On 2026-10-20, in stock-data goal 437, decision 1180 (a `post_merge` decision a
subcommander had filed itself) could not be recorded from the commander's chat,
and the goal stood still.

### Recording an answer from the chat

A human's answer given in the chat is recorded with `atct_decision_answer`, not
by asking the human to repeat it on the dashboard. Pass `decision_id`, the
`answer` (an option label; for a goal review, `approve` or `reject`), an optional
`answer_text` (a supplement, or the reason for a goal-review reject), and
`human_quote`: the verbatim text the human typed in this chat that gives the
answer. The commander that filed the decision records it. The commander also
records the answer to an open decision a subcommander filed before this rule;
the subcommander applies it with `atct_decision_poll`.

For a goal review, `human_quote` must be one of these whole lines, naming the
decision id the dashboard shows: `approve #<decision_id>` or `承認 #<decision_id>`
for approve, `reject #<decision_id>` or `却下 #<decision_id>` for reject. Case is
ignored, and the space before `#` is optional. Ask the human to reply with that
line. When the human answers a goal review in ordinary words (「よい」「承認します」),
do not record it: ask for the fixed line instead. Such a sentence is refused, and
so is a quote cut out of it.

The daemon checks the quote against the harness's own transcript, not a copy
made by a hook, so no hook setup is needed in Codex. The transcript is read
from the Claude Code projects folder under `CLAUDE_CONFIG_DIR` or the Codex
sessions folder under `CODEX_HOME`. When the variable is set, only that folder
is read. The home folder (`~/.claude` or `~/.codex`) is read only when the
variable is empty. The daemon must see the same values as the harness, or a
genuine answer is refused too. The quote must be one whole line of a
message the human typed after the decision was asked, and it must match that
line exactly. A summary, a paraphrase, part of a line, or a quote from earlier
in the conversation is refused. One human message answers only one decision.
Never write a `human_quote` the human did not type.

Messages from agents do not count, even when they arrive as a prompt: input that
`atct codex monitor` sent (its `client_id` starts with `atct:`) and input Codex
generated itself (no `client_id`) are not human answers; do not record them.
Only the session that asked the decision can record it, except a goal review:
only the commander holding the project now can record that, even when another
session asked it. A decision with no asking session is also for the commander of
the same project. If the
human's answer is unclear, do not record it: ask again. If the record is
refused, ask the human to answer on the dashboard and do not retry with other
quotes.

## Archived projects

`atct project archive <name|id>` hides a project without deleting anything: its
goals, tasks and decisions stay, but they leave the dashboard lists, the waiting
answers, every role's Stop hook, the watch, wakeups and the automatic default of
an expired decision. Reading by id (a goal or task page) still works. Changes
are refused for an archived project: creating or claiming a goal, handoffs,
task changes, `atct_decision_ask` / `atct_decision_poll` / withdraw, and plan or
review requests. The error names the project and the way back,
`atct project unarchive <name>` (or the dashboard's Projects section).
`project.release` still works. Unarchiving restores everything; defaults that
expired meanwhile are applied at the next maintenance.

Only the commander archives, and only when the human asks, with
`atct project archive <name>`. A subcommander or executor never does. There is
no MCP tool for it, so no agent can archive on its own.

If an ATCT call is refused because the project is archived, stop the work and
return it to your delegator. Unarchiving is the human's decision.

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
| something found that is another goal | a `review_requested` entry on the goal handoff, for the commander to file as a decision |
| what was left undone | `next_goal_ids`, the ids of the goals to proceed with next |
| the goal is ready for commander review | `atct_goal_handoff_review_request`, the one message |

`atct_goal_handoff_review_request` is terminal: call it once, and only after
every declared task is accepted and committed and the goal is ready for
commander review. Work that is unfinished or blocked stays open and goes to a decision request
(`### A subcommander's decision request`) with concrete options and the
consequence of each; it is never reported through a goal-handoff review request.
`atct_goal_review_request` applies only to an active goal after the commander
has received the goal-handoff review.

A subcommander that stops working sends nothing at all, and the old habit caught
that only because a delegator noticed a quiet terminal. The record catches it
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
