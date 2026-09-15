# Goal 279 Codex Monitor Rejection Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent Codex from silently dropping one unreceived plan, goal, or task review-rejection action when `turn/start` has an unknown result, without falsely acknowledging another thread turn or automatically duplicating a possibly accepted turn.

**Architecture:** Keep watcher lifecycle deduplication and review-receipt control actions unchanged.  A `turn/started` has no request correlation and is never used as an acknowledgement.  A successful `turn/start` response is the only dequeue proof.  An unknown result terminates the bridge/watch as delivery-uncertain; a later, explicitly started monitor reconciles the durable unreceived lifecycle state once.

**Tech Stack:** Go; existing `cmd/atct` Codex monitor, monitor-binding, and watch tests.

## Global Constraints

- Change only `cmd/atct/codex_monitor.go` and focused tests under `cmd/atct/`.
- Do not add a database cursor, daemon RPC from the bridge, App Server protocol field, retry timer, or new dependency.
- Preserve the existing `*.handoff.review.receive` control-only selector and its older-queued-review pruning.
- Preserve FIFO ordering, distinct-handoff isolation, and disabled-monitor behavior when the App Server connection closes or a submission result is unknown.
- Do not create tasks, start executors, or implement before this plan handoff is accepted.

## File structure

- `cmd/atct/codex_monitor.go`: bridge reservation, observed acceptance, and unknown-submission queue ownership.
- `cmd/atct/codex_monitor_test.go`: bridge-only acceptance/unknown-result and lifecycle-generation regressions.
- `cmd/atct/codex_monitor_lifecycle_test.go`: bound watcher to bridge delivery ordering, if the existing fakes can express the final end-to-end case without new harness code.

---

### Task 1: Make uncertain turn submission fail closed

**Files:**

- Modify: `cmd/atct/codex_monitor.go:codexMonitorBridge`, `pump`, and `HandleNotification`.
- Test: `cmd/atct/codex_monitor_test.go`.

**Interfaces:**

- Consumes: reserved `activeAction`, the queue head, `turn/started`, and `errCodexTurnSubmitUnknown`.
- Produces: only a non-empty `turn/start` response dequeues an action; an unknown result terminates delivery without silently claiming success or retrying automatically.

- [ ] **Step 1: Write failing bridge regressions.**

  Replace `TestCodexMonitorUnknownSubmissionDoesNotRetry` with two tests:

  ```go
  // Unknown submission makes the bridge terminal and preserves no automatic
  // retry path, so a later idle notification cannot submit the action twice.
  starter := &fakeCodexTurnStarter{errs: []error{errCodexTurnSubmitUnknown}}
  bridge := newCodexMonitorBridge(starter, "thread-1")
  err := bridge.Enqueue(context.Background(), "rejection")
  if !errors.Is(err, errCodexTurnSubmitUnknown) { t.Fatal(err) }
  _ = bridge.HandleNotification(context.Background(), completed("thread-1"))
  if got := starter.callsSnapshot(); !slices.Equal(got, []string{"rejection"}) { t.Fatal(got) }
  ```

  ```go
  // A same-thread turn/started from another actor before an unknown response
  // must not acknowledge or dequeue the reserved rejection.
  ```

  Exercise the latter with a fake starter callback that calls
  `HandleNotification(... "turn/started" ...)` with a distinct server turn ID
  before returning `errCodexTurnSubmitUnknown`; assert the bridge reports the
  unknown result and never performs a second submission.

