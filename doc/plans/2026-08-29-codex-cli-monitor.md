# Plan: explicit Codex CLI monitor path

> **For the implementer:** follow this plan task by task. Keep the normal
> `codex` and `codex exec` paths untouched. Use test-first development: add a
> focused failing test, run the named test, implement the smallest behavior,
> then rerun it and format the changed Go files.

**Goal:** Add an opt-in `atct codex monitor` supervisor that runs interactive
Codex through a local App Server, reuses ATCT's project-scoped watcher, and
delivers eligible events to the same thread without interrupting an active
turn.

**Architecture:** Keep the CLI entry point in `cmd/atct`. Put the App Server
JSON-RPC client, thread/session bridge, event queue, and supervisor orchestration
in a new `cmd/atct/codex_monitor.go` so it can reuse the existing package-main
watch seams. Put persistent process-record and orphan-reap behavior in a new
`internal/daemonctl/codexmonitor.go`. Use the existing
`github.com/coder/websocket` dependency with an HTTP transport whose
`DialContext` connects to the managed Unix socket. Do not add agmsg or a new
dependency.

**Global constraints:**

- The only automatic behavior is inside the explicit `atct codex monitor`
  command.
- `codex` and `codex exec` are never rewritten or shimmed.
- Use project-level `watchScopeFilter` and `formatWatchDecision`; do not send
  task-level events, raw SSE frames, keepalive messages, or watcher diagnostics
  to Codex.
- Queue while a selected thread is active and deliver after idle/completion via
  serialized `turn/start` requests.
- All setup failures fail open to the real Codex with a visible stderr line.
- State files are atomic, private to the user, and independently registered
  from `~/.atct/watchers`.
- Tests must be deterministic through injected command, clock, dial, and
  process operations where the real operating system is not essential.

## Task 1: Extend the CLI with an explicit monitor entry point

**Files:** `cmd/atct/main.go`, `cmd/atct/main_test.go`.

1. Add a failing parse test for `atct codex monitor -- -m gpt-5` that preserves
   the raw Codex arguments, and for `atct codex monitor stop` that selects the
   stop action. Add a pass-through test for `atct codex monitor exec --help`.
2. Run:

   ```text
   go test ./cmd/atct -run 'TestParseArgs.*Codex|TestCodexMonitor.*PassThrough' -count=1
   ```

   Confirm the new tests fail because the command is unknown.
3. Add the nested `codex monitor` parser without sending Codex flags through
   the ATCT `flag.FlagSet`. Add a raw `codexArgs` field and a distinct stop
   action. Keep usage text explicit about normal `codex`/`exec` behavior.
4. Run the same command and `gofmt -w cmd/atct/main.go cmd/atct/main_test.go`.

## Task 2: Add the monitor process registry and orphan cleanup

**Files:** `internal/daemonctl/codexmonitor.go`,
`internal/daemonctl/codexmonitor_test.go`.

1. Add failing tests for atomic registration, own-record cleanup, dead
   supervisor orphan reaping, live-record preservation, exact project matching,
   bounded stop, and malformed-record removal.
2. Run:

   ```text
   go test ./internal/daemonctl -run 'TestCodexMonitor' -count=1
   ```

   Confirm the tests fail because the registry API does not exist.
3. Implement a small API for:

   - recording supervisor PID, App Server PID, socket, project, and start time;
   - listing records from `~/.atct/codex-monitors`;
   - removing only the caller's record;
   - reaping records whose supervisor is dead and terminating their recorded
     App Server within the existing bounded wait pattern;
   - stopping live records for one exact project without touching other
     projects or live supervisors outside the requested scope.

   Use write-to-temporary-plus-rename for records, reject paths outside the
   managed monitor directory, and retain a record when a live process does not
   exit within the timeout.
4. Run the focused test again, `gofmt`, and `git diff --check`.

## Task 3: Implement the App Server JSON-RPC client

**Files:** `cmd/atct/codex_monitor.go`,
`cmd/atct/codex_monitor_test.go`.

1. Add failing protocol tests using an injected WebSocket dialer or local test
   server. Cover initialize/initialized, request ID matching while unrelated
   notifications arrive, thread/list response decoding, thread/resume, and
   turn/start input with one text item.
2. Run:

   ```text
   go test ./cmd/atct -run 'TestCodexAppServer|TestCodexMonitorRPC' -count=1
   ```

   Confirm the tests fail before the client exists.
