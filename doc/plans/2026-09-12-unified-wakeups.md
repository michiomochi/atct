# Unified Wakeups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Track every Wakeup in one Wakeup tracker.

**Architecture:** `wakeupTracker` stores one Wakeup state per key. `publishWakeup` receives the key, start time, initial wait, optional resend interval, and current time; it decides whether to publish and updates the shared state.

**Tech Stack:** Go, existing `internal/daemon/wakeup.go` and Go tests.

## Global Constraints

- Keep all existing event names and payload types, including `wakeup.*`.
- Do not add database state or dependencies.
- Keep current wait and resend timings exactly.
- Delete obsolete parallel tracker maps after the refactor.

---

### Task 1: Prove the common Wakeup policy

**Files:**
- Modify: `internal/daemon/wakeup_test.go`
- Modify: `internal/daemon/wakeup.go`

**Interfaces:**
- Produces: `wakeupTracker.publishWakeup(now, key, startedAt, initialWait, resendInterval) bool`.
- `resendInterval == 0` means publish at most once while the wakeup remains active.

- [x] **Step 1: Write a failing tracker test.**

```go
func TestWakeupPublishesOnceOrResendsByPolicy(t *testing.T) {
    tracker := newWakeupTracker(now)
    if tracker.publishWakeup(now.Add(3*time.Minute), "once", now, 3*time.Minute, 0) == false { t.Fatal("first publish") }
    if tracker.publishWakeup(now.Add(6*time.Minute), "once", now, 3*time.Minute, 0) { t.Fatal("one-shot wakeup repeated") }
    if tracker.publishWakeup(now.Add(3*time.Minute), "repeat", now, 3*time.Minute, 3*time.Minute) == false { t.Fatal("first repeating publish") }
    if tracker.publishWakeup(now.Add(6*time.Minute), "repeat", now, 3*time.Minute, 3*time.Minute) == false { t.Fatal("repeating wakeup did not resend") }
}
```

- [x] **Step 2: Run the focused test and verify RED.**

```sh
go test ./internal/daemon -run '^TestWakeupPublishesOnceOrResendsByPolicy$' -count=1
```

Expected: fail because `publishWakeup` does not exist.

- [x] **Step 3: Add minimal common Wakeup state and helper.**

Add `wakeupState` and `wakeups map[string]wakeupState` to `wakeupTracker`. Implement `publishWakeup`; it records or updates `ActiveSince`, returns false before `initialWait`, returns false after a one-shot publish, and resends only after `resendInterval` when it is positive.

- [x] **Step 4: Run the focused test and verify GREEN.**

```sh
go test ./internal/daemon -run '^TestWakeupPublishesOnceOrResendsByPolicy$' -count=1
```

- [ ] **Step 5: Commit the common tracker.**

```sh
git add internal/daemon/wakeup.go internal/daemon/wakeup_test.go
git commit -m "refactor: unify Wakeup tracking"
```

### Task 2: Route every Wakeup through the common tracker

**Files:**
- Modify: `internal/daemon/wakeup.go`
- Modify: `internal/daemon/wakeup_test.go`

**Interfaces:**
- Consumes: `publishWakeup` from Task 1.
- Preserves: `EventWakeup`, every `wakeup.*` event, and their existing payloads.

- [x] **Step 1: Add failing behavior regressions.**

Extend existing wakeup tests to assert an actionable Wakeup publishes at 3 minutes and again at 6 minutes, while every `wakeup.*` event publishes at its grace time only once until the target state disappears and reappears.

- [x] **Step 2: Run the focused tests and verify RED.**

```sh
go test ./internal/daemon -run 'Wakeup' -count=1
```

Expected: fail until existing callers use the common helper.

- [x] **Step 3: Replace both parallel state paths.**

Use key `actionable\x00<project-id>` for the actionable path and `wakeupKey` for target-specific paths. Remove `activeSince`, `published`, `wakeupActiveSince`, and `wakeupPublished`. On every evaluation, remove common wakeup entries whose keys are absent from the current evaluation.

- [x] **Step 4: Run the focused tests and verify GREEN.**

```sh
go test ./internal/daemon -run 'Wakeup' -count=1
```

- [ ] **Step 5: Commit the caller migration.**

```sh
git add internal/daemon/wakeup.go internal/daemon/wakeup_test.go
git commit -m "refactor: evaluate all daemon wakeups together"
```

### Task 3: Verify the documentation and full suite

**Files:**
- Modify: `doc/continuous-execution.md`
- Modify: `doc/plans/2026-09-12-unified-wakeups.md`

- [x] **Step 1: Check terminology.**

```sh
rg -n 'detection|separate.*tracker|別 tracker' doc/continuous-execution.md internal/daemon/wakeup.go
```

Expected: no old terminology or parallel tracker wording.

- [x] **Step 2: Run all verification.**

```sh
go test ./... -count=1
git diff --check
```

- [ ] **Step 3: Mark complete and commit.**

```sh
git add doc/continuous-execution.md doc/plans/2026-09-12-unified-wakeups.md
git commit -m "docs: describe unified wakeups"
```
