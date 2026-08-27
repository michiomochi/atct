---
name: atct
description: Use when working on a goal that a human is tracking - declaring the tasks you plan to do, claiming one before you start, and asking the human for a decision instead of guessing. Also use when you are about to finish and need approval.
---

# ATCT

ATCT records what you are working on and routes your questions to a human's
inbox. Registering tools is not enough; the value comes from calling them at
the right moments.

## Roles

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

## Declare before you work

Call `atct_task_declare` with the tasks you intend to do, before doing them.
Pass a stable `idempotency_key` for the batch. Re-declaring the same batch does
not create duplicates, so it is safe after a retry or a context compaction.

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

## Claim before you start

For self-directed work—when you find a task yourself—call `atct_task_claim`
before working on it. Exactly one run wins a claim. If the claim fails the task
is already owned, so pick another one rather than working on it anyway.

A delegated worker owns the task it was given. Receive it with
`atct_handoff_receive`, not `atct_task_claim`; receipt is exclusive, so a handoff
cannot be received twice.

Release a task by setting it back to `todo` with `atct_task_update`. There is
no separate release tool.

## One worktree per goal

ATCT uses `script/worktree-setup.sh <goal-id>` as the canonical way to prepare
a worktree. Do not use a session-scoped native worktree tool such as
`EnterWorktree`.

- The script derives the location and branch from the goal id: `.worktrees/<goal8>`
  and `wt/goal-<goal8>`. A second person working on the same goal enters the
  same tree. A native tool names worktrees per session, so it creates one per
  agent instead of one per goal.
- The script borrows `web/node_modules` from the primary checkout through a
  symlink and copies `web/dist`. Neither the native tool nor the regular Git
  worktree mechanism knows about this frontend setup.
- The script runs only from the primary checkout; when run inside a worktree it
  exits with status 2.

`script/worktree-setup.sh` is the replacement for Steps 1a and 1b of
`superpowers:using-git-worktrees`, and it also covers the frontend part of Step
2 (Project Setup). The `.worktrees/` directory is already in `.gitignore`, so
the skill's safety check is satisfied. Follow the reference skill instead of
copying its setup procedure here.

Usually nobody runs the script by hand. A worktree is prepared before an agent
starts, so the skill's Step 0 reports an already isolated workspace and does
not proceed to Step 1.

- `commander`: Prepare the worktree before waking anyone for the goal; use the
  primary checkout for your own work.
- `subcommander`: Work in the worktree for your own goal; do not create
  worktrees for other goals.
- `executor`: Work in the worktree for the handed-off goal; it does not create
  one itself.

### When the primary checkout is right

The primary checkout is appropriate in these cases:

- `commander` reviews landed changes, publishes a release, resolves conflicts
  between worktrees, or cleans up a worktree after the goal closes.
- Running `script/worktree-setup.sh` itself, because it does not run inside a
  worktree.
- Working on a goal that changes this rule: the rule cannot apply to the change
  until it lands. This rule is being written in the primary checkout for that
  reason.

### What a worktree does not separate

A worktree does not make every resource independent:

- `~/.atct/atct.db` is one shared database for all worktrees; goals, tasks,
  claims, and decisions are shared. Avoiding two people touching the same file
  still depends on declaring before you work.
- The daemon is one per machine, not one per worktree.
- `web/node_modules` is a symlink to the primary checkout. Running `pnpm
  install` in a worktree changes the primary checkout's dependencies.
- Git objects and refs live in one common directory. The same branch can be
  checked out in only one worktree at a time.
- If two worktrees edit the same file, the conflict does not disappear; it is
  merely moved to the merge.
- `GOCACHE` is shared by default across all worktrees. Sharing is harmless
  because the cache is content-addressed, but point it outside the repository:
  if it points inside, generated files can fill `git status`.

## Commit safely

When committing the goal's work, name the paths explicitly; never use `git add -A`.
If another worker's uncommitted changes share a file, stage only your hunks with `git apply --cached`.

## Delegate a task

When handing a task to another worker, keep the contract independent of how
that worker is started:

1. Hold the parent, not the task. The delegator does not hold the task; it must
   have received the handoff for that task's goal before handing it off. Claiming
   the task first always causes the handoff request to be refused because the
   claim already writes an open handoff.

2. Record the handoff before waking the worker.
   The delegator must call `atct_handoff_request` with a unique handoff ID and
   the task ID. Wait for the request to succeed before waking the
   worker; this creates the record needed to receive and complete the handoff.
