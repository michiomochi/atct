# Codex monitor baseline pagination implementation plan

**Goal:** Start the monitored Codex TUI without paginated baseline discovery.

**Architecture:** Call `thread/start` from the monitor App Server connection and pass
its response ID to remote Codex `resume`; notifications are not session identity.
For legacy no-role raw args beginning with `resume`, bypass monitor setup and invoke
normal Codex with the exact original argv. Explicit monitor mode retains owned
thread start/resume; automatic zero-args shim behavior is unchanged.

## Constraints

- Do not lengthen `codexMonitorSetupTimeout`.
- Retain full 128 MiB App Server read limit, strict status decoding, and remoteControl no-op behavior.
- Preserve fast automatic fallback when App Server connection/initialization or
  monitor-owned `thread/start` fails.
- No launcher, shim, user configuration, or unrelated refactoring changes.

### Task 1: Add exact-start and failure regression coverage

**Files:** modify `cmd/atct/codex_monitor_lifecycle_test.go`.

1. Write a failing lifecycle test that asserts `thread/start` receives project CWD and
   remote TUI receives `resume <response ID>`.
2. Write a failing lifecycle test that proves a broadcast `thread/started` is not
   adopted for session identity.
3. Write a failing lifecycle test for pre-launch `thread/start` failure: no TUI or
   watcher starts, and automatic mode uses the original-argument fallback.
4. Extend the owned-thread argv test to prove the generated command has exactly
   `--remote <socket> resume <ownedID>` followed by non-resume original args.
5. Write a failing lifecycle test proving legacy no-role leading `resume` calls
   `runNormal("codex", originalArgs)` without starting App Server or TUI.
6. Run `go test ./cmd/atct -run TestCodexMonitor -count=1` and observe the test fail
   before production changes.

### Task 2: Replace baseline discovery with monitor-owned thread start

**Files:** modify `cmd/atct/codex_monitor.go`, `cmd/atct/codex_monitor_supervisor.go`.

1. Remove startup baseline pagination and `DiscoverThread` goroutines.
2. Add `StartThread(ctx, cwd)` to the monitor App Server interface and implement the
   protocol request/response decode.
3. Start remote TUI with `resume <thread ID>` while preserving non-monitor passthrough
   behavior and existing setup fallback; remove any leading raw `resume` selector
   before appending non-resume original args.
4. Preserve monitor disable behavior for bridge/watch failures without signalling TUI.
5. Bypass all monitor setup for legacy no-role args beginning with `resume`, invoking
   `runNormal("codex", originalArgs)` exactly; leave automatic zero-args unchanged.
6. Run the focused lifecycle test, then `go test ./cmd/atct -count=1`.

### Task 3: Review boundary conditions

**Files:** inspect the two implementation files and lifecycle test only.

1. Confirm no path uses a partial ID map for discovery.
2. Confirm `thread/start` failure still uses `codexMonitorSetupFailure`, preserving
   automatic fallback and explicit monitor failure semantics.
3. Confirm only Goal 216 implementation, tests, spec, and plan changed.
