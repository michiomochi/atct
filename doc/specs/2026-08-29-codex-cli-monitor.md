# Goal 204: explicit Codex CLI monitor path

Date: 2026-08-29

## Outcome

Provide an opt-in command that runs an interactive Codex CLI session through a
local Codex App Server and feeds action-worthy ATCT events into that same
thread. The existing `codex` command is not intercepted, and `codex exec`
continues to use its existing non-interactive path.

The user-facing entry points are:

```text
atct codex monitor [-- <codex interactive arguments>]
atct codex monitor stop
```

The first form is the only form that starts a monitored Codex session. The
second form stops monitor supervisors for the current project. A literal
`stop` intended as a Codex argument is passed after `--`.

The monitor is deliberately explicit. Neither the normal `codex` executable
nor the plugin MCP configuration starts an App Server or changes Codex
arguments. `atct codex monitor` is also a safe pass-through for known
non-interactive/management subcommands, including `exec` and `e`: it invokes
the real Codex command without `--remote` and without starting a monitor.

## Existing seams and constraints

The existing watcher already supplies the required ATCT event semantics:

- `cmd/atct/watch.go:watchSnapshotWithProject` resolves the project from the
  current working directory while fetching `/api/inbox`.
- `cmd/atct/watch.go:watchLoopWithEnsureAndProjectIDAndGoal` reconnects to the
  daemon, re-ensures it, consumes the snapshot, and then consumes SSE.
- `cmd/atct/watch.go:consumeWatchEventsWithStateAndGoal` reads
  `/api/events`, applies scope filtering, and keeps delivery state across
  reconnects.
- `cmd/atct/watch_scope.go:watchScopeFilter` with an empty goal ID is the
  project-level action filter. It excludes task-internal handoffs and claim
  detections, excludes default-applied decisions, and retains approvals,
  rejections, human answers, goal creation, actionable wakeups, and
  goal-level detections.
- `cmd/atct/watch.go:formatWatchDecision` is the stable human-readable event
  representation. The Codex bridge receives only lines for which this
  formatter returned `ok`; watcher status, reconnect, keepalive, and daemon
  recovery lines are never sent to the model.
- `internal/daemonctl/watchreg.go` and `internal/daemonctl/stop.go` provide
  process-liveness and signal/wait patterns, but the Codex monitor has a
  separate registry so a Claude `atct watch` and a Codex monitor do not reap
  one another as duplicate watches.
- `github.com/coder/websocket` is already a direct dependency and is used for
  the App Server WebSocket client. No agmsg package, executable, or protocol
  dependency is added.

The repository's current CLI parser is in `cmd/atct/main.go`. The new nested
`codex monitor` command must consume its remaining arguments as raw Codex
arguments; the generic ATCT flag parser must not reinterpret Codex flags.

## App Server bridge

`atct codex monitor` creates a per-invocation Unix socket below the existing
ATCT home directory and starts:

```text
codex app-server --listen unix://<managed-socket>
codex --remote unix://<managed-socket> <original interactive arguments>
```

The bridge connects to the same socket as a second WebSocket client. The
WebSocket HTTP transport dials the Unix socket while using a loopback WebSocket
URL for the handshake; no TCP listener is opened by ATCT.

The bridge speaks the JSON-RPC App Server protocol directly:

1. initialize the connection and send the `initialized` notification;
2. call `thread/list` with the exact working directory and interactive source
   filter, recording the thread IDs that existed before Codex starts;
3. after the remote Codex process starts, poll `thread/list` until a new CLI
   thread for that directory appears;
4. call `thread/resume` for that thread so this connection receives its
   notifications;
5. send an action-worthy ATCT line as a `turn/start` request whose input is one
   text `UserInput` item.

Thread discovery is scoped by exact absolute `cwd` and a pre-launch ID set; it
does not attach to an unrelated existing thread in the same project. If no
new thread can be identified before the discovery deadline, the monitor is
disabled with a warning and the Codex session remains usable through its
already-started remote TUI. Failures before the TUI starts use the regular
Codex command as the fallback.

The bridge tracks only the selected thread. `turn/started` makes it active.
`turn/completed` and an idle `thread/status/changed` notification make it
available again. An ATCT line received while active is appended to an
in-memory FIFO. Once idle, queued lines are submitted one at a time as new
`turn/start` requests; the bridge never uses `turn/steer` to interrupt a user
turn. A failed submission remains queued and is retried after the next idle
transition, unless the App Server connection has failed.

The queue is process-local by design. If the supervisor exits, the ATCT state
is still authoritative: the next monitor receives unapplied decisions from the
watch snapshot. Existing watch delivery maps continue to provide event and
wakeup deduplication within one monitor invocation.

## Lifecycle, stop, and orphan cleanup

Monitor state is stored under:

```text
~/.atct/codex-monitors/<supervisor-pid>.json
```

The record contains the supervisor PID, App Server PID, managed socket path,
absolute project directory, and start time. It is written atomically before
the remote TUI starts and removed by the supervisor's deferred cleanup.

On every start and explicit stop:

- malformed or dead-supervisor records are removed;
- if a dead supervisor left an App Server alive, only the recorded App Server
  PID associated with its managed socket is terminated and waited for;
- live supervisors are left alone, so multiple explicit Codex sessions do not
  interrupt each other;
- the current supervisor stops its App Server, closes the bridge, removes its
  socket and registry record, and returns the TUI's exit status.

The registry is separate from `~/.atct/watchers`. A Codex monitor is not a
normal `atct watch` registration and cannot cause an existing Claude Monitor
to be stopped. Stop matching is by exact project directory, with a PID/start
record check before signaling. Termination uses a bounded wait and reports a
clear failure without deleting a live record.

## Fail-open behavior

The explicit path must not make Codex unavailable:

- If `codex` cannot be resolved, App Server startup fails, the socket cannot
  become ready, or the initial JSON-RPC handshake fails, print
  `atct codex monitor disabled: <reason>; running normal codex` to stderr and
  invoke the real Codex with the original arguments.
- If a remote TUI has already started and the bridge later loses the App
  Server/SSE connection, print
  `atct codex monitor disabled: <reason>; Codex session remains active` to
  stderr, stop only the watcher, and leave the TUI process running.
- Cleanup errors are reported, but do not replace a successful Codex exit
  status. A setup error is reported separately from a Codex command error.

## Compatibility boundary

No shell shim named `codex` is installed and no environment variable is
required. Existing interactive Codex startup, existing `codex exec`, Codex
MCP configuration, and Claude's `atct watch` path remain unchanged. Only the
new `atct codex monitor` command adds `--remote` and manages an App Server.

## Focused verification

The implementation tests must cover:

- nested CLI parsing and raw argument preservation;
- `exec`/`e` pass-through without App Server startup;
- action-worthy project-scope filtering and suppression of task-level/raw
  watcher status lines;
- App Server JSON-RPC initialization, thread discovery, resume, and
  `turn/start` input shape;
- active-turn FIFO queueing and delivery after completion/idle;
- setup fail-open command/arguments and warning text;
- bridge failure after TUI start leaving the TUI path alive;
- registry cleanup, bounded stop, dead-supervisor orphan reaping, and keeping
  live monitors from being reaped.

The subcommander will run the full Go verification from the goal worktree,
including the packages that open local listeners. Executor verification is
limited to named pure/unit test patterns that do not require the executor
sandbox to bind a listener.

## Protocol reference

The App Server transport and lifecycle used here follow the official Codex App
Server documentation: JSON-RPC over JSONL/WebSocket, `thread/start`/resume
subscription, and `turn/start`/completion events.

- <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>
