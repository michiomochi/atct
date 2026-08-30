# Codex App Server Read Limit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow the Codex monitor to receive finite, large App Server RPC responses without changing small-response or retry behavior.

**Architecture:** Configure Coder WebSocket's per-message limit at the two points that create a real `*websocket.Conn`. Reuse the existing 128 MiB policy constant and retain the read-loop guard as defense in depth.

**Tech Stack:** Go, `github.com/coder/websocket`, Unix-domain HTTP transport.

## Global Constraints

- Change only `cmd/atct/codex_monitor.go`, its corresponding test, and this Goal 214 spec/plan.
- Keep `codexAppServerMessageMaxBytes` at `128 << 20`; never use an unlimited (`-1`) read limit.
- Do not refactor RPC dispatch, retry timing, or notification schema decoding.
- Write and run the regression test while the production code still lacks `SetReadLimit` and record its expected failure.

---

### Task 1: Prove and fix the App Server receive limit

**Files:**
- Modify: `cmd/atct/codex_monitor_test.go` near `TestCodexAppServerRPC`
- Modify: `cmd/atct/codex_monitor.go:57-91`
- Modify: `doc/specs/2026-08-31-codex-app-server-read-limit.md`
- Modify: `doc/plans/2026-08-31-codex-app-server-read-limit.md`

**Interfaces:**
- Consumes: `websocket.Dial` returns `*websocket.Conn`, whose default message read limit is 32 KiB and whose `SetReadLimit(int64)` sets one finite per-message limit.
- Produces: every real Codex App Server connection has read limit `int64(codexAppServerMessageMaxBytes)` before `codexAppServer.readLoop` starts.

- [x] **Step 1: Write the failing regression test**

Create a real Unix-socket WebSocket server in `codex_monitor_test.go`. On its
`thread/list` request, send a valid JSON-RPC result whose serialized message is
larger than `32 << 10` but smaller than `codexAppServerMessageMaxBytes`; call the
client's normal thread-list RPC path and assert it returns the response rather
than a read error.

- [x] **Step 2: Run the regression test before production code changes**

Run: `go test ./cmd/atct -run '^TestCodexAppServerAcceptsLargeThreadListResponse$' -count=1`

Expected: FAIL because Coder WebSocket returns its default message-too-big error
for the larger-than-32-KiB response.

Observed before the production change: both constructor subtests failed with
`websocket: message too big: read limited at 32769 bytes`.

- [x] **Step 3: Apply the minimal production fix**

Immediately after each successful `websocket.Dial` in `newCodexAppServer` and
`dialCodexAppServer`, call:

```go
conn.SetReadLimit(int64(codexAppServerMessageMaxBytes))
```

Do not change `codexWebSocket`, `newCodexAppServerWithConn`, or `readLoop`.

- [x] **Step 4: Verify the regression and existing behavior**

Run:

```bash
go test ./cmd/atct -run '^TestCodexAppServerAcceptsLargeThreadListResponse$' -count=1
go test ./cmd/atct -run '^TestCodexAppServer(RPC|UnixSocketTransport|LifetimeOutlivesDialContext|RejectsMalformedResumeResponse)$' -count=1
```

Expected: the large response is received; existing small RPC, Unix transport,
and lifetime behavior all pass.

- [ ] **Step 5: Run the related package suite for review**

Not run: the task explicitly limits verification to the regression and the
listed App Server tests.

Run: `go test ./cmd/atct -count=1`

Expected: PASS with no changed fallback/retry behavior.
