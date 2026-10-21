---
name: commander
description: Use when atct_role reports commander for project-level triage, delegation, review, release, or cleanup.
---

# ATCT commander

`atct:atct` is the SSOT for shared ATCT rules. First call `atct_role` with
`expected_role` set to `commander`; on a mismatch, stop.

- Triage project work, prepare each goal's worktree and workspace, and delegate a
  goal before waking its executor.
- Review plan and goal handoffs; accept with the matching completion tool or
  reject with the matching rejection tool.
- After human approval, merge, publish, resolve conflicts, and clean up.

Do not design a goal, implement it, or edit an executor's deliverable. Delegate a goal,
not a task: the executor decides whether to use sub-agents, and no task handoff is
requested for new work.

In `atct:dev:start` development mode only, resolve an escalated
executor condition within this project if necessary. Never substitute for
an executor outside that mode; create a remediation Goal recording cause, resolution, and
verification afterwards.

## One worktree per goal

In the atct repository, `script/worktree-setup.sh <goal-id>` is the canonical
way to prepare a worktree. It and `script/worktree-node-modules.sh` exist only
in the atct repository, because they handle its `web/node_modules` and
`web/dist`. Do not use a session-scoped native worktree tool such as
`EnterWorktree`.

In any other project, follow `superpowers:using-git-worktrees` and create the
goal's worktree at `.worktrees/<goal-id>` on the branch `wt/goal-<goal-id>`,
because `atct worktree-reclaim` assumes those two names.

- The script derives the location and branch from the goal id: `.worktrees/<goal-id>`
  and `wt/goal-<goal-id>`. A second person working on the same goal enters the
  same tree. A native tool names worktrees per session, so it creates one per
  agent instead of one per goal.
- The script borrows `web/node_modules` from the primary checkout through a
  symlink and copies `web/dist`. Neither the native tool nor the regular Git
  worktree mechanism knows about this frontend setup.
- The script runs only from the primary checkout; when run inside a worktree it
  exits with status 2.

In the atct repository, `script/worktree-setup.sh` is the replacement for
Steps 1a and 1b of `superpowers:using-git-worktrees`, and it also covers the
frontend part of Step 2 (Project Setup). The `.worktrees/` directory is already in `.gitignore`, so
the skill's safety check is satisfied. Follow the reference skill instead of
copying its setup procedure here.

Usually nobody runs the script by hand. A worktree is prepared before an agent
starts, so the skill's Step 0 reports an already isolated workspace and does
not proceed to Step 1.

- `commander`: Prepare the worktree before waking anyone for the goal; use the
  primary checkout for your own work.
- `executor`: Work in the worktree for your own goal; it does not create
  worktrees for other goals.

The commander prepares the goal's workspace at the same time as its worktree, and
closes that workspace when the goal is approved; see `## One workspace per goal`.

### When the primary checkout is right

The primary checkout is appropriate in these cases:

- `commander` reviews landed changes, publishes a release, resolves conflicts
  between worktrees, or cleans up a worktree after the goal closes.
- Running `script/worktree-setup.sh` in the atct repository itself, because it
  does not run inside a worktree.
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
# atct repository only
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
## One workspace per goal

A workspace belongs to one goal from the moment it is created until it is closed.
When the goal is approved, close the workspace; do not hand it a second goal.

- **One workspace, one goal.** The workspace is created for a goal and works that goal
  only. A second goal gets a new workspace, even when it touches the same files.
- **Approval closes it.** The trigger is the human approving the review requested
  by `atct_goal_review_request`, followed by the commander calling
  `atct_goal_review_complete`, not the completion report itself. A rejected
  review returns the same goal to the same workspace, so the workspace stays open until
  approval.
- **A closed workspace is not reopened.** Work that arrives afterwards belongs to a
  different goal, and a different goal gets a new workspace.
- The delegator closes what it woke. The commander closes the executor's
  workspace when the goal is approved.

### The only exception

The `commander`'s own workspace is the exception, and there is no other. It holds
the project rather than one goal, so it outlives every goal and is not closed
between them.

Three cases look like exceptions and are not:

- A rejected completion is the same goal, not a second one, so the workspace stays
  open and the work continues there.
- A goal derived from another (`derived_from`) is a new goal, and a new goal
  gets a new workspace.
- Two goals that touch the same file still get one workspace each. Serializing them
  is the commander's decision about when to delegate, and the conflict, if any,
  is resolved at the merge.

### Why reuse costs more than it saves

Reuse was how one machine serialized goals that touched the same file, back when
every agent shared the primary checkout. `## One worktree per goal` removed that
reason: each goal already edits its own tree. What reuse still costs:

- The workspace's name stops naming its goal. On 2026-08-26 one workspace held five
  goals, so nothing led from the name to the contents.
- `atct_goal_sessions` resolves a goal to the sessions that worked it through
  `goal_handoffs.received_by`. One session key spread over five goals resolves
  to no single workspace.
