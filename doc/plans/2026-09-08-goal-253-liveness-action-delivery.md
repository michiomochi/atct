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

> **Superseded by Revision 2 below.** Tasks 2R–5R are retained as audit
> history, not delegation authority. Revision 2 replaces diagnostics-only
> handling with end-to-end rightful-role recovery.

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

---

## Revision 2 implementation sequence (requires new approval)

> **Superseded by Revision 3 below.** Revision 2 did not identify persistent
> expected-scope identity, a cross-wrapper delivery owner, or a canonical merge
> blocker producer. It is retained only as review history.

### Task 2V2: Canonical recovery envelope and shared delivery contract

**Files:** identify the existing watch reconciliation/action types and focused
tests before editing; expected `cmd/atct/watch*.go` and monitor bridge tests.

1. RED: fixture each canonical condition with subject ID, target role, and
generation; show live plus reconciliation/reconnect currently loses or repeats
the action.
2. Add one typed recovery envelope and one shared delivery key containing
condition, target role, subject, and generation. Keep
`selectWatchAgentAction` as the sole membership rule for Claude and Codex.
3. Test that two wrappers and reconnect deliver once per generation, while a
new handoff/blocker/registration/completion generation delivers once anew.

### Task 3V2: Commander-safe receiver/session and missing-wrapper recovery

**Files:** reuse Goal 203/221 canonical state interfaces and their monitor
registration/lease paths; add focused integration tests only at that boundary.

1. RED: construct a role-mismatched receiver and an active registered scope
without a live wrapper. Assert neither receiver obtains elevated authority and
the commander currently lacks a durable recovery action.
2. Route canonical mismatch and wrapper-missing states to a commander envelope
with the expected/observed role, stable scope/handoff ID, generation, and an
exact supported restart/reissue instruction.
3. Test valid-live receiver preservation, no executor impersonation, wrapper
registration clearing the missing condition, and Claude/Codex identical output.
Do not change Goal 203's key discovery or Goal 221's lease/takeover internals.

### Task 4V2: Exactly-once blocker and rightful-lifecycle routing

**Files:** shared workflow reconciliation/scope/action code and focused tests;
reuse Goal 227/228/237/240/245/252 canonical records rather than new stores.

1. RED: show an open decision or dependency/merge blocker suppresses liveness
without a durable commander instruction, and that plan completion is unselected.
2. Emit one commander-targeted blocker envelope per decision/blocker generation;
the original agent remains blocked. Select task/plan/goal completion only for
the role shown in the spec's lifecycle table.
3. Preserve FIFO prerequisite actions across receipt, use normal delivery
dedupe, and test live/reconcile/reconnect non-duplication plus a changed
generation fresh action.

### Task 5V2: Parity and no-silent-stop regression suite

1. Run focused RED-to-GREEN tests for recipient mismatch, no wrapper, human
decision, dependency/merge blocker, task/plan/goal lifecycle, queued approval,
and Decision 700 non-duplication.
2. Assert both Claude and Codex receive the same typed action sequence for
every scenario. Include a test that liveness suppression does not suppress an
actionable recovery envelope.
3. Run `go test ./cmd/atct -count=1` and only the owning package tests required
by reused Goal 203/221/227 state boundaries. Review for duplicate lease,
session-key, migration, and lifecycle implementations before each safe-unit
commit.

---

## Revision 3 implementation sequence (requires new approval)

> **Extended by Revision 4 below.** Revision 3's scope, lease, receipt, and
> blocker work remains required; Revision 4 adds review work to that same
> model, not a second reminder path.

### Task 2V3: Persist expected scopes and monitor identity

**Files:** Goal 221's monitor-health store/schema/API boundary, both Claude and
Codex monitor reporters, reconciliation DTOs, generated SQL artifacts, and
focused store/daemon/monitor tests.

1. RED: show process-only `CodexMonitorRecord` cannot match an active
subcommander/executor scope, and an unscoped/foreign/stale health lease cannot
satisfy an expected scope.
2. Create lifecycle-owned `orchestration_scope` records from project claim,
received goal handoff, and received task handoff transitions. Include stable
scope key, rightful session/role, source generation, and active/inactive state.
3. Extend the common Claude/Codex monitor-health report with the expected scope
key and stable session identity. Match only live health whose identity equals
the active rightful scope. Keep the Codex process registry process-only.
4. Test claim/handoff completion and rejection transitions, stale/foreign rows,
and an active expected scope without matching health yielding `monitor_missing`.

