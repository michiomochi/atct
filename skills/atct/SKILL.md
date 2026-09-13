---
name: atct
description: Use when working on a goal that a human is tracking - declaring the tasks you plan to do, claiming one before you start, and asking the human for a decision instead of guessing. Also use when you are about to finish and need approval.
---

# ATCT

ATCT records what you are working on and routes your questions to a human's
inbox. Registering tools is not enough; the value comes from calling them at
the right moments.

Follow `doc/execution-flow.md` for the ATCT execution flow.

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

This skill is the SSOT for role derivation, claims, handoffs, worktrees,
decisions, irreversible operations, and completion records. Role skills own
only their role's operations and must not restate or override these rules.

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

The commander prepares the goal's space at the same time as its worktree, and
closes that space when the goal is approved; see `## One space per goal`.

### When the primary checkout is right

The primary checkout is appropriate in these cases:

- `commander` reviews landed changes, publishes a release, resolves conflicts
  between worktrees, or cleans up a worktree after the goal closes.
- Running `script/worktree-setup.sh` itself, because it does not run inside a
  worktree.
- Working on a goal that changes this rule: the rule cannot apply to the change
  until it lands. This rule is being written in the primary checkout for that
  reason.

### Detach node_modules before running pnpm

`web/node_modules` is a symlink to the primary checkout. Reading through it
works, but **every pnpm command fails** — not just `pnpm install`. Measured
2026-08-28: `pnpm test` cannot write `node_modules/.vite-temp` and `pnpm build`
cannot write `node_modules/.vite`, because a `workspace-write` sandbox refuses
writes that resolve outside the worktree. Full output is in
`doc/investigations/2026-08-28-worktree-node-modules-sandbox.md`.

Detach the worktree first. It replaces the symlink with real dependencies and
leaves the primary checkout untouched.

```sh
script/worktree-node-modules.sh detach          # from inside the worktree
script/worktree-node-modules.sh detach <goal>   # from the primary checkout
script/worktree-node-modules.sh status
script/worktree-node-modules.sh attach --yes    # put the symlink back
```

- **The delegator detaches, not the worker.** Measured 2026-08-28: a worker
  running `pnpm install --frozen-lockfile` in a detached worktree fails with
  `ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY` — pnpm wants to purge
  `node_modules` and cannot ask without a TTY. Detach before handing the task
  off. After that the worker needs no pnpm install: `pnpm test` and `pnpm build`
  both pass (exit 0, 231 tests).
- Detach only the worktrees that need it. The cost is time, not disk: one
  `detach` spends 33s in `pnpm install`, and most worktrees never run pnpm.
  (Disk is nearly free — `du` reports 454M per detached worktree, but the real
  consumption measured through `df` was 15M, because pnpm clones from its store
  and APFS shares the blocks.)
- `detach` and `attach` are both idempotent, so re-running either is safe.

### What a worktree does not separate

A worktree does not make every resource independent:

- `~/.atct/atct.db` is one shared database for all worktrees; goals, tasks,
  claims, and decisions are shared. Avoiding two people touching the same file
  still depends on declaring before you work.
- The daemon is one per machine, not one per worktree.
- `web/node_modules` is a symlink to the primary checkout, so a worktree does
  not get its own frontend dependencies until you detach it. See "Detach
  node_modules before running pnpm" below.
- Git objects and refs live in one common directory. The same branch can be
  checked out in only one worktree at a time.
- If two worktrees edit the same file, the conflict does not disappear; it is
  merely moved to the merge.
- `GOCACHE` is shared by default across all worktrees. Sharing is harmless
  because the cache is content-addressed, but point it outside the repository:
  if it points inside, generated files can fill `git status`.

## One space per goal

A space belongs to one goal from the moment it is created until it is closed.
When the goal is approved, close the space; do not hand it a second goal.

- **One space, one goal.** The space is created for a goal and works that goal
  only. A second goal gets a new space, even when it touches the same files.
- **Approval closes it.** The trigger is the human approving the completion
  decision `atct_goal_complete` creates, not the completion report. A rejected
  completion returns the same goal to the same space, so the space stays open
  until approval.
- **A closed space is not reopened.** Work that arrives afterwards belongs to a
  different goal, and a different goal gets a new space.
- The delegator closes what it woke. The commander closes the subcommander's
  space when the goal is approved; the subcommander closes its executors' panes
  when their tasks are done.

### The only exception

