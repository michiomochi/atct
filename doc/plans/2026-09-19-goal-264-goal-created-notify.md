# Goal 264: New goals reach project commander monitors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Project commander monitors recover and deliver new `goal.created` notifications from canonical reconciliation state across connected, reconnect, and monitor-process restart paths.

**Architecture:** Keep SSE as a trigger for the canonical `/api/events/reconcile` snapshot. Persist one `last_reconciled_at` watermark on the existing stable monitor-token binding. The watcher uses that watermark only for the first snapshot after a process restart, then uses the existing in-memory goal-ID comparison and delivery maps.

**Tech Stack:** Go, `net/http`, SQLite migrations/sqlc, and the standard `testing` package.

## Global Constraints

- Keep the goal projection in `cmd/atct/watch.go`; use the existing
  `watchScopeFilter`, formatter, output sink, action sink, and
  `wakeupDelivered` map.
- Add only the narrow `monitor_bindings.last_reconciled_at` watermark and its
  read/update plumbing. Do not add an event outbox, replay cursor, delivery
  receipt, acknowledgement endpoint, dependency, or daemon operation.
- Pass the stable monitor token on reconciliation only for the existing
  token-bound monitor path. A plain diagnostic watch remains process-local and
  has no agent action state.
- Project `goal.created` only when `ProjectID` is set and both `GoalID` and
  `TaskID` are empty.
- Treat the first successful reconciliation without a durable watermark as a
  baseline; do not announce goals already present in it.
- Advance the durable watermark only from a successful project-scoped
  commander health report after reconciliation projection succeeds. Never
  advance it on a decode, HTTP, output, or action-sink failure.
- Preserve the existing `goal.created` formatter, `wakeupDelivered` duplicate
  suppression, goal/task scope behavior, and monitor action delivery.
- Do not edit Goal 280's `cmd/atct/watch_delivery.go`, Goal 292's
  `cmd/atct/watch_scope.go`, Goal 248's monitor suppression, or Goal 294's
  handoff-recovery implementation. Store changes are limited to monitor
  binding/health watermark plumbing.
- The executor must not commit; after verification it leaves the changes for
  the subcommander to review and commit.

---

### Task 1: Persist the monitor watermark and project newly reconciled goals

**Files:**

- Modify: `cmd/atct/watch.go`
- Test: `cmd/atct/watch_test.go`
- Modify: `internal/httpapi/server.go`
- Test: `internal/httpapi/workflow_events_test.go`
- Modify: `internal/store/monitor_assignment.go` and the existing monitor
  health upsert path
- Test: `internal/store/monitor_assignment_test.go`
- Modify: `internal/store/queries/monitor_binding.sql` and generated
  `internal/store/sqlcgen/monitor_binding.sql.go`
- Create: `0045_monitor_binding_watermark.sql`, after main's
  `0044_fixed_width_timestamps.sql`, adding
  `monitor_bindings.last_reconciled_at`
- Modify: `schema.sql` to keep the declared schema current

**Interfaces:**

- Consumes: canonical `goals` and `created_at` from `/api/events/reconcile`,
  the existing `watchScopeFilter`, and the existing monitor-health lifecycle.
- Produces: one `goal.created` projection per goal created after the bound
  monitor's durable watermark, only for project-wide commander scopes.

- [ ] **Step 1: Add failing connected, scope, and restart tests.**

  Extend the watch reconciliation fixtures so goals include distinct
  `created_at` values. Add a sequential project-scope test whose first response
  contains goal `1`, whose second response adds goal `7`, and whose third
  response is unchanged. Pass one `watchReconciliation` state pointer and
  assert exactly one:

  ```text
  atct goal created (goal_id: 7)
  ```

  Add goal-scoped and task-scoped cases using the same new goal; both must
  produce no project-level line. Keep the existing `watchRoundTripper` helper.

  Add a process-spanning test with the same `monitor_token`: the first watch
  baselines an existing goal and successfully reports health, the fake daemon
  then exposes a new goal, and a second watch instance starts with that goal in
  its first snapshot plus the prior `monitor_last_reconciled_at`. Assert the
  second instance emits the new goal once and does not replay the baseline.
  Make the fake health endpoint record the first successful watermark update so
  the test models “watch down -> goal created -> watch restarted”.

