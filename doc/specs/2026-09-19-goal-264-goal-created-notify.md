# Goal 264: New goals reach project commander monitors

## Classification

Bounded.

## Outcome

A project-scoped commander monitor reports each goal that first appears in its
canonical reconciliation state as:

```text
atct goal created (goal_id: <id>)
```

The report is emitted once per goal for the lifetime of that watch. Existing
goals present in the first successful reconciliation establish the baseline and
are not replayed. Goal-scoped and task-scoped monitors remain unaffected.

## Current behavior

`CreateGoal` publishes `goal.created`, but the watcher treats the SSE payload as
a signal to fetch `/api/events/reconcile`; it does not act on the payload. The
reconciliation already contains canonical `goals`, while
`reconcileWatchScope` currently projects only decisions and handoff state.
Consequently a live signal, a missed signal recovered by reconnect, and a
periodic re-reconcile all fail to produce the existing `goal.created` line.

## Design

Keep the change in `cmd/atct/watch.go`:

1. Track whether the last successful reconciliation is a baseline in the
   in-memory `watchReconciliation` state.
2. On later reconciliations, compare goal IDs in the new snapshot with the
   previous snapshot.
3. For IDs that were not in the previous snapshot, and only when the scope is
   project-wide (`ProjectID` set with no `GoalID` or `TaskID`), create the
   existing `watchDecision` shape and send it through the existing scope filter,
   formatter, output sink, and action sink as `goal.created`.
4. Update the saved reconciliation only after all projections succeed. The
   existing `wakeupDelivered` key (`event name` + `goal ID` + generation)
   suppresses repeated projection of the same goal without adding a second
   notification mechanism.

The first successful snapshot is baseline-only, so reconnects can recover a
goal added after the prior snapshot without replaying goals that predate the
watch. The store, HTTP reconciliation contract, durable monitor-delivery
state, and monitor-side suppression remain unchanged.

## Alternatives considered

- Re-emit every goal in every reconciliation: simpler, but it reports old goals
  and violates exactly-once delivery.
- Add a durable goal-created outbox/cursor: stronger cross-process history, but
  it duplicates the existing monitor delivery machinery and expands this fix
  into store/schema ownership that is not needed for the connected and
  reconnect/re-reconcile paths.

The in-memory canonical-state comparison is the smallest design that covers
the lost-SSE case while preserving existing scope and duplicate behavior.

## Acceptance criteria

- A project-scoped watch emits one `goal.created` line when a new goal appears
  between successful reconciliations.
- The same goal is not emitted again on repeated reconciliation or reconnect.
- The first snapshot does not emit notifications for pre-existing goals.
- Goal-scoped and task-scoped monitors emit no project-level `goal.created`.
- Regression tests cover a connected signal and a reconnect/re-reconcile path.
- No changes are made to the adjacent goals' owned monitor-delivery, scope,
  or store files.
