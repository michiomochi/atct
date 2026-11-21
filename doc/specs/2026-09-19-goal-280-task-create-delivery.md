# Goal 280: task-create handoff delivery

## Purpose

After an accepted plan is completed, the daemon creates a durable task-create
handoff for the subcommander. A Codex monitor must recover that request once,
keep its lifecycle identity stable, and leave the durable handoff available for
recovery if turn submission is unknown.

## Evidence and root cause

- `CompletePlanHandoff` creates the task-create row in the same transaction as
  the plan completion. The row remains unreceived until
  `task.create_handoff.receive` succeeds.
- Reconciliation already projects `task.create_handoff.request` and
  `task.create_handoff.receive`; the existing watch regression covers request,
  receipt, and completion.
- Those event names use `task.create_handoff.*`, while the older lifecycle
  events use `*.handoff.*`.
- `watchActionDeliveryIdentity` and `codexMonitorActionFromWatchAction` only
  recognize `.handoff.`. A task-create action therefore falls back to its
  rendered line as the delivery subject and reaches the Codex bridge without
  the handoff ID. The bridge's identity and recovery contract is consequently
  incomplete for the exact handoff created after plan acceptance.

The existing focused watcher tests pass, but there is no Codex task-create
lifecycle test. This is why the durable row can remain unreceived while the
monitor only reports liveness.

## Requirements

1. A subcommander monitor selects and queues a task-create request from
   reconciliation, with a delivery key whose subject is the durable handoff ID.
2. `task.create_handoff.request` and `.receive` remain distinct lifecycle
   actions, and the completed handoff produces no action.
3. Repeating the same reconciliation in one monitor does not enqueue the same
   task-create request twice.
4. An unknown Codex turn submission leaves the request queued and does not mark
   the durable handoff received. A fresh monitor can recover the request from
   reconciliation.
5. Normal `*.handoff.*` events and Goal 279's fail-closed Codex submission
   behavior remain unchanged.
6. No store schema, daemon handoff state, persistent action cursor, or adjacent
   goal behavior changes are included.

## Design

Use one small shared predicate for handoff lifecycle event names: an event is a
handoff event when it contains `handoff.`. This covers both `task.handoff.*`
and `task.create_handoff.*` without maintaining another event-name list.
Use that predicate in both delivery-key construction and Codex action
conversion, so the same durable handoff ID is used at both sides of the
transport boundary.

Keep `task.create_handoff.receive` agent-facing. Only
`*.handoff.review.receive` is control-only because it advances review
coalescing; task-create receipt must still prompt the subcommander to create
implementation tasks.

The bridge remains responsible only for FIFO delivery, deduplication by the
typed delivery key, and retaining an unknown submission. The durable handoff
store remains the recovery source; no bridge-side acknowledgment is added.

## Verification matrix

| State or event | Expected result |
| --- | --- |
| Requested task-create handoff | one subcommander Codex action; handoff ID is stable |
| Same request reconciled twice | one queued/started action |
| Received task-create handoff | one distinct receive action |
| Completed task-create handoff | no action |
| Unknown turn submission | queued action retained; durable handoff still unreceived |
| Fresh monitor after failure | one recovery action from the same durable request |
