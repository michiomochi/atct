# Goal 254: handoff-only lifecycle and recurring wake ups

## Purpose

Make handoffs and decisions the only durable workflow state. A monitor must
re-read that state every minute and keep the responsible agent moving, without
storing delivery, monitor-ownership, review-work, or blocker state.

This intentionally replaces Goal 253's exactly-once delivery contract. A wake
up is a repeatable prompt, not a delivery guarantee or a state-changing action.

## Evidence and current gap

The current task-creation store path checks only that the goal is active before
inserting task rows (`internal/store/task.go`). It does not require a completed
plan handoff, a subcommander receipt of an approved plan, or any task-creation
handoff. Goal 254 renames this path to `CreateTasks` / `task.create` and removes
the `task.declare` name rather than retaining it as a compatibility alias.

`CompletePlanHandoff` records only the plan completion and the current
orchestration review-work settlement (`internal/store/goal_handoff.go`). It
does not create a downstream task-creation request. Consequently a direct task
task creation can bypass the approved-plan lifecycle, and an accepted plan leaves
no durable request for its next responsible agent.

## Durable state boundary

The durable workflow state after this change is limited to:

- existing task, goal, and plan handoffs;
- decisions, including a human goal-review decision; and
- a new task-create handoff, which is itself a handoff rather than delivery
  infrastructure.

The migration destructively removes these tables and all production paths that
write or query them:

- `orchestration_scope`;
- `orchestration_delivery_leases`;
- `orchestration_delivery_receipts`;
- `orchestration_review_work`; and
- `orchestration_blockers`.

Existing handoff and decision rows are retained. `monitor_health` is out of
scope: monitor-missing detection and health-based recovery are a separate goal
and must not be recreated by this work.

Human-decision waiting is derived from open decisions. Dependency/merge waiting
is not projected by this goal; it is likewise deferred rather than represented
by a replacement blocker record.

## Handoff lifecycle

### Initial handoffs and reviews

Initial task or goal handoffs retain their current request and receipt model.
Their receiver is not persisted before receipt, so an unreceived initial
handoff wakes its delegator, who is responsible for starting or checking the
intended worker. It never wakes an arbitrary executor.

Task, goal, and plan review cycles retain review request, review receipt,
rejection, and acceptance. Add these fields to each relevant handoff record:

- `review_rejection_received_by`;
- `review_rejection_received_at`.

A rejection is a new request from the reviewer to the original work submitter.
Only that submitter may record rejection receipt. It may then revise the work;
the next review request is the completion evidence for that rejected cycle.
This supplies the missing `reject request -> reject receive -> revise -> new
review request` transition for task, plan, and goal reviews.

Task-review acceptance remains terminal because the reviewer marks the task
done in the same lifecycle action. Goal-review acceptance is followed by the
commander's creation of the human goal-review decision; it is a same-owner
operation, not a new self-handoff.

### Approved plan to implementation task

Add a `task_create_handoffs` lifecycle record with a plan-handoff reference,
goal ID, requester and receiver sessions, request/receipt/completion timestamps
and reports, and the task IDs it created. It has this canonical path:

```text
commander:     plan.handoff.complete
               + task-create-handoff.request  (one transaction)
subcommander:  task-create-handoff.receive
subcommander:  task.create (with task-create handoff)  (creates implementation tasks)
subcommander:  task.handoff.request             (delegates each task)
subcommander:  task-create-handoff.complete
executor:      task.handoff.receive
```

The request receiver is the subcommander that submitted the plan review. The
commander creates this request atomically with accepting the plan, so a plan
cannot be accepted without a recorded next responsibility.

`task.create` is the sole public task-creation operation and is the daemon
method used by `atct_task_create`. It requires a received, uncompleted
task-create handoff for implementation tasks covered by an accepted plan, and
records the created task IDs on that handoff. The operation retains
idempotency-key semantics. `task.declare`, `DeclareTasks`, and any compatibility
alias are removed; all MCP, daemon, test, and documentation callers use the
same create terminology.

`task-create-handoff.complete` requires successful task creation and a
recorded `task.handoff.request` for every created implementation task. A task
cannot be considered the next responsibility merely because a row exists; it
must be delegated. The handoff therefore preserves all four required proofs:
request, receive, create, and complete.

