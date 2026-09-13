# Goal 271 Review Notification Deduplication Implementation Plan

**Goal:** Prevent stale queued review-request/received actions from re-notifying a Codex commander after a later lifecycle state for the same handoff is persisted.

**Architecture:** Preserve store event production and reconciliation projection. Carry reconciliation's RFC3339Nano generation to the bridge and compare it only among queued `*.handoff.review.*` actions for one handoff. A strictly newer generation replaces a queued older one, an equal generation is deduplicated, and a delayed older generation is discarded; the active action remains immutable.

**Tech Stack:** Go, existing `cmd/atct` monitor bridge tests.

## Global Constraints

- Do not change Goal 269 lifecycle ordering, Goal 270 activation, Goal 246 records, producer persistence, or general reconciliation.
- Do not use arrival order or phase-name ordering. Do not suppress a valid pending review, a distinct handoff, a newer retry/rejection generation, or the active action.

---

### Task 1: Codex monitor lifecycle queue

**Files:**

- Modify: `cmd/atct/codex_monitor.go`
- Modify: `cmd/atct/codex_monitor_test.go`
- Commit: this plan and spec together with the implementation paths above after accepted task review.

**Interfaces:**

- Consumes: typed `codexMonitorAction` values with `eventName`, `goalID`, `handoffID`, `deliveryKey`, and RFC3339Nano `generation`.
- Produces: bridge queue behavior that retains only the greatest-generation queued `*.handoff.review.*` action for a handoff, while preserving the active action and unrelated queue entries.

- [ ] **Step 1: Write the failing queue regression**

Add a test that begins with an unrelated active action, queues `goal.handoff.review.request` for `handoff-1` with generation `2026-09-13T16:21:24.116018Z`, then queues `goal.handoff.review.receive` for `handoff-1` with generation `2026-09-13T16:23:47.661516Z`, plus an action for `handoff-2`. Complete turns and assert that `handoff-1` injects only the receipt while `handoff-2` remains.

- [ ] **Step 2: Run the focused regression before the fix**

Run: `GOCACHE=/private/tmp/goal271-go-cache go test ./cmd/atct -run 'TestCodexMonitor.*(Review|Queue|Lifecycle)' -count=1`

Expected: FAIL because the bridge submits the obsolete request before the receipt.

- [ ] **Step 3: Implement the minimal queue supersession**

Carry the existing `watchAgentAction.generation` and handoff ID into `codexMonitorAction`. In `enqueueAction`, identify typed `*.handoff.review.*` actions by handoff ID. Parse their RFC3339Nano generations: remove queued entries only when their generation is strictly earlier; suppress an equal generation; discard an incoming strictly earlier generation. Do not use phase-name ordering or arrival order. Do not change `activeAction` and do not remove actions for another handoff or non-review-lifecycle action.

- [ ] **Step 4: Add reconciliation coverage**

Drive the bridge through reconciliation snapshots for requested then received state while busy. Assert only the received action is injected after idle, and that repeating the received snapshot adds no action. Add a retry snapshot whose later `ReviewRequestedAt` replaces an earlier rejection action, and a rejection-receipt snapshot whose later generation remains after a delayed older request snapshot; assert each later action is injected exactly once.

- [ ] **Step 5: Run verification**

Run: `GOCACHE=/private/tmp/goal271-go-cache go test ./cmd/atct -run 'TestCodexMonitor.*(Review|Queue|Lifecycle)|TestWatch.*Review' -count=1`

Expected: PASS.

Run: `GOCACHE=/private/tmp/goal271-go-cache go test ./cmd/atct -count=1`

Expected: PASS.

- [ ] **Step 6: Commit after review acceptance**

Commit only `cmd/atct/codex_monitor.go`, `cmd/atct/codex_monitor_test.go`, `doc/specs/2026-09-14-goal-271-review-notification-dedup.md`, and `doc/plans/2026-09-14-goal-271-review-notification-dedup.md` with an explicit-path `git add`.

## Acceptance Criteria

- The Goal 246 persisted sequence is reproducible in focused tests.
- A stale queued review request cannot arrive after its receipt for the same handoff.
- Valid pending requests, distinct handoffs, retry/reject transitions, and live/reconciliation deduplication remain covered.
