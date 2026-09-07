# Goal 253: durable monitor action delivery

## Purpose

Repair the monitor delivery contract exposed by the Goal 248 incident. A
canonical state transition must be recoverable as an action after an event or
monitor attachment is missed, without turning reconcile/reconnect into an
infinite duplicate-action source. The change covers both Claude and Codex
through their existing shared `watchAgentAction` selection boundary.

## Evidence

The investigation is recorded in
`doc/investigations/2026-09-08-goal-253-liveness-action-loss.md`. It establishes
that Goal 249 intentionally (but incorrectly for this requirement) suppresses
an applied goal approval after `ReceivedAt`, repeats the surviving projection
on every reconciliation, and prunes a queued approval on handoff receipt.

## Requirements

1. A project-scoped commander monitor receives exactly one
   `decision.approved` action for an applied approval of an active goal, even
   when the corresponding goal handoff already has `ReceivedAt`.
2. A repeated reconcile or reconnect within one monitor lifecycle does not
   enqueue or render that same approval again. The design must not reintroduce
   decision 700's duplicate notification behavior.
3. A fresh goal-scoped subcommander must receive the current actionable
   `goal.handoff.review.reject` state from reconciliation. When liveness is due
   as well, the rejection is preserved and delivered before the later liveness
   action in reconciliation order.
4. A Codex bridge must not remove a queued required approval merely because a
   later `goal.handoff.receive` action for the same goal arrives. It preserves
   FIFO delivery until an idle notification starts each queued turn.
5. Claude and Codex must continue to use `selectWatchAgentAction`; no
   transport-specific raw-line classification or selector is allowed.
6. Closed goals remain ineligible for an applied-approval action. Goal-scoped
   and task-scoped monitors do not receive the project-commander's approval
   transition.

## Design

### Canonical approval projection

`reconcileWatchScope` continues to recognize an applied `goal_approval` only
for an active goal in a project-scoped monitor. It no longer inspects
`GoalHandoffs.ReceivedAt`: receipt is a subcommander ownership state, not proof
that the commander observed the approval.

Instead of writing the projection directly, reconciliation routes it through
`emitWatchDecisionWithStateAndSinks` with event name `decision.approved`. That
function already owns the in-memory `watchDeliveryKey` map. For this event the
key is `(eventName, decisionID, defaultApplied)`, so the first reconciliation
delivers the action and subsequent reconnect/reconcile attempts in the same
watch process are suppressed. A newly started monitor has an empty in-memory
map and receives one recovery action; no persistent cursor or acknowledgement
protocol is introduced.

### Queue semantics

`codexMonitorBridge` is a FIFO transport, not a policy engine. Remove the
receipt-triggered approval pruning path. The shared selector has already made
the action eligible; receipt cannot revoke an approval that the commander must
observe. The bridge retains its existing behavior for failed submissions and
idle transitions: it removes an action only after `StartTurn` accepts it, and
`turn/completed` / idle status calls `pumpAfterIdle`.

### Goal-scoped recovery

`watchReconciliationHandoffEvent` keeps mapping the most advanced live
handoff state to an event with that state's timestamp as generation. In a
goal-scoped watch, `watchScopeFilter` continues to admit goal-handoff review
events. This yields exactly one review-reject action per lifecycle generation;
liveness is a separate later action and has no authority to suppress or replace
the reject.

### Shared transport boundary

Formatting, scope filtering, delivery-state deduplication, and
`selectWatchAgentAction` remain upstream of both transports. Claude receives
the selected action through its writer sink; Codex receives that exact typed
action through `ActionSinkWithContext`. Tests compare the selected action
sequences, not separately maintained lists of accepted text prefixes.

## Alternatives considered

1. Keep receipt-based suppression and teach only the Codex queue to restore an
   approval. Rejected: Claude would still lose the transition and the state
   model would remain contradictory.
2. Persist a global action acknowledgement cursor. Rejected: it is broader
   storage/protocol work and would stop a fresh monitor from recovering a
   previously missed state without further identity and expiry design.
3. Project every applied approval on every reconciliation. Rejected: this is
   the current duplicate behavior and recreates decision 700.

The recommended design is the in-memory lifecycle deduplication above: one
recoverable action per monitor lifecycle, with canonical-state reconciliation
as the recovery source.

## Focused regression matrix

| Scenario | Expected action sequence |
| --- | --- |
| Active goal, no handoff | one `decision.approved` |
| Active goal, handoff already received | one `decision.approved` |
| Same snapshot reconciled again in one monitor | no second approval |
| Closed or dropped goal | no approval |
| Fresh goal scope, review rejected and liveness due | review reject, then liveness |
| Codex active queue: approval then handoff receive | approval remains first; receive follows after later idle |
| Claude/Codex selection | identical ordered typed actions |

## Non-goals

- Persistent cross-process delivery acknowledgement.
- Changing liveness cadence, health reporting, SSE replay, or daemon storage.
- Implementing the production change before plan review acceptance.
