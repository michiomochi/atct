# Goal 292 implementation plan

## Goal

Keep a subcommander alive and remind it until every requested child task
handoff has been received by a monitored executor.

## Design

Use the existing task-handoff timestamps as the single source of truth:
`RequestedAt != nil && ReceivedAt == nil` means the executor has not received
the handoff.  Keep the change in the two existing role-aware paths:

- `internal/daemon/stop_check.go` prevents a subcommander stop while its own
  child handoff is request-only.
- `cmd/atct/watch_scope.go` makes a matching request-only handoff actionable;
  the existing one-minute prompt in `cmd/atct/watch.go` does the notifying.

No new state, schema, timer, pane launcher, recovery policy, or abstraction is
needed.

## Implementation task

### Add request-only handoff guards

Files:

- `internal/daemon/stop_check.go`
- `internal/daemon/stop_check_test.go`
- `cmd/atct/watch_scope.go`
- `cmd/atct/watch_test.go`

Steps:

1. Add a failing daemon test for a subcommander with an outgoing task handoff
   whose request is recorded but whose receipt is nil. Assert that the stop
   check blocks and identifies the task handoff. Keep the existing received
   task behavior as the negative case.
2. Extend the role-aware watch liveness regression table with a subcommander
   request-only task handoff that must be actionable, while a received task
   handoff remains non-actionable while the executor is working.
3. In the daemon stop check, inspect the current subcommander's open child task
   handoffs and return the existing stop-block error shape for request-only
   rows before allowing the stop.
4. In the subcommander watch predicate, return actionable for a matching open
   request-only task handoff. Preserve the current review/rejection branches
   and the existing received-executor suppression.
5. Run `gofmt` on changed Go files and verify with:

   ```text
   go test ./internal/daemon -run 'TestSessionStopCheck|TestStopCheckSubcommander' -count=1
   go test ./cmd/atct -run 'TestWatchLiveness' -count=1
   go test ./internal/daemon ./cmd/atct -count=1
   ```

The executor must leave changes uncommitted and submit its review request with
the test results. The subcommander will review the handoff, commit the goal,
and request goal review after all handoffs are complete.

## Non-goals

- Session heartbeat schema or recovery changes.
- Automatic executor pane creation or automatic handoff recovery.
- Changes to project-wide `atct pending` formatting.
- Changes belonging to Goals 286, 280, or 275.
