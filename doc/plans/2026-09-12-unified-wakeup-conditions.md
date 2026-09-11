# Unified Wakeup Conditions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace separate actionable and detection tracker state with one condition tracker while preserving every existing event contract.

**Architecture:** `wakeupTracker` will store one condition state keyed by a string. A common helper receives a condition key, start time, initial wait, optional resend interval, and current time; it decides whether to publish and updates the shared state. Existing callers continue constructing their current `DecisionEvent` values.

**Tech Stack:** Go, existing `internal/daemon/wakeup.go` and Go tests.

## Global Constraints

- Keep all existing event names and payload types, including `detection.*`.
- Do not add database state or dependencies.
- Keep current wait and resend timings exactly.
- Delete obsolete parallel tracker maps after the refactor.

---

### Task 1: Prove the common condition policy

**Files:**
- Modify: `internal/daemon/wakeup_test.go`
- Modify: `internal/daemon/wakeup.go`

**Interfaces:**
- Produces: `wakeupTracker.publishCondition(now, key, startedAt, initialWait, resendInterval) bool`.
- `resendInterval == 0` means publish at most once while the condition remains active.

- [x] **Step 1: Write a failing tracker test.**

```go
func TestWakeupConditionPublishesOnceOrResendsByPolicy(t *testing.T) {
    tracker := newWakeupTracker(now)
    if tracker.publishCondition(now.Add(3*time.Minute), "once", now, 3*time.Minute, 0) == false { t.Fatal("first publish") }
    if tracker.publishCondition(now.Add(6*time.Minute), "once", now, 3*time.Minute, 0) { t.Fatal("once condition repeated") }
    if tracker.publishCondition(now.Add(3*time.Minute), "repeat", now, 3*time.Minute, 3*time.Minute) == false { t.Fatal("first repeating publish") }
    if tracker.publishCondition(now.Add(6*time.Minute), "repeat", now, 3*time.Minute, 3*time.Minute) == false { t.Fatal("repeat condition did not resend") }
}
```

- [x] **Step 2: Run the focused test and verify RED.**

```sh
go test ./internal/daemon -run '^TestWakeupConditionPublishesOnceOrResendsByPolicy$' -count=1
```

Expected: fail because `publishCondition` does not exist.

- [x] **Step 3: Add minimal common condition state and helper.**

Add `wakeupConditionState` and `conditions map[string]wakeupConditionState` to `wakeupTracker`. Implement `publishCondition`; it records or updates `ActiveSince`, returns false before `initialWait`, returns false after a one-shot publish, and resends only after `resendInterval` when it is positive.

- [x] **Step 4: Run the focused test and verify GREEN.**

```sh
go test ./internal/daemon -run '^TestWakeupConditionPublishesOnceOrResendsByPolicy$' -count=1
```

- [ ] **Step 5: Commit the common tracker.**

```sh
git add internal/daemon/wakeup.go internal/daemon/wakeup_test.go
git commit -m "refactor: unify wakeup condition tracking"
```

### Task 2: Route every wakeup condition through the common tracker

**Files:**
- Modify: `internal/daemon/wakeup.go`
- Modify: `internal/daemon/wakeup_test.go`

**Interfaces:**
- Consumes: `publishCondition` from Task 1.
- Preserves: `EventWakeup`, every `detection.*` event, and their existing payloads.

- [x] **Step 1: Add failing behavior regressions.**

Extend existing wakeup tests to assert an actionable condition publishes at 3 minutes and again at 6 minutes, while a detection condition publishes at its grace time only once until the condition disappears and reappears.

- [x] **Step 2: Run the focused tests and verify RED.**

```sh
go test ./internal/daemon -run 'Wakeup|Detection' -count=1
```

Expected: fail until existing callers use the common helper.

- [x] **Step 3: Replace both parallel state paths.**

Use key `actionable\x00<project-id>` for the actionable path and `wakeupConditionKey` for condition-specific paths. Remove `activeSince`, `published`, `detectionActiveSince`, and `detectionPublished`. On every evaluation, remove common condition entries whose keys are absent from the current evaluation.

- [x] **Step 4: Run the focused tests and verify GREEN.**

```sh
go test ./internal/daemon -run 'Wakeup|Detection' -count=1
```

- [ ] **Step 5: Commit the caller migration.**

```sh
git add internal/daemon/wakeup.go internal/daemon/wakeup_test.go
git commit -m "refactor: evaluate all daemon wakeups as conditions"
```

### Task 3: Verify the documentation and full suite

**Files:**
- Modify: `doc/continuous-execution.md`
- Modify: `doc/plans/2026-09-12-unified-wakeup-conditions.md`

- [x] **Step 1: Check terminology.**

```sh
rg -n '^## Detection$|separate.*tracker|別 tracker' doc/continuous-execution.md internal/daemon/wakeup.go
```

Expected: no separate Detection heading or parallel tracker wording.

- [x] **Step 2: Run all verification.**

```sh
go test ./... -count=1
git diff --check
```

- [ ] **Step 3: Mark complete and commit.**

```sh
git add doc/continuous-execution.md doc/plans/2026-09-12-unified-wakeup-conditions.md
git commit -m "docs: describe unified wakeup conditions"
```