3. Wake the worker through the environment. A terminal multiplexer can host a
   working pane, or a sub-agent can perform the work. ATCT does not prescribe
   how the worker is started or how the role is transmitted.
4. Put these exact instructions at the very beginning of the request:

   > First call `atct_session_identify` with a stable session key that remains unchanged for this session and identifies only you. Your agent name is suitable. Do this before any other atct call.
   >
   > Then record receipt of the handoff by calling `atct_handoff_receive` with only
   > the `task_id` provided in this request. Do this before starting work. Do not
   > pass a handoff ID or session; ATCT supplies them.
   >
   > Then invoke the `atct_role` MCP tool with `expected_role` set to one of
   > `commander`, `subcommander`, or `executor`. If it reports `matches: false`,
   > do not start work; return the task.
   >
   > When the work is complete, record completion by calling `atct_handoff_complete`
   > with the `task_id` provided in this request and a `complete_report`. The
   > `complete_report` must say what was done, what was verified, and paths changed.
   >
   > Only then close the task, by calling `atct_task_update` with the `task_id`
   > provided in this request and `status` set to `done`. This order is required:
   > a terminal status closes any open task handoff and replaces its
   > `complete_report`, so the report has to be recorded first. A task nobody
   > closed still reads as unstarted, so do not stop after reporting.

   Report completion before closing the task. Calling `atct_task_update` first
   closes the handoff and the completion report is lost: the record keeps a
   released-the-lock placeholder with no reporter, and the executor is left with
   nothing to write it back through. This reproduced on 2026-08-27 with two
   executors, one on Claude and one on Codex.

   Name what the worker may call, and name what it may not. A blanket ban carries
   no grain, so it is overturned without grain too: an executor that decides atct
   calls are allowed after all reaches the goal scope in the same step.

   An executor may call only these atct tools:
   `atct_session_identify`, `atct_handoff_receive`, `atct_role`, `atct_task_update`, `atct_handoff_complete`.
   Each of them is confined to the `task_id` the executor was given.

   An executor must not call `atct_goal_handoff_complete`, `atct_goal_handoff_receive`,
   `atct_goal_handoff_request`, `atct_goal_claim`, `atct_goal_release`,
   `atct_goal_complete`, `atct_goal_update_content`, `atct_project_claim`,
   `atct_project_release`, `atct_task_claim`, `atct_handoff_request`,
   `atct_task_declare`, or `atct_decision_ask`. Spell the names out; "anything not
   listed above" is not read as a prohibition. In a 2026-08-27 measurement, an
   executor closed a subcommander's goal handoff without knowing it was forbidden.

   An executor that reaches an irreversible operation returns it to the delegator.
   Apply the test in `## Act on reversible choices, ask about irreversible ones`:
   can the human get the previous state back? Rewriting history, discarding
   uncommitted work, deleting a file or directory, and publishing off this machine
   all fail it. The executor does not perform the operation and does not carry the
   judgement itself; it stops there and hands it back to whoever sent the request.
   `atct_decision_ask` is the delegator's call, not the executor's. A design
   decision travels the same way, which is what `does not: make design decisions`
   in `## Roles` means in practice.

5. Keep one worker per task. Return a correction, review fix, follow-up
   question, or clarification for the same task to the same worker. Start a
   new worker for a different task. When the task is done, end that worker.
   A correction or review fix remains the same task because it is a delta
   against the immediately preceding implementation; send it back to the same
   worker. What breaks when you batch is the record, not the context. A handoff
   points to one task. If three tasks are sent in one message, only one handoff
   is created; the other two have no owner, receipt, or completion, so the
   dashboard says nobody started them. In a 2026-08-24 measurement, sending
   three tasks to executor-33 in one message broke the records for two of the
   three. Task count and compression count are not correlated: in that same
   measurement, the three-task pane compressed twice while the one-task pane
   compressed seven times.

   For a follow-up to the same worker, recreate the `atct_handoff_request` with
   a new `handoff_id`; a closed `handoff_id` cannot be reused. The new handoff
   does not mean a different worker; it gives the same worker a new ID.

The worker must perform both instructions itself before doing any work. The
delegator must not run either instruction on the worker's behalf or treat a
worker name, pane title, or launch context as proof of the role. If the role
check reports a mismatch, the worker returns the task without touching it.

### Two-layer delegation

Delegating a task requires a received goal handoff, not a project claim. For two-layer delegation, the commander calls `atct_goal_claim` to create a goal handoff addressed to itself. The project claim is checked first by `session.role` in `internal/daemon/handler.go`, so the role remains `commander`. Then the commander calls `atct_handoff_request` to delegate each task.

