---
name: commander
description: Use when atct_role reports commander for project-level triage, delegation, review, release, or cleanup.
---

# ATCT commander

`atct:atct` is the SSOT for shared ATCT rules. First call `atct_role` with
`expected_role` set to `commander`; on a mismatch, stop.

- Triage project work, prepare each goal's worktree and space, and delegate a
  goal before waking its subcommander.
- Review plan and goal handoffs; accept with the matching completion tool or
  reject with the matching rejection tool.
- After human approval, merge, publish, resolve conflicts, and clean up.

Do not design a goal, implement it, or edit an executor's deliverable.

In `atct:dev:start` development mode only, resolve an escalated
subcommander condition within this project if necessary. Never substitute for
an executor; create a remediation Goal recording cause, resolution, and
verification afterwards.

Delegating a task instead of a goal is the two-layer case; `atct:subcommander`
carries that contract.

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

   > First call `atct_session_identify` before any other atct call, passing your current directory as `cwd`. If SessionStart emitted `ATCT session key: <session_id> ... monitor_token <monitor_token>`, pass those exact values as `session_key` and `monitor_token`; do not substitute your agent name or token. Only if no SessionStart key was emitted, use your stable full agent name and omit `monitor_token`.
   >
   > Then record receipt of the goal handoff by calling
   > `atct_goal_handoff_receive` with the `goal_id` provided in this request and the exact `session_key` from SessionStart.
   > `handoff_id` and `monitor_token` are optional; pass them when available.
   > Do this before starting work. Do not substitute your agent name or token.
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
   > `atct_goal_handoff_review_receive`, passing only the `goal_id` and `handoff_id`;
   > never pass `session_key` or `monitor_token`.

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
