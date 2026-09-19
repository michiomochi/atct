# Goal 264: New goals reach project commander monitors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Project commander monitors recover and deliver new `goal.created` notifications from canonical reconciliation state exactly once.

**Architecture:** Keep the existing SSE-as-trigger and `/api/events/reconcile` canonical snapshot. The watcher remembers the previous successful goal-ID set, projects only newly appearing goals for project-wide scopes, and reuses the existing formatter and delivery deduplication.

**Tech Stack:** Go, `net/http`, and the standard `testing` package.

## Global Constraints

- Modify only `cmd/atct/watch.go` and `cmd/atct/watch_test.go` for implementation and regression coverage.
- Use canonical reconciliation state; do not add a store table, outbox, cursor, endpoint, dependency, or daemon operation.
- Project `goal.created` only when `ProjectID` is set and both `GoalID` and `TaskID` are empty.
- Treat the first successful reconciliation as baseline; do not announce goals already present in it.
- Preserve the existing `goal.created` formatter, `wakeupDelivered` duplicate suppression, goal/task scope behavior, and monitor action delivery.
- Do not edit Goal 280's `cmd/atct/watch_delivery.go`, Goal 292's `cmd/atct/watch_scope.go`, Goal 248's monitor suppression, or Goal 294's `internal/store` files.
- The executor must not commit; after verification it leaves the changes for the subcommander to review and commit.

---

### Task 1: Project newly reconciled goals to the commander monitor

**Files:**

- Modify: `cmd/atct/watch.go` (`watchReconciliation` and `reconcileWatchScope`)
- Test: `cmd/atct/watch_test.go` (canonical reconciliation and reconnect tests)

**Interfaces:**

- Consumes: `watchReconciliation.Goals` returned by `/api/events/reconcile`, the existing `watchScopeFilter`, and the existing `emitWatchDecisionWithStateAndSinks` path.
- Produces: one `goal.created` projection per goal ID newly present after the baseline, only for project-wide scopes.

- [ ] **Step 1: Add failing connected and scope-isolation tests**

  Add a sequential reconciliation test whose first response contains an
  existing goal and whose second response adds goal `7`. Pass one
  `watchReconciliation` state pointer to both calls and assert that the second
  call writes exactly:

  ```text
  atct goal created (goal_id: 7)
  ```

  Call a third time with the unchanged response and assert that the output is
  still one line. Add a table-driven case using the same new goal for a
  goal-scoped and task-scoped `watchScope`; both must produce no line.

  Use the existing `watchRoundTripper` helper and return JSON shaped like:

  ```json
  {"goals":[{"id":1,"status":"active"}],"decisions":[],"goal_handoffs":[],"plan_handoffs":[],"task_handoffs":[]}
  ```

  followed by the same object with `{"id":7,"status":"active"}` appended.

- [ ] **Step 2: Add a failing reconnect/re-reconcile test**

  Exercise the existing `watchWithURLs` loop. Return an empty canonical goal
  list for the initial reconciliation, close the first `/api/events` response
  before sending a frame, then return goal `7` from the reconciliation after
  reconnect. Use `cancelOnOutput` with the expected `goal.created` line and
  assert that the output contains one line and reconciliation was called at
  least once before and once after the stream reconnect. Keep the second stream
  available until the output cancellation ends the watch.

- [ ] **Step 3: Run the focused tests and verify the baseline failure**

  Run:

  ```bash
  go test ./cmd/atct -run 'Test(ReconcileWatchScopeProjectsNewGoalAfterConnectedSignal|ReconcileWatchScopeDoesNotProjectNewGoalForScopedWatch|WatchReconcilesNewGoalAfterReconnect)$' -count=1
  ```

  Expected: FAIL because reconciliation currently never projects entries from
  `state.Goals` as `goal.created`.

- [ ] **Step 4: Implement the minimal canonical-state projection**

  Add an unexported initialization marker to `watchReconciliation`. In
  `reconcileWatchScope`, copy the previous state and its initialization marker
  before decoding the current response. After the current response is decoded
  and before replacing the saved state, run this logic only for a project-wide
  scope and only when `previousInitialized` is true:

  ```go
  if latestReconciliation != nil {
      previous := *latestReconciliation
      previousInitialized := previous.initialized
      if previousInitialized && scope.ProjectID != "" && scope.GoalID == "" && scope.TaskID == "" {
          for _, goal := range state.Goals {
              if watchReconciliationHasGoal(previous, goal.ID) {
                  continue
              }
              decision := watchDecision{GoalID: goal.ID, TargetRole: "commander"}
              if !scopeFilter.delivers("goal.created", decision) {
                  continue
              }
              if err := emitWatchDecisionWithStateAndSinks(
                  out, "goal.created", decision, delivered, lastWakeupContent,
                  wakeupDiscrepancyDelivered, wakeupDelivered, sink, actionSink,
              ); err != nil {
                  return err
              }
          }
      }
      state.initialized = true
      *latestReconciliation = state
  }
  ```

  Compare IDs rather than statuses or content. Set the initialization marker
  only when the full projection succeeds. When the optional
  `latestReconciliation` pointer is absent, retain the existing behavior and
  skip this state-diff projection. Reuse the existing `goal.created` formatter
  and delivery maps; do not modify `watch_delivery.go`, `watch_scope.go`, or
  the store/server reconciliation contract.

- [ ] **Step 5: Verify the focused behavior and package compatibility**

  Run:

  ```bash
  go test ./cmd/atct -run 'Test(ReconcileWatchScopeProjectsNewGoalAfterConnectedSignal|ReconcileWatchScopeDoesNotProjectNewGoalForScopedWatch|WatchReconcilesNewGoalAfterReconnect)$' -count=1
  go test ./cmd/atct -count=1
  git diff --check
  ```

  Expected: all selected and package tests pass, and `git diff --check` is
  silent. Do not run daemon operations or repository-wide test commands from
  the executor pane.

- [ ] **Step 6: Request task review without committing**

  Report what changed, the exact verification results, any command that could
  not run, and the two modified paths through the task handoff review request.
  Leave the implementation uncommitted for the subcommander's review and
  explicitly state that no adjacent goal-owned files were changed.
