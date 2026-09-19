# Goal 253: liveness action loss investigation

## Scope

This investigation covers the Goal 248 report: a fresh goal-scoped
subcommander received repeated `atct monitor liveness: recheck goal` actions
instead of acting on the earlier handoff-review state. It traces the watch
reconciliation, the shared `watchAgentAction` selector, the Codex bridge queue,
idle transitions, and session/role handoff state. No production code was
changed.

## Reproduction commands and results

The first test invocation was blocked before compilation because the sandbox
denied Aqua and Go's user cache writes. That is an environment failure, not a
test failure. The same read-only test command was then run with the normal Go
cache available:

```sh
go test ./cmd/atct -run 'Test(ReconcileWatchScopeSuppressesAppliedGoalApprovalAfterReceiptOrGoalClosure|ReconcileWatchScopeReprojectsAppliedGoalApprovalForCommander|CodexScopedLivenessQueuesUntilThreadIsIdle|CodexMonitorQueueDeliversFIFOAfterIdle|WatchPlanReviewDeliveryUsesLifecycleGeneration|ClaudeAndCodexAgentActionParity)$' -count=1 -v
```

Result: all six focused tests passed. The result confirms the current behavior
rather than disproving the incident: two of those tests encode the conflicting
delivery semantics described below.

## Observed delivery path

1. `reconcileWatchScope` reads canonical decisions and handoffs from
   `/api/events/reconcile` (`cmd/atct/watch.go:1163-1263`). A received/rejected
   handoff is reconstructed through `watchReconciliationHandoffEvent`; its
   lifecycle timestamp is used as the in-memory deduplication generation
   (`cmd/atct/watch.go:1267-1297`).
2. `writeWatchLineWithActionSink` calls the single shared
   `selectWatchAgentAction` selector before passing an action to a monitor
   (`cmd/atct/watch.go:1433-1449`, `cmd/atct/watch_action.go`). The selector
   accepts `goal.handoff.review.reject`, `plan.handoff.review.reject`, and
   `monitor.liveness`, so there is no selector-level exclusion of a review
   rejection. Claude and Codex share this selector.
3. Codex appends selected actions to the bridge queue. A queued action is sent
   only when the selected thread is idle; `turn/completed` and an idle
   `thread/status/changed` both call `pumpAfterIdle`
   (`cmd/atct/codex_monitor.go:680-759, 827-912`). The focused FIFO and liveness
   tests pass, so the ordinary idle transition is not the loss point.
4. Liveness is a separately selected action emitted after a successful scoped
   reconciliation. It can therefore continue to arrive while a prior action is
   not represented in the queue or is suppressed by reconciliation state
   (`cmd/atct/watch.go:936-944`).

## Reproduced defects

### 1. A received handoff suppresses the commander's applied approval

`shouldProjectAppliedGoalApproval` returns false whenever *any* handoff for an
active goal has `ReceivedAt` (`cmd/atct/watch.go:1452-1477`). The focused test
`TestReconcileWatchScopeSuppressesAppliedGoalApprovalAfterReceiptOrGoalClosure`
asserts an empty output for its `received` case (`cmd/atct/watch_test.go:300-346`).

That condition conflates two facts:

- the subcommander has received the handoff; and
- the commander has observed the applied goal-approval transition.

They are independent. Thus a fresh/reconnecting commander can lose the only
action that authorizes it to continue its workflow once `ReceivedAt` already
exists. This directly violates the added Goal 253 acceptance condition.

### 2. Reconciliation currently repeats the same approval indefinitely

The matching positive test invokes reconciliation twice and expects the same
`decision approved (decision_id: 71)` line twice
(`TestReconcileWatchScopeReprojectsAppliedGoalApprovalForCommander`,
`cmd/atct/watch_test.go:264-298`). The implementation deliberately bypasses
`delivered`, the watch's in-memory delivery map, for this projection
(`cmd/atct/watch.go:1205-1212`). A quiet reconnect/reconcile loop therefore
keeps enqueueing the same approval. This is the duplicate-notification class
represented by decision 700 and must not be retained.

### 3. Queue pruning can discard the same approval before it reaches Codex

Goal 249 added `pruneQueuedApprovalsLocked`: on `goal.handoff.receive` it drops
queued `decision.approved` actions with the same goal ID
(`cmd/atct/codex_monitor.go:689-708`). The prior Goal 249 design intended this
to remove an approval made irrelevant by handoff receipt. Under the new
acceptance condition, receipt cannot make the commander's approval transition
irrelevant. The bridge can therefore turn a successfully selected action into a
lost action while the thread is active.

## What was ruled out

- **Selector mismatch:** ruled out. `TestClaudeAndCodexAgentActionParity` passes
  and the selector accepts all three handoff-review lifecycle states for both
  transports.
- **Lifecycle deduplication erasing a reject/re-request cycle:** ruled out for
  plan handoffs. `TestWatchPlanReviewDeliveryUsesLifecycleGeneration` passes;
  generation is the lifecycle timestamp, so a changed lifecycle state is not
  confused with the prior state.
- **Basic Codex idle wakeup:** ruled out. The liveness and FIFO tests pass and
  both completion and idle-status notifications call the same pump.

The review-rejection incident still needs a focused end-to-end regression:
current component tests prove it is selected and queued, but do not exercise a
fresh goal-scoped monitor reconciling an already-rejected handoff while a
liveness action is also eligible. The corrective spec must require that test.

## Root cause and design boundary

The action loss is not an SSE-event-only problem. Recovery is based on a
canonical reconciliation snapshot, but Goal 249 added two state-based
shortcuts that are incompatible with durable action delivery: a receipt-based
approval suppression and receipt-based queue pruning. At the same time, the
approval projection bypasses the normal in-memory delivery generation and
causes unlimited repeats.

The correction must model each state transition as one selected action per
monitor lifecycle: reconciliation may recover an unseen transition, but a
subsequent reconnect/reconcile in that lifecycle must reuse the normal delivery
key. It must keep goal-scoped handoff review state deliverable even when
liveness is eligible, and must use the existing shared selector rather than a
transport-specific Codex rule.

## Acceptance criteria carried into the spec and plan

1. A commander receives one `decision.approved` action for an active goal even
   when that goal's handoff already has `ReceivedAt`.
2. Repeated reconcile/reconnect cycles do not resend the same approval action
   indefinitely; decision 700's duplicate-notification behavior is not
   reintroduced.
3. A fresh goal-scoped subcommander receives its current handoff review
   rejection/actionable state before, and independently from, liveness rechecks.
4. Focused tests prove the same selection contract for Claude and Codex and
   cover queue/idle delivery without cancelling the required approval.
