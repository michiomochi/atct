# Goal 280 task-create delivery implementation plan

> The subcommander owns design and review. An executor owns the implementation
> task after the plan handoff is accepted.

## Goal

Repair the Codex monitor delivery boundary for the durable task-create handoff
created after an accepted plan, without changing Goal 279's submission failure
semantics or the store/daemon lifecycle.

## Scope

- Production: `cmd/atct/watch_delivery.go` and the small shared predicate it
  exposes to the Codex conversion path in `cmd/atct/codex_monitor.go`.
- Tests: focused action identity and Codex monitor tests under `cmd/atct/`.
- Already recorded: the spec and this plan under `doc/specs/` and `doc/plans/`.

Do not modify store/schema/daemon code, monitor binding, review coalescing, or
the adjacent Goal 279 bridge failure handling.

## Task 1: repair and verify task-create action identity

### Implementation contract

1. Add the minimum shared predicate for lifecycle names containing `handoff.`.
2. Replace the dot-only checks in `watchActionDeliveryIdentity` and
   `codexMonitorActionFromWatchAction` with that predicate.
3. Preserve the existing `watchAgentAction` selector, control-only rule, bridge
   queue reservation, unknown-submission retention, and fresh reconciliation
   recovery behavior.

### TDD and focused checks

1. Add a failing regression first. It must assert that a formatted
   `task.create_handoff.request` action has a delivery-key subject and Codex
   `handoffID` equal to the durable handoff ID; the current dot-only code fails
   this assertion.
2. Cover request, receive, and completed reconciliation states. Reconcile the
   requested state twice with one delivery map and one bridge, and assert one
   queued/started action. Use a fresh bridge/delivery map to prove the same
   durable request can be recovered after a submission failure.
3. Exercise the existing `errCodexTurnSubmitUnknown` path for the task-create
   request. Assert the queue is retained, the bridge is disabled as already
   specified by Goal 279, and no receive action is implied.
4. Keep normal handoff and review-rejection tests passing; add task-create
   cases to the existing canonical lifecycle table where that is the shortest
   coverage.

### Verification command

```sh
GOCACHE=/private/tmp/goal280-go-cache go test ./cmd/atct -run 'Test(CodexMonitor|HandoffOnlyReconciliation|ClaudeAndCodexAgentActionParity)' -count=1 -v
```

If the focused pattern is too broad for the package's test names, use the
smallest equivalent `go test ./cmd/atct -run ...` pattern and record the exact
command in the completion report. Do not run `go test ./...` for this task.

Then run the package-level focused verification:

```sh
GOCACHE=/private/tmp/goal280-go-cache go test ./cmd/atct -count=1
git diff --check
```

## Completion boundary

The executor must request task review with: files changed, tests run and their
results, the unknown-submission/recovery evidence, and anything not verified.
The subcommander will inspect the diff and tests before accepting the task.
Only after every task is accepted will the subcommander submit the goal review
request.
