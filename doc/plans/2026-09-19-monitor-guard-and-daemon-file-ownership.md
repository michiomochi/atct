# Plan: Monitor Guard and Daemon File Ownership

Spec: `doc/specs/2026-09-19-monitor-guard-and-daemon-file-ownership.md`

## 1. Stop the daemon from deleting another daemon's files

- `internal/daemonctl/registry.go`: add `RegistryOwnedBy(dir, pid)`.
- `cmd/atct/main.go`: wrap the cleanup `RemoveRegistry` / `os.Remove(sock)` in
  `RegistryOwnedBy(dir, os.Getpid())`.
- Test: `TestRegistryOwnedByRejectsForeignPID`,
  `TestRegistryOwnedByRejectsMissingRegistry`.

## 2. Stop `clearStale` from deleting a live socket

- `internal/daemonctl/ensure.go`: `clearStale` returns `ErrUnresponsive` when
  `SocketAnswers(SocketPath(dir))`. Reuse the existing helper; do not add one.
- Test: `TestClearStaleKeepsAnsweringSocket`,
  `TestClearStaleRemovesDeadSocket`, `TestClearStaleAcceptsMissingSocket`.
  Sockets need a short path; `t.TempDir()` exceeds `sun_path` on macOS.

## 3. Report Monitor liveness for a role scope

- `internal/store/queries/monitor_health.sql`: `CountLiveMonitorsForScope`
  over `project_id`, `role`, `goal_id`, the lease cutoff, and
  `stopped_at IS NULL`. Regenerate with `sqlc generate`.
- `internal/store/monitor_live.go`: `MonitorLiveScope` and
  `HasLiveMonitorForScope`, reusing `MonitorHealthLease`.
- Test: fresh, expired, stopped, and other-goal rows.

## 4. Expose the check over RPC

- `internal/daemon/monitor_check.go`: `monitorCheck` resolves the session key,
  derives the role, and blocks with the Monitor start instructions for both
  harnesses.
- `internal/daemon/handler.go`: dispatch `session.monitor_check`.

## 5. Deny from PreToolUse in both harnesses

- `cmd/atct/monitor_check.go`: `atct monitor-check --hook-input`. Emit
  `hookSpecificOutput.permissionDecision = "deny"`. Pass through when
  `isATCTTool(tool_name)` is false. Fail closed on any error.
- `cmd/atct/main.go`: register the subcommand and its `--hook-input` flag.
- `hooks/pre-tool-use`: Claude script, mirroring `hooks/stop`.
- `hooks/claude-hooks.json`: `PreToolUse` matcher `mcp__atct__.*`.
- `hooks/codex-hooks.json`: `PreToolUse` with the inline version guard.

## 6. Release

Bump `.claude-plugin/plugin.json`, `.codex-plugin/plugin.json`, and the
version guard in `hooks/codex-hooks.json`, then tag and run goreleaser.

The running daemon must be replaced for the fix to take effect. Wait for the
old process to exit before starting the new one; starting after a fixed sleep
is what triggered the overlap this plan removes.
