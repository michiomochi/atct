# Three Missing Wakeup Detections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add wakeup detections for unanswered application of answered decisions, default-applied decisions that remain unapplied, and claims whose agent session is stale.

**Architecture:** Extend the existing `DetectWakeup` → daemon `publishDetection` → `watch` path. Detection candidates are collected in `internal/store`; the daemon owns independent grace durations and target-based deduplication; `cmd/atct/watch.go` formats the three SSE decisions. No pane or herdr dependency is introduced.

**Tech Stack:** Go, existing atct store/daemon/watch packages, web pnpm tests.

## Global Constraints

- Copy the decision predicates from `cmd/atct/pending.go` without modifying that file.
- The human-answered decision detection is immediate; the other two use independent three-minute detection durations while leaving the wakeup interval unchanged.
- Deduplicate decision detections by `decision_id` and stale-claim detections by task ID.
- Do not touch `internal/store/task_handoff.go`, Stop hook code, or web code unless the required `pnpm test` explicitly fails because the new event names are not recognized.
- Preserve unrelated existing worktree changes and do not commit, push, or alter migrations.

---

## Task 1: Add failing store and daemon tests

- [ ] Add store coverage for the exact pending predicates: human-answered/unapplied, default-applied/unapplied, and already-applied exclusion; include stale versus live claims.
- [ ] Add daemon coverage for the three event names, immediate human-answer emission, independent delayed candidates, target IDs, and same-target deduplication.

## Task 2: Collect the three candidate types in `internal/store`

- [ ] Add explicit event/state data for decision IDs and the three candidate collections.
- [ ] Load decisions and classify them using the unchanged `Status == answered && AppliedAt == nil` split on `DefaultAppliedAt`.
- [ ] Use `ClaimLiveness` and retain stale claims only for active project goals; do not infer pane or work status.

## Task 3: Publish detections in `internal/daemon`

- [ ] Add three separately named duration constants: immediate human answer, three-minute default-applied decision, and three-minute stale claim.
- [ ] Publish through the existing tracker and use decision/default-applied/claim timestamps where available; preserve existing wakeup detections and dedupe semantics.

## Task 4: Expose and verify the events in `cmd/atct`

- [ ] Make detection targeting prefer `decision_id` for decision candidates and format all three events in the existing watch path.
- [ ] Add watch tests for the new payloads and missing-target validation.
- [ ] Run the required build, Go tests, and `pnpm test`; only if pnpm reports missing event-name registration, update the smallest required web registry.

## Task 5: Final verification and handoff

- [ ] Run formatting and diff checks, confirm `pending.go` and `task_handoff.go` are unchanged, and report the exact verification results to `atct-commander` without committing.
