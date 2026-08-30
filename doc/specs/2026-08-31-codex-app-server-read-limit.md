# Codex App Server read limit

## Problem

`github.com/coder/websocket` limits inbound messages to 32 KiB unless its
connection read limit is changed. Codex App Server `thread/list` responses can
exceed that default. The monitor therefore drops the socket, retries until its
10-second connection deadline, and reports `connect App Server: context deadline
exceeded`.

`codexAppServerMessageMaxBytes` already defines ATCT's finite 128 MiB policy,
but it currently runs only after `Conn.Read` has allocated the message.

## Scope

Apply that existing 128 MiB policy to each real WebSocket returned by
`websocket.Dial` in `newCodexAppServer` and `dialCodexAppServer`, before it is
wrapped by `newCodexAppServerWithLifetime`. Do not change retry timing, RPC
routing, the post-read defensive check, or notification schema handling.

## Behavior

- A JSON-RPC `thread/list` response larger than 32 KiB and below 128 MiB is
  received and delivered to its pending RPC call.
- Ordinary small RPC responses retain their current behavior.
- A message beyond 128 MiB remains rejected; the limit is never disabled.
- Both direct App Server construction and the supervisor's retry connection
  path configure the limit, so a later caller cannot reintroduce the default.

## Test strategy

Add an integration-style regression test adjacent to `TestCodexAppServerRPC` in
`cmd/atct/codex_monitor_test.go`. It will use a real Coder WebSocket connection
over the existing Unix-socket transport pattern, send a valid `thread/list`
RPC response larger than 32 KiB, and assert that the client receives it. Before
the production change, Coder WebSocket's documented 32 KiB default makes this
test fail; after setting the finite read limit it passes. Existing RPC and
fallback-oriented tests remain the protection for small responses and retry
behavior.

## Non-goal / surprise

The role-aware monitor start in this goal's own executor pane fell back because
`remoteControl/status/changed` supplied a string where the current notification
schema expects `codexThreadStatus`. That unrelated schema mismatch is not
modified by this goal.
