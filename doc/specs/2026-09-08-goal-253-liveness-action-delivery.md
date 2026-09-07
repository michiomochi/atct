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

## Scope expansion: all measured stop paths

The 2026-09-08 space audit is recorded in
`doc/investigations/2026-09-08-goal-253-atct-space-liveness-audit.md`. It
extends this goal from the Goal 248 incident to the delivery boundaries that can
make a commander, subcommander, or executor stop without a next action. It
does **not** merge ownership from related goals.

1. `plan.handoff.complete` is a lifecycle transition that must be selected as
   an action after normal scope filtering and delivery deduplication. Its
   delivery generation is the completed timestamp plus handoff ID, so a live
   event and reconcile/reconnect yield one action, while a new completion is
   independently deliverable.
2. An open human decision may suppress a *liveness prompt* but may never
   suppress a selected handoff/review/approval action. A focused regression
   must show an open decision together with a fresh goal-scoped review rejection
   delivers the rejection once and produces no replacement liveness action.
3. The shared selector is the sole Claude/Codex policy boundary. The complete
   matrix for recovered approvals, review rejection, and plan completion must
   assert identical ordered typed actions for both transports.
4. Monitor coverage is an observable prerequisite, not an inference from a
   pane title. Diagnostics must distinguish: no live monitor record; a live
   monitor with no selected action; a selected action queued for an idle turn;
   a delivery dedupe suppression; and a session/role mismatch. They must expose
   facts only and neither attach a monitor to an old space nor mutate claims,
   handoffs, decisions, or queues.
5. Receipt/reconcile behavior must remain generation-deduplicated. In
   particular, reconnect cannot replay Decision 700's already delivered action
   indefinitely, and receipt cannot discard a queued prerequisite action.
6. Dependency/merge waits and human-decision waits are represented as such;
   the monitor must not synthesize execution or reassign ownership for them.

### Ownership boundary

Goal 182 owns health-vs-progress detection; Goal 203 owns session-key recovery;
Goal 221 owns heartbeat leases and stale takeover; Goal 227 owns goal-handoff
authority; Goals 228/237/240/245 own their dependency/merge work; Goal 248 owns
intentional local-response suppression; Goal 252 owns missing goal-review
detection. This goal may use their canonical states at the selector/diagnostic
boundary, but does not duplicate their stores, migrations, or automatic
recovery.

## Revision 2: end-to-end orchestration recovery

The rejected review correctly identified that observation alone leaves an
orphaned space stopped. Goal 253 therefore owns the *shared orchestration
boundary* from a canonical stop condition to one actionable instruction for its
rightful role. It does not take ownership of the related goals' domain state or
permit an executor to impersonate a subcommander.

### Canonical recovery envelope

Every newly covered condition is normalized before either transport sees it:

```text
orchestration.recovery {
  condition: recipient_mismatch | monitor_missing | human_decision_wait |
             dependency_merge_wait | lifecycle_complete,
  target_role: commander | subcommander,
  goal_id, task_id?, handoff_id?, blocker_id?, generation,
  instruction
}
```

The envelope is selected only by the shared `watchAgentAction` boundary and is
delivered using `(condition, target_role, stable subject ID, generation)`. A
live event, reconciliation, reconnect, and a second monitor wrapper therefore
cannot create a second action for the same condition generation. A changed
handoff, decision/blocker, monitor-registration generation, or lifecycle
completion is a new action. Claude and Codex receive the same ordered typed
envelopes and render the same human-readable instruction.

### Safe recipient/session recovery

When canonical handoff/session data shows that the active receiver's derived
role cannot perform the next lifecycle operation, the signal targets the
commander—not the mismatched receiver. Its instruction names the handoff,
expected role, observed role/session key, and the supported recovery operation:
reissue/transfer to a freshly role-validated rightful receiver, then have that
receiver run session identification, receive, and role validation before work.
An executor never gains subcommander authority through recovery; a live valid
receiver is never stolen. Goal 203 remains the owner of key discovery and its
diagnostic wording, while Goal 221 remains owner of live/stale lease and
takeover rules. Goal 253 only routes their canonical mismatch result to the
commander exactly once.

### Missing-wrapper recovery

For every registered active scope, the shared monitor state compares the scope
registration/lease with its live wrapper registration. A missing live wrapper
creates a durable commander-targeted recovery envelope with a stable scope ID
and registration generation. Its instruction gives the exact monitored restart
form for the rightful role and scope; it does not launch a process itself or
attach a wrapper to a legacy pane. It remains observable until the canonical
wrapper is live, while delivery stays exactly once per missing generation.
Goal 221 continues to define whether a lease is live; Goal 253 maps that fact
to an actionable project-level route.

### Legitimate blocker routing

Open human decisions and dependency/merge blockers do not authorize bypassing
the decision, merge, migration order, or handoff owner. Instead, their canonical
state produces a commander-targeted recovery envelope once per blocker
generation. The instruction identifies the owner and prerequisite; the agent
whose scope is blocked remains paused. An open decision can still suppress
periodic liveness noise, but it cannot suppress this one durable commander
route or an independent handoff/lifecycle action.

### Rightful-role lifecycle restart

Lifecycle completion is actionable only at the next authority boundary:

| Completion | Action target | Instruction boundary |
| --- | --- | --- |
| task handoff complete/review transition | owning subcommander | review/close or re-delegate the task; never the executor |
| plan handoff complete | owning subcommander | continue the approved plan or create its declared task |
| goal handoff complete/review transition | commander | request/perform the human-goal-review path |

Each row is sourced from the canonical handoff owner and completion generation,
not from the pane that happened to emit the event. Receipt and completion must
not remove a pending prerequisite action. Existing Goal 227 authority guards
remain authoritative.

### Expanded focused acceptance matrix

| Scenario | Required result |
| --- | --- |
| wrong recipient / role mismatch | one commander recovery action; executor cannot act as subcommander; valid live receiver is unchanged |
| active registered scope lacks wrapper | one durable commander restart instruction; registration becoming live clears the condition; reconnect does not duplicate |
| open decision or merge dependency | one commander blocker route; scoped agent stays blocked; liveness noise remains suppressed |
| task, plan, goal completion | one action to the table's rightful role; live/reconcile/reconnect are non-duplicating |
| one condition through Claude/Codex | same typed envelope and order on both transports |
| Decision 700 / received approval | completed Task 1215's one-per-lifecycle behavior remains unchanged |

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
| Plan handoff completes, live then reconcile | one `plan.handoff.complete` action per generation |
| Open decision + fresh goal review rejection | rejection action once; no liveness replacement |
| Monitor diagnostic state | coverage, queue/dedupe/role outcome are distinguishable without mutation |

## Non-goals

- Persistent cross-process delivery acknowledgement.
- Changing liveness cadence, health reporting, SSE replay, or daemon storage.
- Implementing the production change before plan review acceptance.
