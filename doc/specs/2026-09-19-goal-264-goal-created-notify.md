# Goal 264: New goals reach project commander monitors

## Classification

Bounded.

## Outcome

A project-scoped commander monitor reports each goal that first appears after
its monitor watermark in canonical reconciliation state as:

```text
atct goal created (goal_id: <id>)
```

The first successful reconciliation for a monitor token with no watermark is a
baseline, so existing goals are not replayed. A later watch process using the
same token recovers goals created while the previous process was down. Goal-
scoped and task-scoped monitors remain unaffected.

The watermark advances only after the existing healthy monitor-health report,
which the watch loop sends after a complete reconciliation and successful
projection. A failed projection therefore remains recoverable. The existing
in-process `wakeupDelivered` map continues to suppress repeated projections
within one watch process.

## Current behavior

`CreateGoal` publishes `goal.created`, but the watcher treats the SSE payload as
a signal to fetch `/api/events/reconcile`; it does not act on the payload. The
reconciliation already contains canonical `goals`, while
`reconcileWatchScope` currently projects only decisions and handoff state.
Consequently a live signal, a missed signal recovered by reconnect, and a
periodic re-reconcile all fail to produce the existing `goal.created` line.

The watch's baseline is also currently process-local. When a monitor process
expires and is relaunched, its empty in-memory state treats every goal in the
first new snapshot as pre-existing. A goal created while the monitor was down
is therefore silently lost.

## Design

Keep goal projection in `cmd/atct/watch.go`, and add the smallest durable
watermark at the existing monitor-token boundary:

1. Add `last_reconciled_at` to the existing `monitor_bindings` row. The value
   is keyed by the stable monitor token, not by the transport agent-session ID,
   so daemon restart and session reattachment do not reset it. A successful
   project-scoped commander health report updates it; a failed reconciliation
   does not.
2. Let `/api/events/reconcile` accept the optional monitor token and return its
   prior watermark as reconciliation metadata. The canonical goals remain the
   source of truth; this metadata is only the monitor's recovery boundary. No
   event outbox, replay cursor, delivery receipt, or new endpoint is added.
3. Include goal `created_at` in the watch's decoded reconciliation state. On a
   project-wide scope, the first snapshot with a durable watermark projects
   goals whose creation time is after that watermark. A first snapshot without
   one establishes the baseline. Later snapshots compare goal IDs against the
   previous successful in-memory snapshot so connected and reconnect paths use
   the same projection function.
4. Create the existing `watchDecision` shape with `TargetRole: "commander"`
   and send it through the existing scope filter, formatter, output sink,
   action sink, and `wakeupDelivered` deduplication. Save the in-memory state
   only after all projections succeed; the normal health report then advances
   the durable watermark.

An unbound diagnostic watch has no stable monitor token and remains process-
local by design. It is not an agent action channel. Token-bound Claude and
Codex monitors are the cross-process contract.

## Alternatives considered

- Keep only the in-memory goal-ID set: it fixes connected/reconnect
  reconciliation but permanently misses goals created while a monitor process
  is down.
- Add a per-goal delivery table, event outbox, or replay cursor: it reintroduces
  delivery machinery explicitly removed in the reconciliation-first design and
  still cannot atomically acknowledge stdout or a Codex enqueue.
- Use monitor-health history as the watermark: its retention window and
  transport-session rows do not provide the stable monitor-token boundary
  required across reattachment.

The token binding's single timestamp is enough for this one monotonic goal
creation projection. It preserves canonical reconciliation as the recovery
source without creating a second event-delivery subsystem.

## Acceptance criteria

- A project-scoped watch emits one `goal.created` line when a new goal appears
  between successful reconciliations.
- Repeated reconciliation and reconnect in one process emit the same goal only
  once.
- A second watch process using the same monitor token emits a goal created
  after the previous process's successful watermark, and does not replay goals
  at or before that watermark.
- Rebinding the same monitor token to a new transport agent session preserves
  the watermark across a daemon restart.
- The first snapshot for a token with no watermark does not notify for
  pre-existing goals.
- Goal-scoped and task-scoped monitors emit no project-level `goal.created`.
- Regression tests cover connected, reconnect/re-reconcile, and process/
  daemon-restart recovery paths.
- No changes are made to Goal 280's monitor-delivery file, Goal 292's scope
  file, Goal 248's monitor-side suppression, or Goal 294's handoff-recovery
  implementation.

## Failure boundary

The watermark update follows rendering, so a process crash in the narrow gap
after output and before the health update can replay that goal on restart. That
is the existing recovery-friendly failure ordering; making a terminal stdout
or Codex enqueue acknowledgement transactional with SQLite would require a
separate delivery protocol and is outside this bounded goal.
