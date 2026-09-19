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

Do not inspect other goals, publish, create another subcommander, or claim the
project.

On an unexpected condition, escalate to the commander. In `atct:dev:start`
development mode only, resolve an escalated executor condition in this Goal if
needed, then create a remediation Goal recording cause, resolution, and
verification.

## Delegate a task

When handing a task to another worker, keep the contract independent of how
that worker is started:

1. Hold the parent, not the task. The delegator does not hold the task; it must
   have received the handoff for that task's goal before handing it off.

2. Record the handoff before waking the worker.
   The delegator must call `atct_task_handoff_request` with a unique handoff ID
   and the task ID. Wait for the request to succeed before waking the
   worker; this creates the record needed to receive and complete the handoff.
3. For a monitored Codex worker, reuse an idle executor workspace if one is
   available. Create a new one only for parallel work, worktree isolation,
   context exhaustion, or a topic change. After the request succeeds, start the
   worker process through the monitor wrapper:

   ```sh
   atct codex monitor -- <codex args>
   ```

   How that command is placed in a workspace is the terminal multiplexer's
   concern, not ATCT's.

   The delegator requests the handoff first; it does not start a worker and add
   monitoring later. Starting Codex directly bypasses the wrapper and is
   forbidden for a monitored worker, and a normal Codex session cannot be
   retrofitted. The monitored worker performs `atct_task_handoff_receive` with
   its SessionStart `session_key` and `monitor_token`, then `atct_role` with
   `expected_role=executor`; the launch role is metadata, not role proof.
4. Put these exact instructions at the very beginning of the request:

   > First record receipt of the handoff by calling `atct_task_handoff_receive`
   > with the `task_id` and `handoff_id` provided in this request, and the exact
   > `session_key` (plus the optional `monitor_token`, when emitted) from SessionStart. Do this before
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