The `commander`'s own space is the exception, and there is no other. It holds
the project rather than one goal, so it outlives every goal and is not closed
between them.

Three cases look like exceptions and are not:

- A rejected completion is the same goal, not a second one, so the space stays
  open and the work continues there.
- A goal derived from another (`derived_from`) is a new goal, and a new goal
  gets a new space.
- Two goals that touch the same file still get one space each. Serializing them
  is the commander's decision about when to delegate, and the conflict, if any,
  is resolved at the merge.

### Why reuse costs more than it saves

Reuse was how one machine serialized goals that touched the same file, back when
every agent shared the primary checkout. `## One worktree per goal` removed that
reason: each goal already edits its own tree. What reuse still costs:

- The space's name stops naming its goal. On 2026-08-26 one space held five
  goals, so nothing led from the name to the contents.
- `atct_goal_sessions` resolves a goal to the sessions that worked it through
  `goal_handoffs.received_by`. One session key spread over five goals resolves
  to no single space.
- Context accumulates across goals that have nothing to do with each other.
- The trigger to close disappears. A space handed the next goal at approval is
  never closed at all; on 2026-08-26 fifteen spaces were closed by hand.

## Commit safely

When committing the goal's work, name the paths explicitly; never use `git add -A`.
If another worker's uncommitted changes share a file, stage only your hunks with `git apply --cached`.

## Delegate a task

When handing a task to another worker, keep the contract independent of how
that worker is started:

1. Hold the parent, not the task. The delegator does not hold the task; it must
   have received the handoff for that task's goal before handing it off.

2. Record the handoff before waking the worker.
   The delegator must call `atct_task_handoff_request` with a unique handoff ID
   and the task ID. Wait for the request to succeed before waking the
   worker; this creates the record needed to receive and complete the handoff.
3. For a monitored Codex worker, use an idle executor pane if one is available.
   Create a new executor pane only for parallel work, worktree isolation,
   context exhaustion, or a topic change. After the request succeeds, start the
   wrapper before the worker process:

   ```sh
   herdr pane run <pane> atct codex monitor -- <codex args>
   ```

   The delegator requests the handoff first; it does not start a worker and add
   monitoring later. Plain `herdr agent start` bypasses the wrapper and is
   forbidden for a monitored worker. A normal Codex session cannot be
   retrofitted. The monitored worker performs `atct_task_handoff_receive` with
   its SessionStart `session_key` and `monitor_token`, then `atct_role` with
   `expected_role=executor`; the launch role is metadata, not role proof.
   Other environments may wake the worker by their own supported path.
4. Put these exact instructions at the very beginning of the request:

   > First record receipt of the handoff by calling `atct_task_handoff_receive`
   > with the `task_id` provided in this request and the exact `session_key`
   > (plus `monitor_token`, when emitted) from SessionStart. Do this before
   > starting work. Do not substitute your agent name or token.
   >
   > Then invoke the `atct_role` MCP tool with `expected_role` set to
   > `executor`. If it reports `matches: false`, do not start work; return the
   > task.
   >
   > When the work is complete, record the review request by calling `atct_task_handoff_review_request` with the received `handoff_id`, the
   > `task_id`, and a non-empty `review_request_report`. The report must say
   > what was done, what was verified, what could not be verified, and paths changed.
   >
   > The subcommander receives that review with `atct_task_handoff_review_receive`.
   > It reviews the implementation, then calls `atct_task_handoff_complete` with the `handoff_id`, `task_id`, and a `complete_report` on acceptance, or calls
   > `atct_task_handoff_review_reject` with a `reject_report` on rejection.

   Report the review request before the reviewer closes the task.

   The task handoff review order is `atct_task_handoff_request` →
   `atct_task_handoff_receive` → `atct_task_handoff_review_request` →
   `atct_task_handoff_review_receive` → `atct_task_handoff_complete`; rejection
   uses `atct_task_handoff_review_reject` and returns the same handoff to the
   same worker for correction.

   Name what the worker may call, and name what it may not. A blanket ban carries
   no grain, so it is overturned without grain too: an executor that decides atct
   calls are allowed after all reaches the goal scope in the same step.

   An executor may call only these atct tools:
   `atct_session_identify`, `atct_task_handoff_receive`, `atct_role`, `atct_task_handoff_review_request`.
   Each of them is confined to the `task_id` the executor was given.

   An executor must not call `atct_goal_handoff_complete`, `atct_goal_handoff_receive`,
   `atct_goal_handoff_request`, `atct_goal_claim`, `atct_goal_release`,
   `atct_goal_complete`, `atct_goal_update_content`, `atct_project_claim`,
   `atct_project_release`, `atct_task_handoff_request`,
   `atct_task_handoff_review_receive`, `atct_task_handoff_complete`,
   `atct_task_handoff_review_reject`, `atct_task_update`,
   `atct_task_create`, or `atct_decision_ask`. Spell the names out; "anything not
   listed above" is not read as a prohibition. In a 2026-08-27 measurement, an
   executor closed a subcommander's goal handoff without knowing it was forbidden.

   An executor that reaches an irreversible or destructive operation returns it to
   the delegator. The executor does not perform the operation and does not carry
   the judgement itself; it stops there and hands it back to whoever sent the request.
   `atct_decision_ask` is the delegator's call, not the executor's. A design
   decision travels the same way, which is what `does not: make design decisions`
   in `## Roles` means in practice.

