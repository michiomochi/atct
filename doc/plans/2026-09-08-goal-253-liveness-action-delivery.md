# Goal 253 Liveness Action Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recover every required monitor action once per monitor lifecycle without suppressing a received-handoff approval or repeatedly re-delivering it during reconciliation.

**Architecture:** Keep reconciliation as the canonical recovery source and route applied goal approvals through the existing decision delivery map rather than a direct writer. Keep the shared `watchAgentAction` selector as the only transport eligibility rule; make the Codex bridge FIFO-only by removing its receipt-based approval pruning.

**Tech Stack:** Go; `cmd/atct` watch, scope, and Codex monitor focused tests.

> **Revision gate (2026-09-08):** Task 1 is complete in commit `c75013e`.
> The remaining tasks are superseded by the scope-expanded plan below and may
> not be delegated until this revision has a new plan-handoff approval. In
> particular, Task 1216 remains unclaimed.

## Global Constraints

- Before the revision gate, modify only `cmd/atct/watch.go`,
  `cmd/atct/codex_monitor.go`, and focused tests under `cmd/atct/`. The
  scope-expanded tasks name their additional selector/status files explicitly.
- Do not add persistent cursors, acknowledgements, SSE replay, or daemon calls from the bridge.
- Preserve action selection through `selectWatchAgentAction` for both Claude and Codex.
- `ReceivedAt` must not suppress an active goal's commander approval action.
- The same applied approval may be recovered once by a fresh monitor but must not repeat during reconnect/reconcile of that monitor.
- Run tests with `GOCACHE=/private/tmp/goal253-go-cache` if sandbox cache writes are unavailable.

---

### Task 1: Deduplicate recovered commander approvals

**Files:**

- Modify: `cmd/atct/watch.go:1205-1212,1452-1475`
- Modify: `cmd/atct/watch_test.go:264-346`

**Interfaces:**

- Consumes: `watchDecision`, `watchReconciliation`, `watchDeliveryKey`, and `emitWatchDecisionWithStateAndSinks`.
- Produces: one `decision.approved` action per `(eventName, decisionID, defaultApplied)` in a monitor lifecycle.

- [ ] **Step 1: Replace the direct approval projection test with the received-handoff recovery case.**

  In `TestReconcileWatchScopeReprojectsAppliedGoalApprovalForCommander`, use an active goal whose `goal_handoffs` entry includes both `RequestedAt` and `ReceivedAt`. Pass a collecting `watchAgentActionSink` to two calls of `reconcileWatchScope` that share `delivered` and `detectionDelivered`. Assert one rendered line and one action:

  ```go
  if got, want := output.String(), "atct decision approved (decision_id: 71)\\n"; got != want {
      t.Fatalf("approval output = %q, want %q", got, want)
  }
  if got, want := actions, []watchAgentAction{{
      line: "atct decision approved (decision_id: 71)", eventName: "decision.approved", goalID: "42",
  }}; !reflect.DeepEqual(got, want) {
      t.Fatalf("approval actions = %#v, want %#v", got, want)
  }
  ```

- [ ] **Step 2: Run the changed test before implementation.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal253-go-cache go test ./cmd/atct -run 'TestReconcileWatchScope(ReprojectsAppliedGoalApprovalForCommander|SuppressesAppliedGoalApprovalAfterReceiptOrGoalClosure)$' -count=1 -v
  ```

  Expected: FAIL because current receipt suppression produces zero actions, while the current no-handoff projection repeats twice.

- [ ] **Step 3: Route applied approval through normal decision delivery.**

  In `reconcileWatchScope`, replace the direct call:

  ```go
  writeWatchDecisionLine(out, "decision.approved", decision, sink, actionSink)
  ```

  with:

  ```go
  emitWatchDecisionWithStateAndSinks(out, "decision.approved", decision,
      delivered, lastWakeupContent, wakeupDiscrepancyDelivered,
      detectionDelivered, sink, actionSink)
  ```

  Keep the `continue`. In `shouldProjectAppliedGoalApproval`, retain project
  scope, `goal_approval`, applied status, non-empty goal ID, and active-goal
  checks; remove the `state.GoalHandoffs` loop and its `ReceivedAt` condition.

- [ ] **Step 4: Replace obsolete suppression expectations.**

  Rename `TestReconcileWatchScopeSuppressesAppliedGoalApprovalAfterReceiptOrGoalClosure` to describe eligibility. Assert one approval for `no handoff`, `requested only`, and `received`; retain zero for `done` and `dropped`.

- [ ] **Step 5: Verify the delivery contract.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal253-go-cache go test ./cmd/atct -run 'TestReconcileWatchScope.*AppliedGoalApproval|TestClaudeAndCodexAgentActionParity' -count=1 -v
  ```

  Expected: PASS. The two-reconciliation test proves no decision-700-style repeat in one monitor lifecycle.

