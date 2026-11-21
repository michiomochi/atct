# Codex monitor remote TUI attachment implementation plan

**Goal:** Start the monitored Codex TUI without paginated baseline discovery.

**Architecture:** Start a fresh App Server for the monitor and launch remote Codex
without a `resume` argument. The remote TUI creates the only user thread on that
private socket; its first valid `thread/started` notification attaches the monitor
bridge to that thread.
Apply three leading-argument boundaries: legacy no-role raw `resume` bypasses monitor
setup and invokes normal Codex with the exact original argv; explicit monitor raw
`resume` fails before setup without normal fallback; and non-resume explicit args are
passed unchanged after the remote socket prefix. Automatic zero-args shim behavior
is unchanged.

## Constraints

- Do not lengthen `codexMonitorSetupTimeout`.
- Retain full 128 MiB App Server read limit, strict status decoding, and remoteControl no-op behavior.
- Preserve fast automatic fallback when App Server connection/initialization fails.
- Reject explicit leading raw `resume` before any setup or fallback.
- Do not strip or reinterpret raw args for the owned remote TUI.
- No launcher, shim, user configuration, or unrelated refactoring changes.

### Task 1: Add remote-TUI attachment regression coverage

**Files:** modify `cmd/atct/codex_monitor_lifecycle_test.go`.

1. Write failing lifecycle coverage for legacy no-role leading `resume`, asserting
   `runNormal("codex", originalArgs)` with no monitor process.
2. Write failing lifecycle coverage for explicit leading `resume`, asserting an
   explicit error before project/scope resolution, App Server, bridge, watcher, TUI,
   or normal fallback.
3. Extend the explicit non-resume monitor test to assert exactly `--remote <socket>`
   followed by unchanged original args.
4. Prove a queued monitor event waits for the remote TUI's `thread/started`
   notification, then starts a turn on that thread without a monitor-owned
   `thread/start`.
5. Run `go test ./cmd/atct -run TestCodexMonitor -count=1` and observe the new
   explicit-boundary test fail before production changes.

### Task 2: Attach the monitor to the remote-TUI thread

**Files:** modify `cmd/atct/codex_monitor_supervisor.go`.

1. Before monitor setup, reject explicit leading raw `resume` with an error and no
   normal fallback; keep the legacy no-role exact pass-through before all setup.
2. Start the remote TUI without `resume`, preserve the original non-resume explicit
   args unchanged, and attach the bridge to the first valid `thread/started` from
   the private App Server.
3. Preserve monitor disable behavior for bridge/watch failures, automatic zero-args
   shim, and setup fallback semantics.
4. Run the focused lifecycle test, then `go test ./cmd/atct -count=1`.

### Task 3: Review boundary conditions

**Files:** inspect `cmd/atct/codex_monitor_supervisor.go` and the lifecycle test only.

1. Confirm no path uses a partial ID map for discovery, monitor-owned `thread/start`,
   or a remote TUI `resume` prefix.
2. Confirm explicit leading raw `resume` cannot start setup or normal Codex, while
   legacy leading `resume` preserves exact argv.
3. Confirm App Server connection failure still uses `codexMonitorSetupFailure`,
   preserving automatic fallback and explicit monitor failure semantics.
4. Confirm only Goal 216 implementation, tests, spec, and plan changed.
