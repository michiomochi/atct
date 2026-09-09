# Goal 225 reconciliation-first notification revision plan

**Goal:** Replace Goal 225's durable event-outbox/cursor delivery contract with canonical scoped reconciliation and best-effort SSE wake-ups.

**Architecture:** The store's handoff, goal, task, and decision records become the only recovery source. A monitor periodically and after live wake-ups reads a deterministic scoped state-and-history snapshot, derives current actionable work, and then uses normal authorization APIs. SSE carries no acknowledgement, sequence, or recovery guarantee.

**Dependencies:** This plan supersedes the notification portions of the accepted Goal 225 execution-flow plan. The pending GoalDetail UI plan remains independent. Do not reconcile Goal 228 until this revision has commander acceptance.

## Global constraints

- No implementation or delegation begins until commander accepts this revised documentation through canonical review.
- Do not alter handoff/lifecycle semantics, shared daemon replacement, configuration, chezmoi, or the pending GoalDetail UI plan while preparing this revision.
- Preserve post-commit in-memory/SSE publication as a best-effort wake-up, not a source of truth.
- Do not edit migration `0025_workflow_event_outbox.sql`; use a new forward-only migration for removal.

## Work unit 1: Canonical reconciliation snapshot

**Files:**

- Modify: `internal/store/workflow_events.go` or its replacement reconciliation module.
- Modify: `internal/store/goal_handoff.go`, `internal/store/task_handoff.go`, `internal/store/goal.go`, and decision query/store paths as needed to expose canonical scoped history.
- Modify: `internal/httpapi/server.go` and reconciliation response tests.
- Test: `internal/store/workflow_event_test.go` replacement tests and `internal/httpapi/workflow_events_test.go` replacement tests.

**Steps:**

1. Write failing store/HTTP tests for a goal, task, and project scoped snapshot. Require current goal/task/claim state plus complete relevant handoff and decision histories, including completed/rejected/reopened handoffs and an applied completion rejection.
2. Replace event/high-watermark/cursor-shaped reconciliation data with deterministic pagination over canonical records. Ensure scope isolation and normal authorization boundaries remain intact.
3. Prove a fresh monitor and a monitor reconnecting after #613-style rejection or completed-handoff reopen derive the same currently actionable work.
4. Run focused store/HTTP reconciliation tests and commit only store/HTTP/test paths for this unit.

## Work unit 2: Best-effort wake-up monitor

**Files:**

- Modify: `cmd/atct/watch.go`.
- Modify: `cmd/atct/watch_test.go`.
- Modify: monitor wrapper tests that assert watch rendering.

**Steps:**

1. Write failing watch tests showing initial start, reconnect, periodic poll, and any live SSE hint each trigger a scoped reconciliation; repeated hints coalesce without suppressing a later snapshot.
2. Remove cursor lookup/advance, replay, high-watermark, stale-cursor handling, and durable event-ID deduplication. Keep live event parsing only as a wake-up trigger.
3. Verify a dropped, duplicate, or out-of-order SSE hint cannot change the derived state; only snapshot reads do. Verify failure retries do not persist a delivery acknowledgement.
4. Run focused watch/wrapper tests and commit only watch/wrapper paths for this unit.

## Work unit 3: Remove durable-delivery storage and compatibility surface

**Files:**

- Create: next forward-only SQLite migration dropping `project_event_sequences`, `workflow_event_outbox`, and `watch_delivery_cursors` after the replacement paths are live.
- Modify: `internal/store/queries/task.sql` and regenerated `internal/store/sqlcgen/*`.
- Modify: `internal/store/notify.go`, transition callers in `internal/store/{decision,goal,goal_handoff,task_handoff}.go`, and HTTP routes/tests.
- Modify: `internal/store/id_migration_test.go` and obsolete outbox/cursor tests.

**Steps:**

1. Write migration and API compatibility tests: never rewrite migration 0025; old rows require no domain-data migration; cursor query parameters/acknowledgement follow the documented temporary compatibility behavior; final API contains no replay or stale-cursor contract.
2. Remove transactional outbox persistence, sequences, retention, cursor SQL, routes, and aliases. Preserve post-commit best-effort `publishEvent` behavior.
3. Apply the forward migration to a database containing 0025 rows and prove handoff/decision history and reconciliation remain intact.
4. Run focused store, HTTP, watch, and migration tests plus `git diff --check`; commit only this unit's paths.

## Review matrix

- Fresh start, process restart, disconnect/reconnect, and periodic poll each yield the same scoped role/next action.
- #613-style `rejected -> applied` is visible in decision history without inbox membership or replay.
- Completed-handoff reopen is visible as both historical completion and new current receipt.
- Goal/project/task scope isolation holds for histories and actionable subsets.
- Lost, duplicate, and reordered live hints cause at most an extra reconciliation; they never create a state transition or require a cursor.
- Migration from a database containing outbox/cursor rows loses no canonical handoff, goal, task, or decision data.
- Goal 228 remains untouched until commander accepts this plan and explicitly reconciles its downstream assumptions.
