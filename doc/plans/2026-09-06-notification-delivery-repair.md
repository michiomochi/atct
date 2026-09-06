# Notification delivery repair implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver each plan-review lifecycle cycle once and prevent stale queued approvals from starting after a goal handoff is received.

**Architecture:** Preserve watch text output while adding an optional structured action sink for the Codex monitor bridge. Version handoff dedup keys by lifecycle timestamp and use the structured goal ID to prune only pending approvals invalidated by a receive event.

**Tech Stack:** Go, existing `cmd/atct` watch and Codex monitor tests.

## Global Constraints

- Modify only `cmd/atct/watch.go`, `cmd/atct/codex_monitor.go`, and their focused tests.
- Keep SSE live-only and reconciliation as recovery; add neither persistence nor daemon calls from bridge pump.
- Workers do not commit; test commands use a task-specific `GOCACHE` under `/private/tmp`.

---

### Task 1: Version plan-review delivery keys

**Files:**
- Modify: `cmd/atct/watch.go`, `cmd/atct/watch_scope_test.go`

**Interfaces:**
- Produces a lifecycle-generation component for `watchDetectionDeliveryKey`.

- [ ] Add a generation field to the handoff detection key and populate it from the timestamp representing the formatted lifecycle event.
- [ ] Add a focused long-lived-watch test for `request → receive → reject → request` with one HandoffID; assert two request lines and no duplicate within either cycle.
- [ ] Run: `GOCACHE=/private/tmp/goal251-repair-cache go test ./cmd/atct -run 'TestWatchFormatsHandoffReviewEvents|Test.*Plan.*Review.*' -count=1`.

### Task 2: Pass structured watch actions to the Codex bridge

**Files:**
- Modify: `cmd/atct/watch.go`, `cmd/atct/codex_monitor.go`, `cmd/atct/codex_monitor_test.go`

**Interfaces:**
- `codexMonitorAction{line, eventName, goalID}` replaces a queue of bare strings internally.
- Existing line-only writers retain their current output.

- [ ] Add an optional structured sink used only by the Codex monitor path; create actions at the same point rendered lines are emitted.
- [ ] Store actions in the bridge queue and keep `StartTurn` input equal to `action.line`.
- [ ] On `goal.handoff.receive`, remove queued `decision.approved` actions with the same non-empty goal ID before queuing the receive event.
- [ ] Add bridge tests: queued same-goal approval is not started after receive and idle; a different-goal approval remains FIFO; an active approval is not cancelled.
- [ ] Run: `GOCACHE=/private/tmp/goal251-repair-cache go test ./cmd/atct -run 'TestCodexMonitor.*|TestReconcileWatchScope.*' -count=1`.

### Task 3: Integrate and verify

**Files:**
- Modify: focused files from Tasks 1–2 only.

- [ ] Run the focused command above plus `GOCACHE=/private/tmp/goal251-repair-cache go test ./cmd/atct -count=1`.
- [ ] Inspect `git diff --check` and `git status --short`; report every changed path and any sandbox limitation.