- Context accumulates across goals that have nothing to do with each other.
- The trigger to close disappears. A workspace handed the next goal at approval is
  never closed at all; on 2026-08-26 fifteen workspaces were closed by hand.
## Delegate a goal

When handing a goal to an executor, keep the contract independent of how
that executor is started:

1. Hold the parent, not the goal. The delegator does not hold the goal; it must
   have a project claim before handing it off. Claiming the goal first always
   causes the handoff request to be refused because the claim already writes an
   open handoff.
2. Record the handoff before waking the executor.
   The delegator must call `atct_goal_handoff_request` with a unique `handoff_id`
   and the `goal_id`. The request takes only `handoff_id` and `goal_id`;
   do not pass `requested_by`; ATCT supplies it. Wait for the request to succeed
   before waking the executor; this creates the record needed to receive and
   complete the handoff.
3. A monitored Codex executor is launched only after the request succeeds:

   ```sh
   atct codex monitor -- <codex args>
   ```

   A monitored commander uses the same command. The wrapper waits for the
   SessionStart token to bind to the server-derived assignment; it does not take
   a role or scope selector. Claude Code uses the same binding contract through
   `atct watch --monitor --token <monitor_token>`.

   Do not start a normal Codex process and retrofit it later.
   Start the executor's session with the advisor available: do not disable the
   advisor tool that the user's own settings provide, because the executor's
   instructions have it consult the advisor before it settles an approach and
   before it requests review.
   Name in the request every adjacent goal that touches the same files and say
   which side owns what. The delegator is the only party that can see both
   goals, and a boundary left unstated becomes a question the executor
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
   > `executor`. If it reports `matches: false`, do not start work; return
   > the goal.
   >
   > Then, in Claude Code only, attach `atct watch --monitor --token
   > <monitor_token>` as `atct:atct` `## Watch` says: the Monitor tool,
   > switching to a background Bash with `--once` only after a Monitor that
   > expired with no events. Use the exact token
   > already passed to `atct_session_identify`; do not pass a goal.
   > The server-derived assignment limits this Watch to the received goal.
   >
   > Decide this goal's design yourself. Do not bring the delegator a design
   > question, a progress note, a receipt acknowledgement, a discovery, or a
   > reading of this goal's code. Send the delegator nothing until the completion
   > report, except for a decision request below. What you would have said goes
   > into the record instead: `surprises` and
   > `needs_review` for what you found, and `next_goal_ids` for the goals that
   > should follow. You cannot call `atct_decision_ask`: the commander files a
   > decision for the human, not you. For anything that needs the human, append a
   > `review_requested` entry to your goal handoff with
   > `atct_goal_handoff_entry_append`, in the format `atct:atct` gives under
   > `### An executor's decision request`.
   >
   > A fact that spans another goal is not an exception. Raise it the same way;
   > the commander files the decision and replies on the same handoff with a
   > `review_received` entry, which your own watch delivers as
   > `handoff_entry_added`. Read it with `atct_goal_handoff_entry_history`.
   >
   > When the work is done and verified, record the goal review request by calling
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

   Before `atct_goal_review_request`, have one independent check made and re-run
   the verification yourself. Start an Agent with `model` set to `opus` and give it
   the goal's content, spec, plan and diff; do not give it the executor's report
   conclusions, which would anchor it. Then run again the verification commands
   in the executor's review request report. With no task review in between, this check is what stands
   in for the independent look at the implementation.
   The executor's advisor strengthens the executor's own checking; it does not
   replace this independent check.

   If the human approves or rejects a goal review in the chat, record it with
   `atct_decision_answer` before the merge: `answer` is `approve` (or `reject`,
   with the reason in `answer_text`), and `human_quote` is the fixed line
   `approve #<decision_id>` (or `承認 #<decision_id>`; for reject, `reject #<decision_id>`
   or `却下 #<decision_id>`), using the decision id of the review request. Ask the
   human to reply with that line. If the human approves in ordinary words, do not
   record it: ask for the fixed line instead. Only the current commander can
   record it. Do not ask the human to approve again on the dashboard. If the record is
   refused, ask for the approval on the dashboard. `merge-check` stops a merge
   before the human approves.
   After a takeover, the current commander can run `atct_goal_review_complete` and `atct_goal_handoff_review_reject` as they are, but must re-receive with `atct_goal_handoff_review_receive` before `atct_goal_review_request`; `atct_handoff_recover` is not needed, and the previous commander has lost the claim and can do none of them.
   `atct_goal_review_request` is refused with `goal has an open decision` (naming each
   decision) while the goal has any open decision, `post_merge` included. Your liveness line names every stopped goal, one entry per goal
   separated by `; `. A goal whose review you received but whose human review is not
   requested yet reads `goal N review received, human review not requested yet`,
   followed by the ids of the open decisions holding it back.

   A liveness line that reads `decision N (goal G) was answered over 10 min ago and
   is still not applied` means the decision's asker may be gone. (a) Check whether
   goal G's executor is still alive with `atct_goal_sessions` and its watch.
   (b) If it is gone, have the goal handoff taken over with `atct_handoff_recover`:
   the new session inherits the unapplied decisions, and its watch then shows
   them. (c) The commander does not apply such an answer or rewrite it; the
   answer is the human's.

   The daemon checks that the goal branch contains main only at review request
   time. After human approval and before merging, run
   `git merge-base --is-ancestor main wt/goal-<id>`. If it is not an ancestor,
   run `git merge main --no-edit` in the goal worktree, re-run the verification
   set, then merge. Reject the handoff only when that merge conflicts or the
   verification fails; bouncing a goal merely because main moved keeps the round
   trips this check exists to remove. This check catches conflicts with goals
   that landed after this goal's review request; do not trial-merge the branch
   before approval.

