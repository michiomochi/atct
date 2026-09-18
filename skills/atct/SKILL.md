---
name: atct
description: Use when working on a goal that a human is tracking - declaring the tasks you plan to do, claiming one before you start, and asking the human for a decision instead of guessing. Also use when you are about to finish and need approval.
---

# ATCT

ATCT records what you are working on and routes your questions to a human's
inbox. Registering tools is not enough; the value comes from calling them at
the right moments.

Every ATCT response that continues the flow carries `next_step`, naming the
operation that follows it. Read that field and do what it says; it is derived
from the same chart and is always current.

Read `doc/execution-flow.md` only when `next_step` does not answer the
question: to see the whole chart at once, or to settle who owns a step.

## Roles

### Development escalation

Only `atct:dev:start` may call `atct_development_start`; this marks the current
agent session as development mode. An executor escalates to its subcommander,
and a subcommander escalates to its commander. In that mode only, a
subcommander may resolve executor work in its Goal and a commander may resolve
subcommander work in its Project; a commander never performs executor work.
After resolution, create a remediation Goal with the cause, resolution, and
verification, then return to the normal spec → plan → review flow.

Role values for `expected_role`: `commander`, `subcommander`, `executor`.

The daemon derives the role in this order:

- It checks the project claim first; if the agent holds one, the role is `commander`.
- `subcommander`: the agent has a received, uncompleted goal handoff and no project claim.
- If neither condition applies, the role is `executor`.

| Layer | Does | Does not |
|---|---|---|
| `commander` | triage incoming work / split goals / prepare a working area / review landed changes / publish / resolve conflicts / clean up | design the goal / implement the goal / edit executor deliverables |
| `subcommander` | design the goal / delegate the goal's work / review implementation / report completion for the goal / issue decisions to the human / commit the goal's work / close a task its worker cannot | inspect or manage other goals / publish / create another subcommander / claim the project |
| `executor` | implement / test / close the task it was given | make design decisions / re-delegate / commit / write internal version-control details |
## Role-specific skills

After identifying the session, call `atct_role` before role-specific work. If
the result does not match, do not work: return the handoff or let the caller
recover its claim. If it matches, invoke exactly one skill:

- `commander` → `atct:commander`
- `subcommander` → `atct:subcommander`
- `executor` → `atct:executor`

This skill is the SSOT for role derivation, claims, handoffs, decisions,
irreversible operations, and completion records. Role skills own only their
role's operations and must not restate or override these rules.

Operations only one role performs live in that role's skill, so no role loads
another's. Moved out of this skill on 2026-09-19:

| Section | Now in |
| --- | --- |
| `## One worktree per goal`, `## One space per goal`, `## Delegate a goal` | `atct:commander` |
| `## Delegate a task`, `## Close a task the moment it is finished` | `atct:subcommander` |
## Declare before you work

1. Call `atct_task_create` with the tasks you intend to do. Creating them is
   how you declare them. Pass a stable `idempotency_key` for the batch. Sending
   the same batch again does not create duplicates, so it is safe after a retry
   or a context compaction.
2. Then start the work, and only the work you declared.

**Out of order:** Work done before it is declared never reaches the dashboard.
The human reads a goal with no tasks on it and plans around work that is already
in flight, and because nothing recorded what you intended, a wrong assumption in
the task cannot be corrected before you have acted on it.

The title says what to do. The description says the conditions for judging it
done and the assumptions that must hold. Paraphrasing the title in the
description is equivalent to not writing a description.

On 2026-08-20, a task to verify whether a decision is rolled back to open on
approval failure was withdrawn because its assumption was wrong. If that
assumption had been written in the description, a human could have corrected
it when the task was declared.
## Fix a declared task

- After declaring a task, use `atct_task_update_content` to fix its `title`
  or `description`.
- Only `todo` and `doing` tasks can be fixed. `done` and `dropped` tasks are
  rejected because changing them would change the basis for a completion report
  after the fact.
- Re-declaring with the same `idempotency_key` does not update the task;
  re-declaration is not a way to fix it.
## Receive before you start

Every implementation task is delegated.

1. The subcommander requests the task handoff, and the executor receives it with
   `atct_task_handoff_receive` before touching the work. Receipt is exclusive,
   so a handoff cannot be received twice.
2. The executor requests review when the work is ready; the subcommander accepts
   it with `atct_task_handoff_complete`, which closes the task.

**Out of order:** Working before receipt lets a second executor receive the same
task. Stopping before requesting review leaves the task open even when the work
landed.
## Commit safely