### Task 3V3: Fence a single delivery owner and persist receipts

**Files:** store migration/query/API, watch reconciliation/action delivery,
Claude writer and Codex bridge integration, focused store/daemon/cmd tests.

1. RED: two healthy wrappers for one scope each emit the same action because
their in-memory maps are independent; a replacement after owner crash has no
durable answer.
2. Implement transactional `(scope_key,target_role)` delivery leases with
fencing tokens and expiry, and `reserved` / `accepted` / `unknown` receipts
unique by scope, delivery key, and generation.
3. Allow only the current fenced holder to submit. On accepted transport result
persist `accepted`; on pre-submit failure release/retry under the same owner;
on unknown post-submit result persist `unknown` and route one commander
inspection instruction, never automatic retry.
4. Test owner failover, stale-owner fencing, second-wrapper suppression,
reconnect, acceptance persistence, and unknown-result non-duplication for both
Claude and Codex.

### Task 4V3: Produce canonical blockers and route rightful lifecycle recovery

**Files:** decision store transaction, new blocker store/API/MCP surface,
reconciliation and shared selector, focused store/daemon/cmd tests.

1. RED: a decision can suppress liveness without a durable commander route;
merge dependency can only be guessed from a pane or Git; plan completion is not
selected.
2. Add `orchestration_blockers`. Create/resolve human-decision blockers in the
same decision transaction. Add commander/owning-subcommander-only idempotent
`blocker.report` and `blocker.resolve` for dependency/merge source IDs;
executors are denied.
3. Join active blockers and lifecycle completion generations into the fenced
delivery path. Route blockers to commander once; leave workers blocked. Route
task/plan/goal completion only to their rightful roles. Include plan completion
in the shared selector.
4. Test repeats, settlement/resolution, unauthorized producer, live/reconcile
parity, receipt dedupe, and changed-generation fresh delivery.

### Task 5V3: focused end-to-end verification

1. Run the RED-to-GREEN tests from Tasks 2V3–4V3 with fresh cache, including
both monitor transports and both owner-failover paths.
2. Run affected store/daemon/MCP packages and `go test ./cmd/atct -count=1`.
3. Run `git diff --check`. Review explicitly for Goal 182/203/221/227/228/237/
240/245/248/252 duplication before each safe-unit commit. Preserve c75013e's
received-approval guarantee throughout.

---

## Revision 4 implementation sequence (requires new approval)

### Task 5V4: Persist review-work state with handoff transitions

**Files:** task/goal/plan handoff store transactions and migrations, workflow
events/reconciliation DTO, generated SQL, and focused store/daemon tests.

1. RED: requested-but-unreceived and received-but-unsettled task/plan/goal
reviews have only transient watch events and are lost after reviewer restart.
2. Add `orchestration_review_work` with kind, handoff ID, requester, expected
reviewer role/scope, requested/received/settlement generations and active state.
Update it atomically in every review request/receive/reject/complete handoff
transaction. Validate table-specific rightful reviewers at the existing
authority boundary.
3. Test request, receipt, reject/re-request, completion, foreign reviewer,
reviewer restart, and no active orphan after final settlement for all three
handoff kinds.

### Task 6V4: Route review work through fenced delivery

**Files:** Revision 3 reconciliation/action/lease/receipt integration and
Claude/Codex focused monitor tests.

1. RED: no durable targeted action exists for both active review-work states;
plan completion does not wake the approved-plan subcommander.
2. Map active review work to the existing fenced owner and receipt path. Select
one instruction to the specified reviewer for `requested` and `received`; do
not add a timer-only or raw-line-only reminder.
3. On rejection route only the revision owner; on completion route only the
next rightful role. Join missing reviewer wrapper to the same commander
monitor-missing route with review-work identity.
4. Test live/reconcile/reconnect, second wrapper, owner failover, unknown
receipt, reviewer restart, and no automatic accept/reject for task/plan/goal.

### Task 7V4: complete focused no-silent-stop verification

1. Run all Revision 3 tests plus the review-work matrix, asserting Claude/Codex
typed action parity and durable non-duplication for every state transition.
2. Run affected store/daemon/MCP packages and `go test ./cmd/atct -count=1`.
3. `git diff --check` must pass. Preserve Task 1215's regression and reject any
parallel delivery/reminder implementation outside the fenced receipt model.