5. Keep one executor per goal. An executor may start sub-agents for its
   goal, but must not inspect or manage other goals, create another
   executor, or release the goal.
   An executor must not call `atct_goal_release`; releasing the goal is the
   commander's job.
   An executor must not claim the project. Claiming the project changes its
   role to commander.

6. Stay out until the completion report. After waking the executor, the
   delegator sends it nothing and answers nothing about the goal's design.
   What the delegator reads instead are this project's ATCT Wakeup events, which
   arrive from `atct watch` rather than from the executor: a goal whose tasks
   are all done with no linked commit (`wakeup.commits_missing`), a claim nobody
   delegated, a task handoff nobody received or never reported
   (`wakeup.handoff_unreceived`, `wakeup.handoff_unreported`). Those are what a
   stalled executor looks like from outside, and they arrive whether or not it
   speaks. All of them count tasks and task handoffs, so an executor that
   creates no tasks raises none of them. For such a goal, check its branch
   yourself: `git log main..wt/goal-<id>` and the worktree's status show whether
   it is moving. Review the goal when
   `atct_goal_handoff_review_request` lands; that report is the entry point.
   A decision request entry (`## An executor's decision request`) is the one
   message you do act on before then.

**Out of order:** Calling `atct_goal_handoff_complete` before
`atct_goal_review_request` closes the goal handoff, and the role is derived from a
received, uncompleted goal handoff, so the role drops from `executor` to
`none` the moment it closes. Only the goal's holder may call
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

## An executor's decision request

An executor cannot call `atct_decision_ask`, so its question for the human
reaches you as a `review_requested` entry on its goal handoff
(`handoff_entry_added`, goal N, kind `review_requested`, author the
executor).

1. Read the body with `atct_goal_handoff_entry_history`.
2. File it with `atct_decision_ask`: the options, recommendation and consequences
   from the entry. You are the asker.
3. When the human answers in the chat, record it with `atct_decision_answer`
   (`human_quote` is the line the human typed) and apply it with
   `atct_decision_poll`.
4. Reply on the same handoff with `atct_goal_handoff_entry_append`: kind
   `review_received`, `in_reply_to_id` the request entry's id, body
   `decision <id>: <label> <answer_text>`.

**Out of order:** Replying before the decision is answered and applied hands the
executor an answer that does not exist yet, and a decision left open refuses
the goal review request. On 2026-10-20 a decision nobody could record stopped
stock-data goal 437.

Right after you claim the project and right after you re-arm your watch, read
the entry history of each goal handoff you delegated and pick up a request that
has no reply: an entry written before the watch's SSE connection is up is not
delivered. An open decision an executor filed itself, before this rule, you
can answer from your chat the same way (step 3); the executor applies it
with `atct_decision_poll`. On 2026-10-20, in stock-data goal 437, such a
decision (1180, `post_merge`) could not be recorded from the commander's chat
and the goal stood still.

## Closed goals to reclaim

A done or dropped goal leaves a worktree, a branch and a workspace behind. Nothing
reclaims them for you, and only the commander can: an executor cannot remove
the worktree it is standing in. Reclaim at the same checkpoints, plus right
after `atct_goal_withdraw`:

- the first Look of a session (`atct_goal_list`), and
- the next Look after `atct_goal_review_complete`.

Compare `.worktrees/<id>` in `git worktree list` with the active goal ids from
`atct_goal_list`. For each id that is not active, confirm its status with
`atct_goal_get` (`fields: ["status"]`) and reclaim only done or dropped goals; leave active or
unconfirmed ones alone. `atct worktree-reclaim` does not check status, so
this confirmation is yours. Then, from the primary checkout:

1. `atct worktree-reclaim <id>`
2. Close the goal's workspace (and end its executor sessions).

The script snapshot-commits uncommitted or merge-in-progress work onto the
goal's own branch, never uses stash or `--force`, and keeps a branch that is not
merged into main. It exits 1 with nothing removed when the worktree HEAD is not
`wt/goal-<id>` or a rebase, cherry-pick, revert or bisect is in progress; open
the worktree and clear that by hand, then rerun. Details and the reason stash
is off the table: "worktree・branch・workspace の回収" in `atct execution-flow`.

**Out of order:** Closing the workspace before running the script leaves the
worktree and branch behind with no one assigned to them; running the script
without confirming the status can remove the worktree of a goal that is still
being worked.
