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
cannot confuse another client's thread.

Leading raw `resume` has three explicit boundaries. A legacy no-role monitor calls
`runNormal("codex", originalArgs)` without starting App Server, `thread/start`,
bridge, watcher, or TUI. An explicit monitor (role, goal, or task scope) rejects
leading raw `resume` with an explicit error before any setup, including
`runNormal`, so its scope contract cannot be silently discarded. For a non-resume
explicit monitor, the TUI args are exactly `--remote <socket> resume <ownedID>`
followed by the original args; the supervisor does not strip or reinterpret a
resume selector. Automatic shim behavior with zero args is unchanged.

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
- Explicit leading raw `resume` is rejected before project/scope resolution and
  cannot fall back to normal Codex. Other setup fallbacks preserve the original raw
  args unchanged.

## Tests

Use controllable channels in the lifecycle fake, not wall-clock sleeps or a changed
10-second constant. Prove the three leading-argument boundaries: legacy no-role
leading `resume` calls normal Codex with executable `codex` and the exact original
argv without starting any monitor process; explicit leading `resume` returns an
error before project/scope/App Server/bridge/watcher/TUI/normal execution; and
non-resume explicit args are preserved after the owned `resume <id>` prefix. Also
prove that `thread/start` receives the project CWD and its response ID is used by
the remote TUI, that a broadcast `thread/started` is not adopted, and that
`thread/start` failure does not launch the TUI or watcher. Keep status-decode,
remote-control no-op, automatic fallback, and explicit scope-failure coverage.
