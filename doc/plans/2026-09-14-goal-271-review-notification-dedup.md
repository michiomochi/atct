# Goal 271 Review Notification Deduplication Implementation Plan

**Goal:** Prevent review receipts from creating duplicate agent notifications in both Claude and Codex monitors, while allowing Codex to cancel a stale queued review request.

**Architecture:** Preserve store production and reconciliation. Add a control-only classification for `*.handoff.review.receive` in the shared watch action. The Claude writer skips control-only actions. The Codex bridge uses a control-only receipt and its RFC3339Nano generation to delete only older queued review actions for that handoff, then never injects the receipt. Agent-facing retries and rejection lifecycle actions keep their current delivery behavior.

**Tech Stack:** Go, existing `cmd/atct` monitor bridge tests.

## Global Constraints

- Do not change Goal 269 lifecycle ordering, Goal 270 activation, Goal 246 records, producer persistence, or general reconciliation.
- Do not use arrival order or phase-name ordering. Do not suppress a valid pending review, a distinct handoff, a newer retry/rejection generation, or the active action.

---

### Task 1: Shared action classification and monitor consumers

**Files:**

- Modify: `cmd/atct/codex_monitor.go`
- Modify: `cmd/atct/codex_monitor_test.go`
- Modify: `cmd/atct/watch_action.go`
- Modify: `cmd/atct/watch_action_test.go`
- Modify: `cmd/atct/watch_test.go`
- Commit: this plan and spec together with the implementation paths above after accepted task review.

**Interfaces:**

- Consumes: shared `watchAgentAction` values carrying event name, handoff ID, RFC3339Nano generation, and a control-only flag.
- Produces: no Claude/Codex agent turn for a review receipt; Codex removes stale queued review work for that handoff while preserving unrelated and active work.

- [ ] **Step 1: Write failing cross-harness regressions**

Assert the shared selector marks only `*.handoff.review.receive` control-only; review request/retry and rejection lifecycle actions remain agent-facing. Drive a Claude monitor writer with a request then receipt and assert it writes the request but not the receipt. Drive a busy Codex bridge with an earlier queued request, a later receipt, and another handoff; assert the receipt injects nothing, the stale request is removed, and the other handoff remains.

- [ ] **Step 2: Run the focused regression before the fix**

Run: `GOCACHE=/private/tmp/goal271-go-cache go test ./cmd/atct -run 'Test(CodexMonitor|Watch).*Review' -count=1`

Expected: FAIL because review receipts are currently delivered as agent actions in both consumers.

- [ ] **Step 3: Implement the shared control action**

Carry the control-only flag through the existing action conversion. Make the Claude writer skip it. In the Codex queue, parse review generations and remove only strictly older queued review actions for the same handoff when control arrives; deduplicate equal or delayed older generations and do not enqueue/inject control. Do not use phase-name ordering or arrival order. Do not change `activeAction`, another handoff, or non-review actions.

- [ ] **Step 4: Add reconciliation and ordering coverage**

Drive both consumers through reconciliation snapshots for requested then received state, a delayed old request snapshot, and repeated receipt. Assert neither monitor receives a receipt turn and Codex injects no stale queued request. Add a later retry request and rejection/rejection-receipt sequence; assert valid agent-facing events are delivered once. Include a distinct handoff to prove it is not suppressed.

- [ ] **Step 5: Run verification**

Run: `GOCACHE=/private/tmp/goal271-go-cache go test ./cmd/atct -run 'Test(CodexMonitor|Watch).*Review' -count=1`

Expected: PASS.

Run: `GOCACHE=/private/tmp/goal271-go-cache go test ./cmd/atct -count=1`

Expected: PASS.

- [ ] **Step 6: Commit after review acceptance**

Commit only the listed watch/Codex files and these spec/plan files with an explicit-path `git add`.

## Acceptance Criteria

- The Goal 246 persisted sequence is reproducible in focused tests.
- A review receipt is not emitted as a Claude or Codex agent notification.
- A stale queued review request cannot arrive after its receipt for the same handoff in Codex.
- Valid pending requests, distinct handoffs, retry/reject transitions, and live/reconciliation deduplication remain covered.
