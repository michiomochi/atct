# Goal 225: reconciliation-first removal boundary

## Purpose and authority

This specification applies the accepted
`2026-09-03-goal-225-reconciliation-notification-revision.md` to the current
implementation. It does not replace that document. The canonical handoff,
goal, task, and decision records are the recovery source; an SSE message is a
best-effort post-commit wake-up only.

The current outbox/cursor implementation contradicts that rule: migration
0025 creates `project_event_sequences`, `workflow_event_outbox`, and
`watch_delivery_cursors`; `persistWorkflowEvent` allocates a transactionally
stored sequence and serializes every selected transition; HTTP replays those
rows, returns `stale_cursor`, and accepts acknowledgements; and `watch` uses
event IDs, cursors, replay and high-watermarks to render state. Those are
delivery-history dependencies, not canonical reconciliation.

## Target boundary

### Retained: post-commit live wake-up

Keep the existing in-memory notifier and SSE subscription:

- state transitions commit their canonical row changes before publication;
- `Store.SubscribeEvents`, `notifier.publishEvent`, and the `/api/events`
  stream remain a bounded, lossy signal path; and
- a received signal schedules/causes a scoped reconciliation. It is not
  rendered as authoritative history and no event ID, sequence, deduplication,
  acknowledgement, or resume cursor is required.

The event payload may continue to carry enough scope for filtering a live
wake-up. It must not require persistence or stable event identity.

### Removed: durable delivery machinery

Remove the following Goal 225-owned correctness machinery after the
replacement read path is live:

- `project_event_sequences`, `workflow_event_outbox`, and
  `watch_delivery_cursors` from migration 0025 via a **new, forward-only**
  migration;
- `WorkflowEvent`, `WorkflowEventQuery`, `WorkflowEventPage`,
  `WatchDeliveryCursor`, sequence allocation, outbox insert/pruning, outbox
  aliases, cursor get/advance aliases, replay bounds, and transaction-time
  `persistWorkflowEvent` calls;
- SQL queries and generated models for those tables;
- `/api/events` replay (`cursor`, `after_sequence`, `Last-Event-ID`),
  high-watermark and `410 stale_cursor` behavior, and `/api/watch/cursor`;
  and
- watch-side watcher keys, event-ID deduplication, acknowledgement, replay
  fallback, high-watermark processing, and tests that assert them.

Removing the durable writer must not remove post-commit publication. Transition
callers publish the already-committed in-memory event directly, preserving the
existing best-effort wake-up ordering without persisting an event first.

### Canonical reconciliation read

Retain the existing `GET /api/events/reconcile` route as the sole
reconciliation boundary; it changes from an event replay envelope to a
canonical scoped snapshot. This avoids a second recovery API.

For a goal scope it returns the goal, all of its tasks, all goal/plan/task
handoff rows (including completed and rejected rows), and all decisions for
that goal (including open, answered, approved, rejected, and applied rows).
The actionable subset is derived from these arrays using the normal state
rules, not returned from an outbox position. A task scope returns its task,
parent-goal authority evidence, its task handoffs and decisions, and the
goal-level handoffs required to derive role. A project scope uses the same
canonical categories in deterministic existing-ID order; if its payload must
be bounded, the implementation extends the existing reconciliation read's
pagination rather than adding a replay/cursor/ack API.

The initial implementation does not invent tables, recovery state, or a
separate daemon. It changes the existing store reconciliation read and its
existing HTTP representation only.

## Compatibility and rollout

1. First make canonical reconciliation complete enough to reconstruct normal
   authority and historical outcomes (#613-style applied decision and
   completed-handoff reopen) while migration 0025 remains present but unused
   for recovery.
2. Change the bundled watch/monitor client to reconcile at start, reconnect,
   periodic checks, and after every live signal. Deploy this reader before
   removal. During the short mixed-version window, an old cursor query or
   acknowledgement may be accepted as a documented no-op; it must neither
   read nor recreate durable delivery state.
3. Remove writers, replay/cursor HTTP behavior, and watch cursor code. A live
   `/api/events` connection has no resume semantics; disconnection causes a
   reconciliation on reconnect.
4. Add the next migration (after 0025; it does not rewrite 0025) to drop the
   three tables and their indexes only after all supported clients use the
   reconciliation-first path. Existing outbox/cursor rows are not domain data:
   they are ignored during the compatibility window and are discarded only by
   that table drop. No handoff, task, goal, or decision row is migrated or
   deleted.

## Minimality

The change stays narrow by reusing the existing notifier, `/api/events`,
`/api/events/reconcile`, watch loop, canonical list methods, and migration
runner. It does not rename subsystems, introduce a delivery service, add a
new state record/table/API, or make another goal reimplement this work.

## Non-goals

- No changes to handoff, decision, role, goal/task lifecycle, daemon runtime,
  configuration, skills, or UI.
- No implementation in this design task; migration 0025 and existing rows are
  untouched until an accepted implementation plan is executed.
- No changes to Decision #638, the removed snapshot-migration evidence, or
  Goal 227/229/231/232/234/236 worktrees.
- No event-by-event delivery, cursor recovery, exactly-once, at-least-once,
  replay, high-watermark, or stale-cursor contract.

## Acceptance evidence for the later implementation

- A fresh, disconnected, or restarted watcher derives the same next action
  from the same scoped canonical snapshot.
- An applied decision and completed/reopened handoffs appear in canonical
  history without an outbox row.
- Lost, duplicated, and reordered SSE wake-ups create at most extra reads;
  they cannot apply a state transition or suppress a required reconciliation.
- The forward migration preserves canonical records while dropping only the
  obsolete delivery tables.