When committing the goal's work, name the paths explicitly; never use `git add -A`.
If another worker's uncommitted changes share a file, stage only your hunks with `git apply --cached`.
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
| the design and why | a spec committed with the goal's work, and `work_done` |
| something found inside this goal | `surprises` and `needs_review` |
| something found that is another goal | `atct_decision_ask`, addressed to the human |
| what was left undone | `next_steps` |
| the goal is ready for commander review | `atct_goal_handoff_review_request`, the one message |

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

1. Confirm the handoff is already closed and carries no report. A handoff that is
   still open belongs to the normal completion path, not to this one.
2. Call `atct_task_handoff_report_amend` with the specific `handoff_id`, its
   `task_id`, and a non-empty `complete_report`; for a goal handoff call
   `atct_goal_handoff_report_amend` with `handoff_id`, `goal_id`, and
   `complete_report`. The repair does not change the recorded completion time.

**Out of order:** Amending first, without checking, writes the report through the
repair tool while the normal one was still available. The worker that owed
`atct_task_handoff_complete` never learns it owed anything, the amended report hides
the missing completion instead of exposing it, and the single normal path stops
being the path anybody follows.
## Recover when your role comes back wrong

If `atct_role` returns `executor` while you still hold work that should be yours, stop working and read this section.

A role is derived from the received, uncompleted goal handoff an agent holds, so
anything that closes that handoff takes the role with it.
Closing a subcommander's goal handoff drops that subcommander to `executor`.
Nothing announces the drop: the subcommander learns of it only the next time it
calls `atct_role`, and until then the dashboard shows the goal as completed while
its work is still uncommitted.

1. Stop working. Whatever you do while the role is wrong is recorded against a
   layer you do not hold.
2. The first recovery path is `atct_session_identify`; follow `### Session keys` first.
3. Only if the session key does not restore your role, recover each layer as follows:

   - project: `atct_project_release` → `atct_project_claim`
   - goal: `atct_goal_handoff_complete` → `atct_goal_handoff_request` (legacy/out-of-order recovery; the commander must issue the handoff again)
   - task: after the stale lock is released, have the subcommander request a fresh `atct_task_handoff_request`

**Out of order:** Reaching for the layer repair before the session key closes a
handoff that did not need closing, and closing it is exactly what drops the role.
A reconnect the session key alone would have fixed becomes a real loss: the goal
handoff is gone, the subcommander cannot reissue it, and the goal waits on the
commander. Continuing to work before either step spends the whole detour on
changes nobody can attribute.

There are four triggers for a role to come back wrong, and the handoff state differs between them:

| Trigger | Handoff |
|---|---|
| A daemon restart (regardless of whether the version changed) | remains open; only the session record was lost |
| The correspondence between the transport and the session key is lost (cause not measured) | remains open; the goal handoff and the session row in the database are still alive |
| Rejection of a completion report | remains closed; it is automatically reissued to the original recipient |
| An out-of-order `atct_goal_handoff_complete` call or an executor calling it by mistake | remains closed; it is not reissued |

Rejection is automatic, so the goal step above that asks the commander to reissue the handoff is needed only for the last trigger.
For background, see `doc/specs/2026-08-25-session-id-swap.md` and `doc/specs/2026-08-28-reissuing-the-goal-handoff-on-rejection.md`.
## Keep going

An active goal is permission to coordinate work, not a request for a plan. When
`atct_goal_list` or the session context shows one, declare the tasks and delegate them.
Do not wait for the human to approve the plan first: they set the goal, and that
was the approval.

Carry each task through to a commit. The human's attention is for decisions and
for the final approval, not for granting permission at every step.

Finishing a task is not a checkpoint. Review it, delegate the next one, and keep going. When a
goal has no unclaimed tasks left, move to another active goal instead of
reporting back. Announcing what you will do next and then stopping to be told to
do it wastes the turn that ends the sentence.

Stop before anything that cannot be undone:

- rewriting history: force push, `rebase`, `reset`, `amend`
- discarding uncommitted work: `restore`, `checkout`, `stash`
- deleting files or directories
- publishing off the machine: deploying, sending data to an external service

For those, use `atct_decision_ask` and park.

The test is whether the human can get the previous state back. A commit is
undoable. A force push over work that exists nowhere else is not.
## Ask instead of guessing

Call `atct_decision_ask` when a choice would change the shape of the work and
you cannot settle it from the code. Supply options with a label, a description,
and the consequence of choosing it. `wait_ms` blocks for an answer and parks
when none arrives, so asking does not force you to stall.

Do not ask about things you can determine yourself. An inbox full of trivia
stops being read.
## Ask here, not in conversation

`atct_decision_ask` is the only place a question belongs. Saying "let me know how
you want to proceed" in the conversation and then waiting is not asking — it is
stopping. That sentence never reaches the dashboard, leaves no record, carries no
default, and holds every other task hostage until someone happens to reply.