3. Implement a minimal JSON-RPC 2.0 client over the already-present
   `github.com/coder/websocket` package. The client must:

   - dial a loopback WebSocket URL through a custom transport connected to the
     managed Unix socket;
   - serialize writes and correlate responses by request ID;
   - keep notifications available to the bridge while calls wait for results;
   - close the read loop and pending calls when the context ends;
   - expose only the methods needed by this monitor.

   Decode only the stable fields needed by this goal: thread ID, cwd, source,
   thread status, turn ID, and event method. Treat malformed or error responses
   as bridge errors rather than panics.
4. Rerun the focused protocol tests, format, and inspect the diff.

## Task 4: Implement event filtering, active-turn queueing, and thread bridge

**Files:** `cmd/atct/codex_monitor.go`, `cmd/atct/codex_monitor_test.go`,
`cmd/atct/watch.go` only if a writer/callback seam is required.

1. Add failing tests for:

   - project-scope `watchScopeFilter` allowing an answer/approval/detection and
     rejecting default-applied/task-internal events;
   - the monitor sink accepting only formatted action lines and ignoring
     watcher diagnostics;
   - FIFO queueing while active and one-at-a-time submission after idle;
   - turn completion and idle status releasing the queue;
   - submission failure retaining the pending item.
2. Run:

   ```text
   go test ./cmd/atct -run 'TestCodexMonitor(Filter|Queue|Delivery)' -count=1
   ```

3. Implement the bridge state machine. Reuse
   `watchSnapshotWithProject` and
   `watchLoopWithEnsureAndProjectIDAndGoal` rather than starting a second
   `atct watch` process. Feed the loop through a narrow line sink or callback;
   only `formatWatchDecision` lines that pass the project filter enter the
   monitor queue.
4. Before launching the TUI, capture the existing thread IDs and start the SSE
   watcher so events arriving during thread discovery enter the FIFO. Launch
   the remote TUI, discover a new exact-cwd CLI thread, resume it, and attach
   the bridge to that watcher. Set active before submitting a monitor turn to
   prevent a notification race. Submit queued lines through `turn/start`,
   never `turn/steer`.
5. Rerun the focused tests, format, and `git diff --check`.

## Task 5: Implement supervisor startup, fallback, stop, and documentation

**Files:** `cmd/atct/codex_monitor.go`, `cmd/atct/main.go`,
`cmd/atct/main_test.go`, `README.md`.

1. Add failing tests for setup failure fallback, exact warning text, preserving
   Codex arguments and exit status, cleanup after TUI exit, and explicit stop.
   Add a process-free test that `exec` is passed to real Codex unchanged.
2. Run:

   ```text
   go test ./cmd/atct -run 'TestCodexMonitor(Supervisor|Fallback|Lifecycle|Stop)' -count=1
   ```

3. Implement `runCodexMonitor`:

   - resolve the real Codex executable and create a managed per-invocation
     socket path;
   - reap dead monitor records before startup;
   - start App Server and wait for readiness;
   - connect/initialize the bridge and capture baseline threads;
   - start the TUI with `--remote` only for interactive arguments;
   - run the bridge and watcher until the TUI exits;
   - on pre-launch setup failure, print the running-normal-Codex warning and
     run the original arguments;
   - on post-launch bridge failure, print the session-active warning and let
     the TUI continue;
   - stop children, remove socket/state, and return the TUI result.

   Add stop handling that matches the current absolute project path and uses
   the registry API. Do not change any existing `codex` shell wrapper because
   none exists in this repository.
4. Document the explicit command and pass-through boundary in `README.md`.
   Do not claim that `/atct:start` automatically attaches a Codex monitor.
5. Rerun the focused tests and format all changed Go files.

## Task 6: Review and full verification

**Files:** all changed files from Tasks 1–5.

1. Review the complete diff against this plan. Check that no agmsg import,
   dependency, shell shim, global Codex config edit, or ordinary watch registry
   reuse was introduced.
2. Run the focused tests again, then the fresh full verification:

   ```text
   go test ./... -count=1 -timeout 600s
   go build ./...
   git diff --check
   ```

3. Exercise the command parser with `go run ./cmd/atct codex monitor -- -h`
   only if the local Codex binary is not needed; do not start a real remote
   session as part of automated verification.
4. Record any environment-limited checks explicitly in the completion report.