- [ ] **Step 6: Commit Task 1.**

  ```sh
  git add cmd/atct/watch.go cmd/atct/watch_test.go
  git commit -m "fix: dedupe recovered goal approvals"
  ```

### Task 2: Preserve queued approvals and review-reject recovery

**Files:**

- Modify: `cmd/atct/codex_monitor.go:689-708`
- Modify: `cmd/atct/codex_monitor_test.go:828-999`
- Modify: `cmd/atct/watch_test.go`

**Interfaces:**

- Consumes: `watchAgentAction{line, eventName, goalID}` emitted by the shared selector.
- Produces: FIFO `codexMonitorBridge` submissions; a goal-scoped recovery sequence where review rejection precedes liveness.

- [ ] **Step 1: Replace the pruning regression with FIFO preservation.**

  Rewrite `TestCodexMonitorQueuePrunesQueuedApprovalAfterGoalHandoffReceive` so the active bridge queues:

  ```go
  approval := watchAgentAction{line: "atct decision approved (decision_id: 71)", eventName: "decision.approved", goalID: "42"}
  receipt := watchAgentAction{line: "atct goal handoff received (goal_id: 42, handoff_id: h1)", eventName: "goal.handoff.receive", goalID: "42"}
  ```

  After the first `turn/completed`, assert `StartTurn` received `approval.line`.
  After the next idle notification, assert the second call received
  `receipt.line`. Assert both actions were queued before the first idle.

- [ ] **Step 2: Run the queue test before implementation.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal253-go-cache go test ./cmd/atct -run 'TestCodexMonitorQueue(PrunesQueuedApprovalAfterGoalHandoffReceive|KeepsActiveApprovalAfterGoalHandoffReceive)$' -count=1 -v
  ```

  Expected: FAIL because `pruneQueuedApprovalsLocked` removes `approval` before the first idle transition.

- [ ] **Step 3: Remove receipt-driven queue policy.**

  Delete the `goal.handoff.receive` conditional from `enqueueAction` and delete
  `pruneQueuedApprovalsLocked`. Do not change the `activeAction` reservation,
  failed-submission retention, `turn/completed`, or `thread/status/changed`
  paths.

- [ ] **Step 4: Add a fresh goal-scope reconciliation regression.**

  Add a `cmd/atct/watch_test.go` case with a monitor scope:

  ```go
  watchScope{Role: "subcommander", ProjectID: "1", GoalID: "42"}
  ```

  and reconciliation JSON containing:

  ```json
  {"goal_handoffs":[{"ID":"h1","GoalID":42,"ReviewRejectedAt":"2026-09-08T00:02:00Z"}]}
  ```

  Collect `watchAgentAction` values through `reconcileWatchScope`, then emit
  one liveness line with `writeWatchLineWithActionSink`. Assert the ordered
  actions are:

  ```go
  []watchAgentAction{
      {line: "atct goal handoff review rejected (goal_id: 42, handoff_id: h1)", eventName: "goal.handoff.review.reject", goalID: "42"},
      {line: "atct monitor liveness: recheck goal 42", eventName: "monitor.liveness", goalID: "42"},
  }
  ```

  Feed the same action slice to a Claude collecting sink and a Codex bridge
  with an active fake thread. Complete/idle the bridge twice and assert both
  transports observe the identical order.

- [ ] **Step 5: Verify queue and shared-selection behavior.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal253-go-cache go test ./cmd/atct -run 'Test(CodexMonitorQueue.*Approval.*GoalHandoffReceive|CodexScopedLivenessQueuesUntilThreadIsIdle|FreshGoalScope.*ReviewReject.*Liveness|ClaudeAndCodexAgentActionParity)$' -count=1 -v
  ```

  Expected: PASS. The review-reject action is preserved and liveness cannot replace it.

- [ ] **Step 6: Commit Task 2.**

  ```sh
  git add cmd/atct/codex_monitor.go cmd/atct/codex_monitor_test.go cmd/atct/watch_test.go
  git commit -m "fix: preserve monitor action delivery order"
  ```

