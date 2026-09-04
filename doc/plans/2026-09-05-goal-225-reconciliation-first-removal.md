# Goal 225 reconciliation-first removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove outbox/cursor/replay correctness machinery while retaining canonical scoped reconciliation and best-effort live wake-ups.

**Architecture:** Reuse the existing reconciliation route and notifier. First make the route read complete canonical records; then make watch treat SSE only as a signal to read it; finally delete obsolete persistence, routes, queries, and schema through one forward migration.

**Tech Stack:** Go, SQLite migrations, sqlc, net/http SSE, existing `cmd/atct` watch client.

## Why this plan has small change sets

The dependency chain is writer → recovery read → watch consumer → obsolete
storage. Each work unit changes one boundary and has an independently
reviewable invariant. It reuses existing store lists, the existing
`/api/events/reconcile` route, and the existing notifier instead of adding a
delivery subsystem, a new recovery API, or intermediate tables. This keeps
generated SQL churn and migration work isolated to the final unit.

## Global constraints

- Do not rewrite `0025_workflow_event_outbox.sql`; create one later
  forward-only removal migration after the new read/client path is live.
- Do not add a table, durable cursor/state, replay API, acknowledgement,
  high-watermark, stale-cursor behavior, daemon, or cross-goal work.
- Keep post-commit in-memory/SSE publication best-effort. A disconnected or
  slow subscriber may miss a message and must reconcile instead.
- Keep all canonical goal/task/decision/handoff rows and retain
  `.atct-evidence/task-1047-snapshot-evidence` unchanged.
- Each executor receives only its work unit through record-first handoff;
  do not delegate implementation until this spec/plan review is accepted.

---

### Task 1: Make the existing reconciliation read canonical

**Files:**

- Modify: `internal/store/workflow_events.go`
- Modify: `internal/store/decision.go`
- Modify: `internal/store/queries/task.sql`
- Regenerate: `internal/store/sqlcgen/task.sql.go`, `internal/store/sqlcgen/models.go`
- Modify: `internal/httpapi/server.go`
- Test: `internal/store/workflow_event_test.go`
- Test: `internal/httpapi/workflow_events_test.go`

**Consumes:** Existing `ListGoalHandoffs`, `ListPlanHandoffs`, and
`ListTaskHandoffs`, which already include terminal rows; existing goal/task
scope validation in `eventScopeIDs`.

**Produces:** The existing reconciliation response with canonical records and
no `events`, sequence, high-watermark, cursor, or outbox-derived fields.

- [ ] Write focused failing store tests proving a goal snapshot includes a
  completed/reopened handoff and an applied decision, while a task snapshot
  includes its parent goal authority evidence and excludes unrelated tasks.
- [ ] Add the smallest ordered decision-list query needed to return every
  decision in a scope; do not add a new table or write path. Replace the
  current reconciliation filtering that drops completed handoffs and returns
  only open/unapplied decisions.
- [ ] Remove `WorkflowEventQuery` sequence fields and outbox page fields from
  the reconciliation representation while keeping the existing route name and
  scope validation. Reject/ignore no replay state because the route no longer
  consumes it.
- [ ] Add HTTP assertions that `/api/events/reconcile` reconstructs the same
  canonical history without `current_sequence`, `high_watermark`, or `events`.
- [ ] Run focused store and HTTP reconciliation tests, then `git diff --check`.
  Commit only this unit's explicit paths.

### Task 2: Convert watch and SSE to wake-up-only behavior

**Files:**

- Modify: `internal/store/notify.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/workflow_events_test.go`
- Modify: `cmd/atct/watch.go`
- Test: `cmd/atct/watch_test.go`
- Test: `cmd/atct/watch_scope_test.go`

**Consumes:** Task 1's canonical response. The existing notifier channel is
lossy and its event payload is used only to decide which scoped read to make.

**Produces:** `/api/events` has no durable replay/resume contract; watch
reconciles on start, reconnect, periodic check, and every live signal.

- [ ] Write failing watch tests for a dropped signal, duplicate signals, and
  reordered signals. Each must cause zero or coalesced extra reconciliations
  and must not emit a direct state transition from an SSE frame.
- [ ] Remove watcher-key URL construction, `Last-Event-ID` parsing, event-ID
  deduplication, stale-cursor retry, acknowledgement, and high-watermark
  handling. Keep live event scope filtering and make the event path schedule
  `reconcileWatchScope`.
- [ ] Simplify `/api/events` to stream only currently published in-memory
  events; remove replay, cursor parsing, `410 stale_cursor`, and the cursor
  route. During the explicitly supported mixed-version release, accept old
  cursor parameters/acknowledgements only as no-ops if compatibility requires
  it; do not query or write delivery tables.
- [ ] Preserve `notifier.publishEvent` after canonical transaction commit and
  update its comment/tests to state that slow subscribers can miss wake-ups.
- [ ] Run focused HTTP/watch/monitor tests and `git diff --check`. Commit only
  this unit's explicit paths.

### Task 3: Remove durable writes, SQL, and schema forward-only

**Files:**

- Create: `internal/store/migrations/0026_drop_workflow_event_delivery.sql`
- Modify: `internal/store/workflow_events.go`
- Modify: `internal/store/{decision,goal,goal_handoff,task_handoff}.go`
- Modify: `internal/store/queries/task.sql`
- Regenerate: `internal/store/sqlcgen/task.sql.go`, `internal/store/sqlcgen/models.go`
- Modify: `internal/store/id_migration_test.go`
- Modify: `internal/store/workflow_event_test.go`
- Modify: `internal/store/migrations_test.go`

**Consumes:** Task 2 has already eliminated reader/client dependence on
outbox/cursor tables.

**Produces:** No transition allocates a sequence or serializes a workflow
event; the new migration drops only obsolete durable-delivery tables/indexes.

- [ ] Write a migration fixture containing migration-0025 outbox/cursor rows
  plus canonical handoff/decision rows. Assert the forward migration drops
  only `project_event_sequences`, `workflow_event_outbox`, and
  `watch_delivery_cursors`, while canonical rows remain readable.
- [ ] Remove `persistWorkflowEvent`, sequence/outbox retention helpers, their
  transaction call sites, outbox/cursor SQL, and generated models. Retain the
  direct post-commit `publishEvent` path using the already committed domain
  event.
- [ ] Add `0026_drop_workflow_event_delivery.sql` with `DROP TABLE` statements
  for the three obsolete tables. It must not edit 0025 or transform domain
  rows; old outbox/cursor rows disappear solely with those tables.
- [ ] Update schema-integrity and store tests from outbox persistence claims to
  canonical-history and best-effort-publish claims.
- [ ] Run focused migration/store/HTTP/watch tests and `git diff --check`.
  Commit only this unit's explicit paths.

## Review matrix

- Goal/task/project scopes return only their canonical records in stable order.
- Applied decisions and completed/reopened handoffs survive recovery without
  event replay.
- Start, reconnect, periodic check, and every SSE hint invoke reconciliation;
  dropped/duplicate/reordered hints cannot create or skip a state transition.
- No code path reads/writes an outbox row, sequence, cursor, acknowledgement,
  high-watermark, or stale-cursor response after Task 3.
- Applying the forward migration preserves canonical records and removes only
  the three delivery tables.
