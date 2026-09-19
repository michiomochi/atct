# Monitor Guard and Daemon File Ownership

## Goal

Refuse ATCT tool calls from a session that has no live Monitor, and stop the
daemon churn that kills Monitors in the first place.

## Symptom

Reported (2026-09-18): ATCT runs while no Monitor is attached, so wakeup never
reaches the session. Measured the same day: of 25 `monitor_health` rows, 18
were `state = 'healthy'`, and one of those named a PID that no longer existed
with `last_seen_at` 3.3 hours stale.

## Cause

Two defects, one feeding the other.

**Shared files are removed without ownership.** `runDaemon` cleans up on exit
with `RemoveRegistry(dir)` and `os.Remove(sock)`. Both paths are shared by
every daemon. A `kill`ed daemon does not exit immediately, so a replacement
started a second later reaches `WriteRegistry` first; the older daemon then
runs its cleanup and deletes the *newer* daemon's registry and socket. Both
processes were observed alive at once on 2026-09-18.

**A missing registry is read as "no daemon".** With the registry gone,
`Ensure` takes the `ErrNoRegistry` branch and calls `clearStale`, which
removes the socket unconditionally. The surviving daemon keeps the HTTP port,
so the daemon `clearStale` starts in its place fails with
`bind: address already in use` and never recreates the socket. Every RPC path
-- MCP, `stop-check`, the CLI -- is then dead while the daemon still answers
HTTP 200. Observed for 36 minutes.

`Ensure` already guards the symmetric case: when a registry exists and
`ProcessAlive(reg.PID)`, it refuses with `ErrUnresponsive`. Only the
`ErrNoRegistry` branch lacked that check.

## Scope

- `runDaemon` removes the registry and socket only when the registry still
  records this process.
- `clearStale` refuses to remove a socket that answers.
- `session.monitor_check` reports whether a session's role scope has a Monitor
  inside the existing `MonitorHealthLease`.
- `atct monitor-check --hook-input` denies ATCT tool calls when it does not.
- Both harnesses run it from `PreToolUse`.

## Decision

Put the liveness gate in a hook rather than in the RPC core. Every ATCT tool
call already crosses `PreToolUse`, and the hook runs where the session key is
known, as `session-key` and `stop-check` already do. Guarding `dispatch`
instead would need a session identity that is not present on every method.

Match the Monitor on `project_id`, `role`, and `goal_id`.
`monitor_health.agent_session_id` exists but is `0` for every row in
production, so keying on it would deny every session.

Reuse `ListMonitorHealth`'s lease window. A row that stopped, or stopped
heartbeating, is not live; `state` alone is not evidence.

Codex accepts Claude Code's `hookSpecificOutput` wire, so one CLI subcommand
serves both harnesses. The gate applies only to tool names prefixed
`mcp__atct__` or `atct__`, so no harness-specific matcher is required.

## Verification

- `TestRegistryOwnedByRejectsForeignPID` and the `clearStale` tests reproduce
  both deletion paths.
- `HasLiveMonitorForScope` tests cover fresh, expired, stopped, and
  other-goal rows.
- `monitorCheckDeny` emits the deny wire; `isATCTTool` gates the scope.
