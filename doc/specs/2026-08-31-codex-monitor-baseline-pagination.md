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
semantics are opaque and `thread/started` is broadcast, so neither identifies the
remote TUI exactly. The monitor connection calls official `thread/start` with the
project CWD and takes `response.thread.id` as the owned session ID. It then launches
the remote TUI with `resume <id>`. This request/response association is exact and
cannot confuse another client's thread. The legacy no-role path is an exception for
raw args beginning with `resume`: it calls `runNormal("codex", originalArgs)` without
starting App Server, `thread/start`, bridge, watcher, or TUI. Explicit monitor mode
continues to create and resume an owned thread; its TUI args contain the owned
`resume <id>` followed by non-resume original args. Automatic shim behavior with
zero args is unchanged.

## Lifecycle and failure behavior

- Setup succeeds after connection, initialization, and monitor-owned `thread/start`;
  it performs no `thread/list`.
- TUI, watch, bridge, record registration, strict thread status decoding, read limit,
  and notification handling retain their existing behavior.
- `thread/started` is ignored for session discovery; the monitor uses only the
  `thread/start` response ID. Unrelated notifications, including remoteControl,
  remain no-ops.
- App Server absence, connect/initialize failure, or `thread/start` failure remains
  inside the short setup timeout and keeps the present automatic fallback / explicit
  failure policy.
- Any setup fallback preserves the original raw args unchanged.

## Tests

Use controllable channels in the lifecycle fake, not wall-clock sleeps or a changed
10-second constant. Prove that `thread/start` receives the project CWD and its
response ID is used by the remote TUI's `resume` arguments. Prove that a broadcast
`thread/started` is not adopted and that `thread/start` failure does not launch the
TUI or watcher, while automatic fallback and explicit failure remain intact. Keep
status-decode and remote-control no-op coverage. Prove that a legacy no-role leading
`resume` bypass calls normal Codex with executable `codex` and the exact original
argv without starting any monitor process.
