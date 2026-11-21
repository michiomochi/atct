# Goal 260: Reconcile with main (v0.63.18)

## Finding

The Goal 260 worktree sat on v0.63.0 with 215 uncommitted changes; main was 162
commits ahead. Most of what Goal 260 set out to build now exists in main under
other goals:

| Goal 260 item | main (f4b91cf) |
|---|---|
| next action named after each transition | `internal/daemon/next_step.go`, parity with `doc/execution-flow.md` in `next_step_doc_test.go` |
| role-checked handoff transitions | role API authorization (`role_authorization_test.go`) |
| executor monitor follows the session's current task | `monitor_bindings` + `ListMonitorExecutorAssignments`; `runMonitorBindingLoop` re-derives scopes every second |
| no liveness for a task already in review | `watchExecutorLivenessActionable` |

## Decision

Do not port the predecessor's `internal/workflow`, `atct_workflow_next`, or the
executor current-handoff projection. They would duplicate next_step.go and the
monitor binding, and the projection contradicts the lease/binding authority the
accepted plan told us not to bypass. The predecessor's tracked changes are kept
on branch `wt/goal-260-snapshot` (d81bbd8) as a reference, not for merge.

The one piece main lacks is the end-to-end proof. Land it on main:
`doc/specs/2026-09-18-goal-260-workflow-monitor-e2e.md` is the contract, with
one correction. The monitor under test is driven by the real binding path:
`/api/monitor-bindings/<token>` and `runMonitorBindingLoop`, not a test-side
scope refresh. The previous review (task 1316) was rejected for exactly that.

Tasks 1247-1251 produced code that will not land; they are closed without
commits and recorded as superseded.

## Non-goals

No production change unless the E2E exposes a real defect in main. If it does,
stop and report the defect in the review request; do not fix it silently.