### Task 3: Run focused and package verification

**Files:**

- Modify: none unless a preceding verification identifies a test-only correction.

**Interfaces:**

- Consumes: Tasks 1 and 2's regressions.
- Produces: verification evidence for the accepted delivery contract.

- [ ] **Step 1: Run the complete focused suite.**

  ```sh
  GOCACHE=/private/tmp/goal253-go-cache go test ./cmd/atct -run 'Test(ReconcileWatchScope.*AppliedGoalApproval|FreshGoalScope.*ReviewReject.*Liveness|CodexMonitorQueue.*Approval.*GoalHandoffReceive|CodexScopedLivenessQueuesUntilThreadIsIdle|ClaudeAndCodexAgentActionParity|WatchPlanReviewDeliveryUsesLifecycleGeneration)$' -count=1 -v
  ```

  Expected: PASS.

- [ ] **Step 2: Run the package suite.**

  ```sh
  GOCACHE=/private/tmp/goal253-go-cache go test ./cmd/atct -count=1
  ```

  Expected: PASS.

- [ ] **Step 3: Inspect the final change set.**

  ```sh
  git diff --check
  git status --short
  ```

  Expected: no whitespace errors; only the files named in Tasks 1–2 are changed.

- [ ] **Step 4: Commit Task 3 documentation/test-only correction if needed.**

  If verification requires a correction, stage the known delivery files and
  commit:

  ```sh
  git add cmd/atct/watch.go cmd/atct/codex_monitor.go cmd/atct/watch_test.go cmd/atct/codex_monitor_test.go
  git commit -m "test: cover monitor action recovery"
  ```

  Otherwise, make no empty commit.

---

## Scope-expanded implementation sequence (requires new approval)

### Task 2R: Preserve queue order and make plan completion actionable

**Files:** `cmd/atct/codex_monitor.go`, `cmd/atct/codex_monitor_test.go`,
`cmd/atct/watch_action.go`, `cmd/atct/watch_action_test.go`, and only required
`cmd/atct/watch*_test.go` coverage.

1. RED: prove an approval queued before `goal.handoff.receive` survives until
   its turn starts; prove `plan.handoff.complete` is selected by the shared
   selector for both Claude and Codex; prove live plus reconciliation emits one
   plan-complete action for its handoff/timestamp generation.
2. GREEN: remove receipt-triggered queue pruning, add plan-complete selector
   membership, and route only through the existing delivery state. Do not add a
   second Codex-only rule or persistent acknowledgement.
3. Verify queue FIFO, retry-on-idle behavior, selector parity, and no duplicate
   completion action on reconnect.

### Task 3R: Keep actionable recovery independent from decision-suppressed liveness

**Files:** focused `cmd/atct/watch_test.go` and `cmd/atct/watch_action_test.go`;
production files only if RED demonstrates an actual coupling.

1. Build a goal-scoped snapshot containing an open decision and a fresh
   `goal.handoff.review.reject` generation.
2. Assert the rejection is selected/delivered once even while `PromptDue` is
   false; assert no liveness action is substituted for it.
3. Feed the same typed sequence to Claude and Codex collectors and assert the
   exact order is identical.

### Task 4R: Add read-only monitor outcome diagnostics

**Files:** the existing monitor lifecycle/status command and its focused tests,
identified from fresh source reconnaissance before coding.

1. Record a compact, read-only outcome model: monitor-record coverage,
selector admission, queued/started state, delivery-key suppression, and
role/session mismatch diagnostics.
2. Provide a status surface that reports those facts per scope without adding a
   monitor to an old worktree, waking an agent, changing handoff ownership, or
   modifying a decision/claim.
3. Exercise monitor-present, monitor-absent, selected-and-queued,
dedupe-suppressed, and role-mismatch fixtures. Reuse Goal 221's lease facts
rather than reimplementing heartbeat ownership.

### Task 5R: Regression verification and integration review

1. Run the focused Task 1, 2R, and 3R selectors/queue/reconciliation tests and
the diagnostics tests, all with a fresh `GOCACHE`.
2. Run `go test ./cmd/atct -count=1`.
3. Review the final diff specifically against Goals 182, 203, 221, 227, 228,
237, 240, 245, 248, and 252. Reject any duplicate health, lease, lifecycle,
migration, or dependency implementation.
4. Commit each accepted task as a separate safe unit; no empty verification
commit.