5. Name the verification boundary in the request. The delegator must name the
   verification commands the worker can run. Do not put broad commands such as
   `go test ./...` in the request. List the packages the worker may run instead.
   The worker sandbox is not the same as the delegator sandbox. In a 2026-08-27
   measurement, the same `go test` in the same worktree could bind a port for the
   delegator but failed for the worker with `bind: operation not permitted`.
   When checking whether a worker can use a tool, run the command the worker will
   actually run. Do not use `--version` or `--help` to determine availability: the
   same executable can succeed with an argument that does not touch its resource
   and fail with a permission error when one does.
   The delegator runs every verification not named for the worker and includes it
   in review. This is part of delegation, not an exception to it. The worker must
   not add verification that the request does not name. The worker must not
   silently skip verification it could not run. It must say "could not run" in
   its completion report.

6. Keep one handoff per task and one worker for its correction and review
   cycle. Return a rejection to the same worker; it remains the same task and
   handoff. When an executor finishes and unassigned tasks remain, reuse an idle executor for the next task.
   A different task alone is not a reason to create a new executor. Start a new executor pane only for parallel work, worktree isolation, context exhaustion, or a topic change. If no unassigned tasks remain, close the idle executor.
   What breaks when you batch is the record, not the context. A handoff points
   to one task. If three tasks are sent in one message, only one handoff is
   created; the other two have no owner, receipt, or completion, so the
   dashboard says nobody started them. In a 2026-08-24 measurement, sending
   three tasks to executor-33 in one message broke the records for two of the
   three. Task count and compression count are not correlated: in that same
   measurement, the three-task pane compressed twice while the one-task pane
   compressed seven times.

   For a follow-up that starts a new task on the same worker, recreate the
   `atct_task_handoff_request` with a new `handoff_id`; a closed `handoff_id`
   cannot be reused. The new handoff does not mean a different worker; it gives
   the same worker a new ID.

The worker must perform both instructions itself before doing any work. The
delegator must not run either instruction on the worker's behalf or treat a
worker name, pane title, or launch context as proof of the role. If the role
check reports a mismatch, the worker returns the task without touching it.

**Out of order:** Waking the worker before the request succeeds leaves it with
nothing to receive. Asking
for review before the executor has received the handoff, or completing the
handoff before the reviewer receives the review, leaves the record without the
report that proves what was reviewed. That last one reproduced on 2026-08-27
with two executors, one on Claude and one on Codex.

### Two-layer delegation

Delegating a task requires a received goal handoff, not a project claim.

1. For two-layer delegation, the commander calls `atct_goal_claim` to create a goal handoff addressed to itself. The project claim is checked first by `session.role` in `internal/daemon/handler.go`, so the role remains `commander`.
2. Then the commander calls `atct_task_handoff_request` to delegate each task.

**Out of order:** `atct_task_handoff_request` before `atct_goal_claim` is refused: a
delegator with no received goal handoff holds no parent for the task, so no task
can be handed off at all and every worker woken for the goal arrives with no
record to receive.

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
3. A monitored Codex subcommander is launched only after the request succeeds:

   ```sh
   atct codex monitor -- <codex args>
   ```

   A monitored commander uses the same command. The wrapper waits for the
   SessionStart token to bind to the server-derived assignment; it does not take
   a role or scope selector. Claude Code uses the same binding contract through
   `atct watch --monitor --token <monitor_token>`.

   Do not start a normal Codex process and retrofit it later.
   Name in the request every adjacent goal that touches the same files and say
   which side owns what. The delegator is the only party that can see both
   goals, and a boundary left unstated becomes a question the subcommander
   cannot answer for itself.