- [ ] **Step 2: Add the durable token watermark.**

  After merging main, use `0045_monitor_binding_watermark.sql` for this
  forward-only migration; do not leave the worktree's migration at 0044,
  because main already owns `0044_fixed_width_timestamps.sql`. Before review,
  verify that `schema_migrations` contains no
  `0045_monitor_binding_watermark.sql` record; if an old-name record exists,
  stop and escalate instead of silently renaming history. Add the migration
  with a non-null empty default for `monitor_bindings.last_reconciled_at`;
  update `schema.sql`, the monitor
  binding query source, and generated sqlc code. Add store methods to read the
  token's prior watermark and to update it from `UpsertMonitorHealth` only when
  the report is a healthy project-scoped commander report. Preserve the value
  when the same token is rebound to a new transport agent session. A missing
  binding or empty watermark means “no durable baseline”, not an error in a
  plain diagnostic watch.

- [ ] **Step 3: Expose the prior watermark without changing the event model.**

  Add `monitor_token` as an optional reconciliation query parameter. For a
  project-wide request, have the HTTP handler include the token's prior
  `monitor_last_reconciled_at` in the JSON response when present. Keep the
  canonical `goals`, decisions, and handoff fields unchanged; do not add a
  replay route or mutate the SSE payload. Add HTTP coverage for a token's
  watermark, an empty first watermark, and preservation after rebinding.

- [ ] **Step 4: Implement the shared canonical projection.**

  Add `created_at` and the optional watermark metadata to the watch
  reconciliation types. Add the monitor token to `watchReconcileURL`.
  In `reconcileWatchScope`, for a project-wide scope:

  - On the first successful snapshot, if a durable watermark exists, create
    `watchDecision{GoalID: goal.ID, TargetRole: "commander"}` only for goals
    whose `created_at` is after that watermark. If no watermark exists, mark
    the snapshot as the baseline and emit nothing.
  - On later snapshots, compare goal IDs with the previous successful state so
    live SSE signals, periodic reconciliation, and reconnects share the same
    path. Compare IDs, not statuses or content.
  - Route every candidate through `scopeFilter.delivers("goal.created", ...)`
    and `emitWatchDecisionWithStateAndSinks`. Update the in-memory state only
    after all projections succeed. The existing `wakeupDelivered` map handles
    repeated candidates within the watch process; the later healthy report
    advances the durable watermark for the next process.

  Keep goal/task scopes unchanged and retain the behavior when the optional
  reconciliation state pointer is absent.

- [ ] **Step 5: Verify the focused behavior and package compatibility.**

  Run:

  ```bash
  go test ./cmd/atct -run 'Test(ReconcileWatchScopeProjectsNewGoalAfterConnectedSignal|ReconcileWatchScopeDoesNotProjectNewGoalForScopedWatch|WatchReconcilesNewGoalAfterReconnect|WatchReconcilesNewGoalAfterProcessRestart)$' -count=1
  go test ./internal/store ./internal/httpapi -run 'Test.*Monitor.*(Watermark|Binding)|TestWorkflowReconcile.*Monitor' -count=1
  go test ./cmd/atct -count=1
  go test ./internal/store ./internal/httpapi -count=1
  ./script/schema-check.sh
  git diff --check
  ```

  Expected: selected and package tests pass, sqlc/schema parity is clean, and
  `git diff --check` is silent. Do not run daemon operations or repository-wide
  test commands from the executor pane.

- [ ] **Step 6: Request task review without committing.**

  Report the watermark migration, endpoint metadata, watch projection, exact
  verification results, any command that could not run, and all modified paths
  through the task handoff review request. Leave the implementation
  uncommitted for the subcommander's review and explicitly state that no
  adjacent goal-owned delivery, scope, suppression, or handoff-recovery files
  were changed.