### Planning-task boundary

This restriction begins only after a plan handoff for the goal has been
accepted. Before that transition, `task.create` (through `atct_task_create`)
creates the goal's own design, spec, and plan-review task; Goal 254's existing
task 1230 is one such task. A completed plan handoff is the durable boundary,
so the daemon does not infer a task's kind from its title, agent name, or pane.

After a completed plan handoff exists for a goal, `task.create` without its
received task-create handoff rejects every new task for that goal with the
lifecycle error. The received task-create handoff is then the only creation
authority. This prevents an accepted plan from being bypassed while retaining
the normal pre-plan design workflow. The operation is idempotent for its
handoff and records every task ID it created.

## Recurring wake-up projection

Every monitor reconciliation, including the periodic one-minute tick, derives
the desired wake-ups directly from current handoff and decision state:

| Current state | Wake-up target | Stops when |
| --- | --- | --- |
| initial task/goal handoff requested, not received | delegator | handoff receipt |
| review requested, not received | rightful reviewer | review receipt |
| review received, not accepted or rejected | reviewer | acceptance or rejection |
| review rejected, rejection not received | original submitter | rejection receipt |
| rejection received, no new review request | original submitter | next review request |
| plan accepted, task-create handoff requested/received/created but incomplete | approved-plan subcommander | task-create handoff completion |
| task handoff requested, not received | its delegator | executor receipt |
| goal review accepted, no human goal-review decision | commander | decision creation |

The projection does not create work, assign roles, or execute lifecycle calls.
Existing store authorization remains the authority for each call.

The bridge maintains only an in-memory desired/pending set keyed by
`handoff_id + phase`. At most one copy of a desired wake-up waits while a turn
is active. A fresh one may be queued after that turn completes if the phase is
still current. Reconciliation removes a pending wake-up when its source phase
has changed. A monitor restart loses this memory by design; the next
reconciliation reconstructs the desired set from handoffs. No receipt,
acknowledgement, lease, or delivery history is persisted.

## Error handling and authority

- A reconciliation, SSE, or bridge failure changes no workflow state; the next
  one-minute tick re-evaluates the handoffs.
- Repeated prompts are permitted. Duplicate lifecycle operations are rejected
  by the handoff transaction and never grant a role or alter ownership.
- An active bridge must not accumulate one queued turn per minute for the same
  phase. Coalescing is process-local queue control, not delivery deduplication.
- No monitor-missing detection, wrapper restart, fencing, or cross-monitor
  ownership is implemented here.

## Verification

Tests must establish all of the following.

1. The destructive migration removes every listed `orchestration_*` table and
   production schema/query/API reference, while preserving existing handoffs
   and decisions.
2. Completing a plan handoff atomically creates exactly one task-create request
   for its submitting subcommander; wrong sessions cannot receive, create, or
   complete it.
3. `task.create` is the only task-creation name across store, daemon, MCP,
   tests, and documentation; no `task.declare` endpoint or alias remains.
   Before plan acceptance it can create the design task. After plan acceptance,
   it rejects every new task for the goal unless a received task-create handoff
   authorizes it. The authorized operation creates idempotently, records its
   task IDs, and completes only after every created task has a requested task
   handoff.
4. Task, plan, and goal review rejections require the original submitter's
   explicit receipt before revision; a new review request closes that rejection
   phase. Foreign sessions are rejected at every transition.
5. Reconciliation maps every table row above to only its named responsible
   role. It repeats an unchanged phase on successive one-minute ticks and
   stops projecting it on the stated transition.
6. Claude and Codex use the same typed action selection. While active, either
   bridge retains at most one pending action for one handoff phase; a state
   change removes it, and a restarted monitor reconstructs it from the
   handoff state.
7. Existing task/goal/plan handoff role authorization and terminal task-done
   behavior remain intact.

## Out of scope

- monitor absence, monitor health, lease takeover, and automatic wrapper
  restart;
- dependency/merge blocker routing;
- restoring Goal 253's exactly-once notification or delivery receipt behavior;
- implementation tasks, executor handoffs, or code changes before this plan is
  reviewed and accepted.