**Bringing the options is part of the question.** "This needs a decision about X"
is not a decision to make; it is work you have not finished. Find out what the
real alternatives are, what each one costs, and put them in the call. If you
cannot yet name two concrete options, you are not ready to ask — go find out.
## Write so the answer takes ten seconds

**Open with the choice, not the history.** The human is deciding, not reviewing
your investigation. Put what you need decided in the first sentence, then the
options, then — only if it changes the answer — how you got here.

A question that opens "I implemented X but it turns out Y because Z was created
by W and never reaches V…" makes the reader assemble the decision themselves.
The one that opens "Which of these three should I use for X?" does not.

**Say which one you would pick and why, in one line.** `default_option` already
carries that, but name it in the text too. "I would take the first: it keeps
the plugin-only install working."

**Options carry consequences, not descriptions.** "Use approach A" tells the
reader nothing. "Use approach A — users install nothing extra, but we keep
maintaining the apply path ourselves" lets them choose.

Skip the parts that do not change the decision: file paths, function names,
which commit introduced it. If the reader would decide the same way without a
sentence, cut it.

**This holds for anyone waiting on you, not just the dashboard.** When something
is blocked on your answer — a person, another agent — reply with the answer
first. Your account of how you got it wrong belongs after, or nowhere. Burying a
one-word decision inside a retrospective makes them ask again.
## Report completion in six parts

1. Commit the goal's work.
2. Close every task the goal declared.
3. Fill in all six fields and call `atct_goal_complete`. What a caller supplies
   is a separate question from what the column stores: all six columns hold
   non-empty text once the goal is `done`, so even "there was nothing here"
   arrives as a written value.

**Out of order:** Report first and the goal goes to the human for approval with
zero commits and its tasks still `todo`. The dashboard says "completed" about work
that is not in the repository, `how_to_verify` points at changes the approver
cannot find, and the tasks stay open behind a goal that is already closed. Goal
144 closed with no commits and four tasks still `todo` on 2026-08-27.

`atct_goal_complete` takes six fields, and the database rejects a completion
with any of them empty. **For the five text fields — `work_done`,
`now_possible`, `how_to_verify`, `surprises`, `needs_review` — where nothing
applies, say so** — writing "none" is the point, because it separates "there was
nothing" from "I did not look."

| Field | What goes in it |
|---|---|
| `work_done` | What you changed |
| `now_possible` | What the human can do that they could not before |
| `how_to_verify` | What to look at to confirm it |
| `surprises` | What turned out differently than expected |
| `needs_review` | What you want them to look at closely |
| `next_steps` | What you left for later, and why |

The approver reads `how_to_verify` and `needs_review` first: these fields say
what to check and what still needs confirmation. Keep `work_done` concise so it
does not bury them.

**`work_done` is the only field about you.** The other five are about them —
what they gained, what to check, what to worry about, what is still open. A
report where all six read like a changelog has answered one question six times.

**`surprises` is where a report earns its keep.** It is the field a writer most
wants to skip and a reviewer most needs. If your change touched the human's data
in a way they did not ask for, that belongs here, not buried in `work_done`.

Each field has a length limit. **A report nobody finishes reading cannot be
approved**, and six short fields beat one long one.
## Name goals after the symptom, not the mechanism

"Attach unattached decisions to the goal detail response" describes the fix.
**"Decisions waiting on you do not show up on the goal page" describes what the
human saw.** They set the goal from the symptom; they will look for it by the
same words.
## Ask the human only before irreversible operations

**Human judgment is requested only immediately before an irreversible or destructive operation.**
## Apply what you were told

Answers reach you through `atct_decision_poll`.

1. Poll before continuing work that depended on the question. Polling marks the
   decision applied, which is how the human can tell their answer landed rather
   than hanging.
2. Then continue that work, on the answer you just read.

If a question stopped being relevant, call `atct_decision_withdraw` rather than
leaving it open.

**Out of order:** Continue first and you are acting on a guess while the answer
sits unread — and because nothing marked it applied, the human's side still shows
the question hanging, so they cannot tell whether their answer reached you or
whether you are still blocked on it.
## Finishing

1. Answer or withdraw every decision still open on the goal's tasks. A task
   cannot become `done` while a decision on it is open.
2. Set those tasks to `done`.
3. Call `atct_goal_complete` when the work is done. It creates a completion
   decision for the human to approve or reject; approval closes the goal, and
   rejection returns a reason for you to act on.

**Out of order:** Go for `done` with a decision still open and the update is
refused, so the goal never reaches a state `atct_goal_complete` can describe
truthfully. You find that out at the last step, with a completion report already
written, and have to go back for the decision you left open.
