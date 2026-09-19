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

## Revision 3: implementable durable ownership

Revision 2's global exactly-once wording was not implementable: `watchDeliveryKey`
is a per-process map, and `CodexMonitorRecord` has no role/goal/task identity.
This revision replaces those assumptions with the following durable authority.

### Expected scope and live-wrapper identity

The daemon derives an **expected scope** from the lifecycle records it already
owns, never from Herdr panes or Git state:

| Rightful role | Expected-scope source | Stable scope key |
| --- | --- | --- |
| commander | live project claim | `project:<project_id>:commander` |
| subcommander | received, uncompleted goal handoff | `goal:<goal_id>:subcommander:<handoff_id>` |
| executor | received, uncompleted task handoff | `task:<task_id>:executor:<handoff_id>` |

The lifecycle transaction creates/updates an `orchestration_scope` row with that
key, rightful session/role, source handoff/claim generation, and `active` state;
completion/release/rejection transitions it inactive or creates a new
generation. This is the canonical expected-scope source.

Both `atct watch` (Claude) and `atct codex monitor` must publish the same
leased `monitor_health` identity: `agent_session_id`/stable `agent_key`, role,
project, goal, task, process-start generation, and `scope_key`. The existing
`monitor_health` role/project/goal/task lease is the starting storage; the
Codex process registry remains process cleanup metadata and is not used for
scope matching. A live wrapper is one whose health lease names the active scope
and whose agent/session matches its rightful receiver. No matching health lease
at evaluation time yields `monitor_missing` for that scope generation.

### One delivery owner and failure semantics

Add persistent `orchestration_delivery_leases` keyed by `(scope_key,
target_role)`, containing holder monitor ID, fencing token, and expiry, plus an
append-only `orchestration_delivery_receipts` key `(scope_key, delivery_key,
generation)`. A monitor acquires/renews the lease transactionally only while
its health row matches the active scope. Only the holder can claim a delivery.

Claiming inserts a `reserved` receipt before writing to either transport. A
successful Claude write or accepted Codex `StartTurn` changes it to `accepted`.
Failure before acceptance releases the reservation for the same fenced owner;
lease expiry lets one new healthy monitor take ownership and retry the same
delivery ID. If the transport result is unknown (crash/timeout after submission),
the receipt becomes `unknown`: automatic retry is forbidden, and the durable
commander recovery route names that delivery for explicit inspection. This is
at-most-once accepted routing across wrappers, with no unbounded reconnect
replay; it does not falsely claim exactly-once agent execution without a target
acknowledgement.

### Canonical blockers and producers

Add `orchestration_blockers` with stable `blocker_id`, `scope_key`, kind,
source ID, generation, owner role, instruction, opened/resolved timestamps, and
unique `(kind, source_id, generation)`. It is the only source for blocker
routing.

- The decision store transaction is the producer for `human_decision`: opening
  a decision inserts/updates its blocker generation; answering, withdrawal, or
  default settlement resolves it in the same transaction.
- A new role-checked `blocker.report` daemon/MCP operation is the producer for
  `dependency_merge`. Commander or the owning subcommander supplies a stable
  source ID (for example required main commit plus dependent goal/task), owner,
  and instruction; retries upsert the same generation. The same owner calls
  `blocker.resolve` only after the prerequisite is actually integrated. It
  cannot be inferred from pane state or `git status`, and executors cannot
  create/resolve it.

The reconciliation API joins active scopes, live health, delivery receipt state,
and open blockers to produce the recovery envelope. Lifecycle completion is
represented as an `orchestration_scope` generation transition and uses the same
lease/receipt route to the role table in Revision 2.

### RED acceptance tests before production work

1. A Codex and a Claude health report with the same active scope identity;
   only the leased owner claims one delivery. Owner expiry fences the old owner;
   the replacement observes the existing receipt and does not duplicate an
   accepted action.
2. Missing, wrong-role, stale, and completed-scope health rows each produce the
   specified canonical result; only an active expected scope with no matching
   lease produces one commander `monitor_missing` instruction.
3. A post-submit timeout stores `unknown` and emits one commander inspection
   route, never automatic duplicate delivery.
4. Decision creation/settlement atomically opens/resolves its blocker. Repeated
   `blocker.report` keeps one merge blocker generation; executor attempts and
   pane/Git-only observations cannot create one.
5. Live SSE, reconcile, reconnect, and a second wrapper share the durable
   receipt result. Claude/Codex output has the same delivery ID, target, and
   order. Completed Task 1215's received approval regression remains green.

## Revision 4: durable review-work recovery

Review work is a separate stop condition from ordinary lifecycle completion.
The current handoff fields already give canonical generations
(`ReviewRequestedAt`, `ReviewReceivedAt`, `ReviewRejectedAt`), but event
formatting alone loses the work after a missed event or reviewer restart.

### Canonical review-work record

Add `orchestration_review_work`, keyed by `(kind, handoff_id,
review_requested_generation)`, with requester session, expected reviewer role,
reviewer scope key, state, received generation, settlement generation, and
active/inactive timestamps. The handoff transaction is its only producer:

| Handoff kind | `requested` state targets | `received` state targets | settlement transition |
| --- | --- | --- | --- |
| task | owning subcommander reviewer | recorded reviewing subcommander | reject → receiving executor to revise; complete → owning subcommander to close/redelegate |
| plan | project commander reviewer | recorded commander | reject → requesting subcommander to revise; complete → that subcommander to execute its approved plan |
| goal | project commander reviewer | recorded commander | reject → receiving subcommander to revise; complete → commander for goal-review path |

`review.request` atomically creates active `requested` work with its timestamp
as generation. `review.receive` atomically records the validated reviewer and
changes it to `received`. `review.reject` resolves that generation and creates
one reject-to-owner action; a re-request creates a new requested generation.
`handoff.complete` resolves the review work and creates the table's next-role
action. A completed/rejected handoff cannot leave active review work.

### Delivery and recovery contract

For either active state, reconciliation emits
`review_work:{kind}:{handoff_id}:{requested_or_received_generation}` through
the Revision 3 `(scope_key,target_role)` fenced delivery owner and durable
receipt. The recorded rightful reviewer scope—not an idle pane, generic
liveness, or the requester—receives exactly one actionable instruction:

- `requested`: receive the named review after role validation;
- `received`: finish that review by accepting or rejecting; no automatic
  acceptance/rejection occurs.

A reviewer monitor restart, reconnect, or a second wrapper sees the receipt
and does not duplicate an accepted action. If no matching reviewer wrapper is
live, the existing `monitor_missing` action targets the commander with the
review-work ID and monitored restart instruction. Rejection and completion
never resend the old review action: their new lifecycle generations route only
to the owner/next role stated above.

### Review-work RED acceptance tests

1. For task, plan, and goal reviews, a requested-but-unreceived row produces
one action to the recorded rightful reviewer; a fresh/restarted reviewer uses
the same durable receipt and sees no duplicate accepted action.
2. After `review.receive`, the unfinished-review action targets the recorded
reviewer once; a different session, requester, and executor cannot accept or
receive it as that reviewer.
3. `review.reject` resolves the old work, routes one revision action to the
proper owner, and a re-request gets a new generation. `handoff.complete`
resolves it and routes exactly one next-role action.
4. Claude and Codex process the same review-work delivery IDs and ordered
actions across live event, reconciliation, reconnect, owner failover, and
missing-wrapper recovery. No test permits automatic review settlement.

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
