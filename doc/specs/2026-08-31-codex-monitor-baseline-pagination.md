# Codex monitor baseline pagination

## Problem

The monitor currently performs App Server connect/initialize and every `thread/list`
page under one 10-second setup context.  A project with a large thread history can
therefore exhaust setup time in baseline pagination and fall back to ordinary Codex
before the monitored TUI starts.

Leading `resume` arguments are a compatibility boundary: a legacy no-role monitor
must preserve an explicit user resume request instead of creating a new owned thread.

## Decision

Remove baseline listing and ID-difference discovery from monitor startup. Cursor
semantics are opaque, and a `thread/start` issued on the monitor connection does
not create a rollout that a second remote TUI client can resume. The monitor starts
a fresh, private App Server and launches the remote TUI without a `resume` prefix.
The TUI creates its own thread; the monitor attaches to the first valid
`thread/started` notification from that private socket. No other client is given
the socket, so this identifies the monitored TUI without pagination semantics.

Leading raw `resume` has three explicit boundaries. A legacy no-role monitor calls
`runNormal("codex", originalArgs)` without starting App Server, `thread/start`,
bridge, watcher, or TUI. An explicit monitor (role, goal, or task scope) rejects
leading raw `resume` with an explicit error before any setup, including
`runNormal`, so its scope contract cannot be silently discarded. For a non-resume
explicit monitor, the TUI args are exactly `--remote <socket>` followed by the
original args; the supervisor does not strip or reinterpret a resume selector.
Automatic shim behavior with zero args is unchanged.

## Lifecycle and failure behavior

- Setup succeeds after connection and initialization; it performs no `thread/list`
  or monitor-owned `thread/start`.
- TUI, watch, bridge, record registration, strict thread status decoding, read limit,
  and notification handling retain their existing behavior.
- The first valid `thread/started` notification from the fresh private App Server
  attaches the bridge to the remote TUI thread. Later `thread/started` and unrelated
  notifications, including remoteControl, remain no-ops.
- App Server absence or connect/initialize failure remains inside the short setup
  timeout and keeps the present automatic fallback / explicit failure policy.
- Explicit leading raw `resume` is rejected before project/scope resolution and
  cannot fall back to normal Codex. Other setup fallbacks preserve the original raw
  args unchanged.

## Tests

Use controllable channels in the lifecycle fake, not wall-clock sleeps or a changed
10-second constant. Prove the three leading-argument boundaries: legacy no-role
leading `resume` calls normal Codex with executable `codex` and the exact original
argv without starting any monitor process; explicit leading `resume` returns an
error before project/scope/App Server/bridge/watcher/TUI/normal execution; and
non-resume explicit args are preserved after the remote socket prefix. Also prove
that the remote TUI receives no owned `resume` argument, its `thread/started`
notification attaches queued monitor events to that thread, and the monitor never
starts a thread itself. Keep status-decode, remote-control no-op, automatic fallback,
and explicit scope-failure coverage.