- [ ] **Step 2: Run the new tests to prove the current loss.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal279-go-cache go test ./cmd/atct -run 'TestCodexMonitor(UnknownSubmission|ObservedTurnStarted)' -count=1 -v
  ```

  Expected: the terminal-error assertion fails because the current action sink
  suppresses a non-disabled unknown result; the foreign-notification case
  proves why thread ID cannot be used as an acknowledgement key.

- [ ] **Step 3: Add the minimum acknowledgement state.**

  Do not add acknowledgement state to `HandleNotification`; `turn/started`
  lacks a correlation key.  In `pump`, retain normal dequeue after a successful
  non-empty response.  For `errCodexTurnSubmitUnknown`, set `disabled = true`,
  clear the active reservation, and return the error without treating a queued
  action as delivered.  In `ActionSinkWithContext`, return that terminal error
  so `watchLoop` exits through `watchSinkError` and the supervisor emits its
  existing disabled-monitor report.  Keep App Server closed behavior intact.

- [ ] **Step 4: Run the focused bridge tests.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal279-go-cache go test ./cmd/atct -run 'TestCodexMonitor(UnknownSubmission|ObservedTurnStarted|QueueDeliversFIFOAfterIdle|ReviewQueue)' -count=1 -v
  ```

  Expected: PASS.

- [ ] **Step 5: Commit Task 1.**

  ```sh
  git add cmd/atct/codex_monitor.go cmd/atct/codex_monitor_test.go
  git commit -m "fix: retain unacknowledged Codex monitor actions"
  ```

### Task 2: Prove rejection lifecycle recovery without weakening receipt suppression

**Files:**

- Modify: `cmd/atct/codex_monitor_test.go:TestCodexMonitorReviewQueueUsesHandoffGeneration`.
- Modify: `cmd/atct/codex_monitor_lifecycle_test.go` only if an end-to-end bound-monitor fake is already available.

**Interfaces:**

- Consumes: `watchAgentAction` from the shared selector, including
  `controlOnly`, handoff ID, and RFC3339Nano generation.
- Produces: one confirmed turn for an unreceived plan/goal/task rejection; no
  turn for a review receipt; an uncertain turn surfaces terminally instead of
  being replaced by liveness.

- [ ] **Step 1: Write failing lifecycle cases.**

  Table-drive `plan.handoff.review.reject`, `goal.handoff.review.reject`, and
  `task.handoff.review.reject`.  For each, enqueue an old review request, then
  its control-only review receipt, then a newer rejection while the bridge is
  active.  Queue `monitor.liveness` after the rejection.  Assert before idle
  that the queue holds `[rejection, liveness]`, then complete turns and assert
  that order.  Add the unknown-result variant: assert a terminal sink error,
  exactly one `StartTurn` call, and no liveness turn.  Recreate the watcher with
  fresh in-memory delivery maps against the unchanged rejection snapshot and
  assert it selects that rejection once.

- [ ] **Step 2: Run the lifecycle regression before the fix.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal279-go-cache go test ./cmd/atct -run 'TestCodexMonitor(ReviewQueueUsesHandoffGeneration|RejectionDelivery)' -count=1 -v
  ```

  Expected: the unknown-result variant fails before Task 1 because the watcher
  treats the bridge error as recoverable and later work can mask it; receipt-only
  cases remain passing and produce no receipt turn.

- [ ] **Step 3: Keep the selector and generation rules unchanged.**

  Do not change `selectWatchAgentAction`, `controlOnly`,
  `reviewControlGenerations`, or `pruneQueuedReviewActionsLocked`.  Adjust only
  test fixtures if Task 1's accepted-reservation state changes test timing.

- [ ] **Step 4: Run all focused delivery tests.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal279-go-cache go test ./cmd/atct -run 'Test(CodexMonitor|MonitorBindingLoop|WatchAgentActionSelector|ClaudeAndCodexAgentActionParity)' -count=1 -v
  ```

  Expected: PASS; each confirmed rejection is submitted once, each uncertain
  attempt stops visibly without a duplicate, review receipts still inject no
  agent turn, and a fresh monitor can select the durable rejection once.

- [ ] **Step 5: Commit Task 2.**

  ```sh
  git add cmd/atct/codex_monitor_test.go cmd/atct/codex_monitor_lifecycle_test.go
  git commit -m "test: cover Codex rejection action delivery"
  ```

## Plan self-review

- Spec coverage: Task 1 covers the identified bridge loss boundary; Task 2
  covers plan, goal, and task rejection behavior plus review-receipt retention.
- No persistent state, daemon operation, or transport-specific selector fork is
  introduced.
- Later task names and interfaces use only existing bridge and watcher types.
