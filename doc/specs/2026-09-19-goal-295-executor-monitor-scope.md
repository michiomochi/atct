# Executor monitor-check recognizes every assigned task scope

## Goal

Allow `atct_role` for an executor to pass its PreToolUse monitor check when the
executor has a live monitor for any received, incomplete task handoff.

## Confirmed root cause

`Store.MonitorAssignment` correctly retains every executor task in
`MonitorAssignment.Tasks`, but the executor branch leaves the top-level goal
empty. `Daemon.monitorCheck` reduces the assignment to one `roleAssignment` and
only adds a goal filter when that top-level value is non-zero. The resulting
query uses `goal_id IS NULL`, while the executor monitor health row has the
task's real goal ID, so an otherwise healthy executor is denied.

## Behavior

- A session with no project or received role assignment remains allowed to
  enter the handoff flow; it cannot have a monitor scope yet.
- A commander checks for a live project-level monitor.
- A subcommander checks for a live monitor for its goal.
- An executor checks every project/goal scope in `MonitorAssignment.Tasks` and
  is allowed when at least one matching monitor is live. It remains denied when
  all assigned scopes have no live monitor.
- A live monitor is still required for every assigned role; the fix does not
  weaken the no-live-monitor protection.

## Multiple executor assignments decision

Match any assigned scope. The monitor binding loop starts one task-scoped watch
per received task, and task handoffs can belong to different goals. Choosing
the first task would deny a session when that first monitor is stopped but a
monitor for another assigned task is healthy. Checking any scope preserves the
guard while matching the monitor that can actually receive the wakeup.

## Scope and non-goals

- Change the daemon monitor-check scope derivation and its regression tests.
- Keep the existing monitor-health schema, lease, SQL matching, role response,
  and monitor binding API unchanged.
- Do not modify adjacent monitor, stop-check, watch-delivery, or handoff
  recovery work.

## Verification

The regression suite must cover live commander, subcommander, and executor
scopes; refusal when an assigned session has no live monitor; executor
assignments spanning multiple goals; and a received executor task reaching
`atct_role(expected_role=executor)`. Run the focused daemon and MCP tests, then
`go test ./... -count=1` and `git diff --check`.