## Delegate a goal

When handing a goal to a subcommander, keep the contract independent of how
that subcommander is started:

1. Hold the parent, not the goal. The delegator does not hold the goal; it must
   have a project claim before handing it off. Claiming the goal first always
   causes the handoff request to be refused because the claim already writes an
   open handoff.
2. Record the handoff before waking the subcommander.
   The delegator must call `atct_goal_handoff_request` with a unique `handoff_id`
   and the `goal_id`. The request takes only `handoff_id` and `goal_id`;
   do not pass `requested_by`; ATCT supplies it. Wait for the request to succeed
   before waking the subcommander; this creates the record needed to receive and
   complete the handoff.
3. Wake the subcommander through the environment. ATCT does not prescribe how
   the subcommander is started or how the role is transmitted.
   Name in the request every adjacent goal that touches the same files and say
   which side owns what. The delegator is the only party that can see both
   goals, and a boundary left unstated becomes a question the subcommander
   cannot answer for itself.
4. Put these exact instructions at the very beginning of the request:

   > First call `atct_session_identify` with a stable session key that remains unchanged for this session and identifies only you. Your agent name is suitable. Do this before any other atct call.
   >
   > Then record receipt of the goal handoff by calling
   > `atct_goal_handoff_receive` with only the `goal_id` provided in this request.
   > Do this before starting work. Do not pass a handoff ID or session; ATCT
   > supplies them.
   >
   > Then invoke the `atct_role` MCP tool with `expected_role` set to
   > `subcommander`. If it reports `matches: false`, do not start work; return
   > the goal.
   >
   > Then attach `atct watch -goal <goal_id>` to a background stream the way
   > your harness watches one, passing only the `goal_id` provided in this
   > request. The detections for this goal, and the answers to the decisions
   > you raise for it, then reach you without the delegator relaying them.
   > Pass no other goal; a subcommander must not inspect other goals. This
   > step applies only in Claude Code; Codex has no Monitor, so a Codex reader
   > skips it.
   >
   > Decide this goal's design yourself. Do not bring the delegator a design
   > question, a progress note, a receipt acknowledgement, a discovery, or a
   > reading of this goal's code. Send the delegator nothing until the completion
   > report. What you would have said goes into the record instead: a task for
   > work in flight, `surprises` and `needs_review` for what you found,
   > `next_steps` for what you left, and `atct_decision_ask` for anything that
   > needs the human.
   >
   > A fact that spans another goal is not an exception. Raise it with
   > `atct_decision_ask`; the answer reaches you through your own watch, without
   > passing through the delegator.
   >
   > When the work is complete, record completion by calling
   > `atct_goal_handoff_complete` with the `goal_id` provided in this request
   > and a `complete_report`. The `complete_report` must say what was done,
   > what was verified, and paths changed.

   The order matters: the role is derived from a received, uncompleted goal
   handoff, so checking it before receipt always returns `matches: false`.

5. Keep one subcommander per goal. A subcommander may wake executors for its
   goal, but must not inspect or manage other goals, create another
   subcommander, or release the goal.
   A subcommander must not call `atct_goal_release`; releasing the goal is the
   commander's job.
   A subcommander must not claim the project. Claiming the project changes its
   role to commander.

6. Stay out until the completion report. After waking the subcommander, the
   delegator sends it nothing and answers nothing about the goal's design.
   What the delegator reads instead are this project's ATCT detections, which
   arrive from `atct watch` rather than from the subcommander: a goal with no
   commits, a goal with no declared tasks, a claim nobody delegated, a handoff
   nobody received. Those are what a stalled subcommander looks like from
   outside, and they arrive whether or not it speaks. Review the goal when
   `atct_goal_handoff_complete` lands; that report is the entry point.

### Session keys

The caller owns the session key. It must remain unchanged for the life of the
session and identify only that caller; the caller's agent name is suitable. If
a reconnect causes the role to appear wrong, call `atct_session_identify` again
with the same key to return to the original session row.

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
| progress on the work | tasks: `atct_task_declare`, then `done` as each one lands |
| the design and why | a spec committed with the goal's work, and `work_done` |
| something found inside this goal | `surprises` and `needs_review` |
| something found that is another goal | `atct_decision_ask`, addressed to the human |
| what was left undone | `next_steps` |
| the goal is finished | `atct_goal_handoff_complete`, the one message |

