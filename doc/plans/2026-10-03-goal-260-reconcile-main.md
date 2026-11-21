# Goal 260 E2E on main — Implementation Plan

Spec: `doc/specs/2026-10-03-goal-260-reconcile-main.md`, contract in
`doc/specs/2026-09-18-goal-260-workflow-monitor-e2e.md`. Base: branch
`wt/goal-260` at main f4b91cf.

### Task 1316 (rejection correction, same handoff)

- [ ] Reference: `git show wt/goal-260-snapshot:cmd/atct/workflow_monitor_e2e_test.go`
  (567 lines; helpers for daemon, transport, action recorder). Reuse its
  structure, drop `refreshExecutorScope` and any use of the removed projection.
- [ ] Create `cmd/atct/workflow_monitor_e2e_test.go` with
  `TestWorkflowMonitorEndToEndContract`. Executor monitors run through
  `runMonitorBindingLoop` with a token bound via the real
  `/api/monitor-bindings/<token>` path. Commander and subcommander monitors use
  the same binding path.
- [ ] Assert, per the spec table: each lifecycle action reaches only its role;
  executor A, after its task review request, emits no `monitor.liveness`; after
  the subcommander completes the task the binding drops A's scope; executor B's
  received task is delivered throughout; the merge-continuation marker follows
  `goal.review.complete`.
- [ ] Verify (only these):
  `GOCACHE=/private/tmp/goal260-go-cache go test ./cmd/atct -run TestWorkflowMonitorEndToEndContract -count=1`,
  `GOCACHE=/private/tmp/goal260-go-cache go test ./cmd/atct ./internal/daemon ./internal/store ./internal/httpapi -count=1`,
  `git diff --check`. Failures that exist on a clean main must be shown with
  `git stash`-free evidence (run on the same tree before adding the test) and
  reported, not fixed.
- [ ] Changed paths expected: the new test file only.