4. Put these exact instructions at the very beginning of the request:

   > First call `atct_session_identify` before any other atct call. If SessionStart emitted `ATCT session key: <session_id> ... monitor_token <monitor_token>`, pass those exact values as `session_key` and `monitor_token`; do not substitute your agent name or token. Only if no SessionStart key was emitted, use your stable full agent name and omit `monitor_token`.
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
   > Then, in Claude Code only, attach `atct watch --monitor --token
   > <monitor_token>` to a persistent background stream. Use the exact token
   > already passed to `atct_session_identify`; do not pass a goal.
   > The server-derived assignment limits this Watch to the received goal.
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
   > When all task handoffs are accepted, record the goal review request by calling
   > `atct_goal_handoff_review_request` with the received `handoff_id`, the
   > `goal_id`, and a non-empty `review_request_report`.
   >
   > The commander receives that review with
   > `atct_goal_handoff_review_receive` and reviews the goal's implementation.

   The order matters: the role is derived from a received, uncompleted goal
   handoff, so checking it before receipt always returns `matches: false`.

   The goal completion order is `atct_goal_handoff_review_request` →
   `atct_goal_handoff_review_receive` → `atct_goal_review_request` (recording
   the completion report while the handoff remains open) → human approval → merge → `atct_goal_review_complete`
   (which atomically completes the reviewed handoff and the goal).
   The rule is simple: only the commander may call `atct_goal_review_request`;
   after human approval and merge, only the commander may call `atct_goal_review_complete` with the `goal_id` provided in this request.
   `atct_goal_handoff_complete` is reserved for legacy/out-of-order recovery,
   not the normal goal-review path.

5. Keep one subcommander per goal. A subcommander may wake executors for its
   goal, but must not inspect or manage other goals, create another
   subcommander, or release the goal.
   A subcommander must not call `atct_goal_release`; releasing the goal is the
   commander's job.
   A subcommander must not claim the project. Claiming the project changes its
   role to commander.

6. Stay out until the completion report. After waking the subcommander, the
   delegator sends it nothing and answers nothing about the goal's design.
   What the delegator reads instead are this project's ATCT Wakeup events, which
   arrive from `atct watch` rather than from the subcommander: a goal with no
   commits, a goal with no declared tasks, a claim nobody delegated, a handoff
   nobody received. Those are what a stalled subcommander looks like from
   outside, and they arrive whether or not it speaks. Review the goal when
   `atct_goal_handoff_review_request` lands; that report is the entry point.

**Out of order:** Calling `atct_goal_handoff_complete` before
`atct_goal_review_request` closes the goal handoff, and the role is derived from a
received, uncompleted goal handoff, so the role drops from `subcommander` to
`executor` the moment it closes. Only the goal's holder may call
`atct_goal_review_request`, so the human review request can no longer be filed
at all. Recovery takes the commander reissuing the goal handoff. Goals 180 and
187 both stalled this way on 2026-08-27 and 2026-08-28.

### Session keys

The caller uses the exact key emitted by SessionStart when one is present; it
must remain unchanged for the session. Do not replace it with an agent name.
Only when SessionStart emitted no key, the caller's stable full agent name is
suitable. If a reconnect causes the role to appear wrong, call
`atct_session_identify` again with the same key to return to the original
session row.

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

## Close a task the moment it is finished

1. Land the work in the executor's worktree.
2. The executor requests review. The subcommander receives it, checks the work,
   and calls `atct_task_handoff_complete` with the completion report.
3. Delegate the next task.

A task nobody closes still reads as unstarted.

**Out of order:** Delegating the next task first can leave the finished one open
after the worker moves on. The landed work reads as unstarted for the rest of the
session, so the queue looks longer than it is and the finished task can be handed
to somebody else. Close it without linked commits and the loss is quieter but still real:
`wakeup.commits_missing` fires, and **the approver can no longer tell which
change belongs to which task.** The diff view goal 187 added
(`GET /api/goals/{id}/diff`) reads the branch, so the diff itself is visible with
no commits linked at all — but the per-task correspondence exists nowhere else.
On 2026-08-28, eight of eleven units went `done` with `task_commits` empty.

This matters most when the run that did the work is not the reviewer. The
executor finishes, the subcommander moves on, and nothing writes the result
back. **Then the dashboard says the work has not begun, and the human plans
around that.** Close the task when the executor reports, not later.

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