A subcommander that stops working sends nothing at all, and the old habit caught
that only because a delegator noticed a quiet pane. The record catches it
instead: a goal with tasks and no commits, or a closed handoff with nothing
committed, each raises a detection on the delegator's watch. On 2026-08-27 goal
172 stalled with three tasks still `todo` and eight files uncommitted, and goal
144 closed its handoff with no commits and four tasks still `todo`. Both
detections had already fired; nobody had been told to read them.

## Fill in a report on a handoff that is already closed

Only a subcommander or commander uses this repair path when a closed handoff
has no report. It is not part of normal executor completion. Call
`atct_handoff_report_amend` with the specific `handoff_id`, its `task_id`, and
a non-empty `complete_report`; for a goal handoff call
`atct_goal_handoff_report_amend` with `handoff_id`, `goal_id`, and
`complete_report`. The repair does not change the recorded completion time.

## Recover when your role comes back wrong

If `atct_role` returns `executor` while you still hold work that should be yours, stop working and read this section.

A role is derived from the received, uncompleted goal handoff an agent holds, so
anything that closes that handoff takes the role with it.
Closing a subcommander's goal handoff drops that subcommander to `executor`.
Nothing announces the drop: the subcommander learns of it only the next time it
calls `atct_role`, and until then the dashboard shows the goal as completed while
its work is still uncommitted.

The first recovery path is `atct_session_identify`; follow `### Session keys` first.

Only if the session key does not restore your role, recover each layer as follows:

- project: `atct_project_release` → `atct_project_claim`
- goal: `atct_goal_handoff_complete` → `atct_goal_handoff_request` (the commander must issue the handoff again)
- task: `atct_handoff_complete` (with `task_id` and `complete_report`) → `atct_task_claim`

A subcommander cannot restore its own goal; ask the commander to issue the goal handoff again because reissuing it requires a project claim. This is a procedure, not a repair; it becomes unnecessary once the issue is fixed.
For background, see `doc/specs/2026-08-25-session-id-swap.md`.

## Close a task the moment it is finished

Call `atct_task_update` with `done` as soon as the work lands, before you claim
anything else. Claiming is only half of the pair; a task nobody closed still
reads as unstarted.

This matters most when the run that did the work is not the run that holds the
claim — an orchestrator delegating to another agent, for example. The delegate
finishes, the orchestrator moves on, and nothing writes the result back. **Then
the dashboard says the work has not begun, and the human plans around that.**
If you delegated, close the task when the delegate reports, not later.

## Keep going

An active goal is permission to work, not a request for a plan. When
`atct_goal_list` or the session context shows one, declare the tasks and start.
Do not wait for the human to approve the plan first: they set the goal, and that
was the approval.

Carry each task through to a commit. The human's attention is for decisions and
for the final approval, not for granting permission at every step.

Finishing a task is not a checkpoint. Claim the next one and keep going. When a
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

`atct_goal_complete` takes six fields, and the database rejects a completion
with any of them empty. **Where nothing applies, say so** — writing "none" is
the point, because it separates "there was nothing" from "I did not look."

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

## Act on reversible choices, ask about irreversible ones

Classify the decision before asking:

- For a reversible choice, execute the recommendation first, then record it with
  `atct_decision_ask` using `wait_ms=0` and `default_after_ms=0`. Do not stop to
  wait for an answer. If the human chooses differently, create a new decision
  to correct it; never overturn a settled decision.
- For an irreversible choice, ask before acting: omit `default_option` and
  `default_after_ms`, and wait for the human's approval. Use `wait_ms=0` to park
  it while doing unrelated work.

As of 2026-08-25, a decision used as a record sets `default_option` and
`default_after_ms=0`; it is applied immediately and does not block `done`. A
human-waiting question omits both; it blocks `done` until answered. This is the
form for an irreversible choice.

The test is whether the human can get the previous state back. A commit is
undoable. A force push over work that exists nowhere else is not.

**A deadline on an irreversible choice is not a safeguard with a timer on it; it
is the thing happening anyway, with a delay.** That is why those get no default
at all. And if every remaining task depends on that one answer, say so and stop —
that is a real block, and it is worth the human knowing about.

## Apply what you were told

Answers reach you through `atct_decision_poll`. Polling marks the decision
applied, which is how the human can tell their answer landed rather than
hanging. Poll before continuing work that depended on the question.

If a question stopped being relevant, call `atct_decision_withdraw` rather than
leaving it open.

## Finishing

A task cannot become `done` while a decision on it is open. Answer it or
withdraw it first.

Call `atct_goal_complete` when the work is done. It creates a completion
decision for the human to approve or reject; approval closes the goal, and
rejection returns a reason for you to act on.
