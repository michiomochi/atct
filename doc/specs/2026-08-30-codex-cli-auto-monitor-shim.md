# Goal 209: transparent Codex CLI monitor shim

Date: 2026-08-30

## Outcome

After one explicit installation, an interactive `codex` launch in an
ATCT-registered project starts the existing ATCT Codex monitor before the TUI
starts. The TUI is launched through the local Codex App Server with `--remote`,
so entering `$atct:start` in that session receives the same project-scoped
ATCT delivery as the existing monitor path.

The shim is transparent outside that case:

- an unregistered current directory invokes the real Codex unchanged;
- known non-interactive Codex commands, especially `codex exec`, invoke the
  real Codex unchanged even in a registered project;
- monitor setup failures fall back to the real Codex with the original
  arguments and exit status;
- an already-running normal TUI is never attached or retrofitted.

The automatic path is commander/project scope only. Subcommander and executor
sessions continue to be launched by orchestration with their explicit
`--role` plus `--goal`/`--task` selectors. The shim never infers or invents a
worker scope.

## Non-goals and protected boundaries

- Do not implement Codex App support.
- Do not edit `.codex/hooks.json`, Codex configuration, or ATCT global
  settings.
- Do not enable monitoring per project or auto-register a project.
- Do not alter the existing goal 206 bridge's task/goal filtering, worker
  handoff order, or fail-closed explicit role contract.
- Do not start a second watcher or a second App Server implementation. Reuse
  the supervisor, exact-cwd thread discovery, FIFO queue, idle/completion
  handling, and cleanup already owned by goal 206.

## User-facing installation

The only opt-in operation is an explicit command:

```text
atct codex shim install
```

The installer writes an executable POSIX shell shim at
`$HOME/.atct/bin/codex`. It adds one idempotent, marked PATH-prepend block to
the shell startup file selected from `$SHELL` (`~/.zshrc` or `~/.bashrc`), or
to an explicitly supplied profile path for tests and non-standard shells. The
installer is the sole code path allowed to edit a shell profile; no plugin
startup, `$atct:start`, daemon start, or ordinary `codex` invocation edits
shell configuration. If no supported profile is available, installation still
writes the shim and prints the exact PATH line needed to enable it.

Installation is safe to repeat. It must atomically write the shim with mode
`0700` or `0755`, preserve an existing non-ATCT `codex` file instead of
overwriting it, and avoid duplicating its profile block. The generated script
uses `exec` so stdin, stdout, stderr, signals, and the real Codex exit status
are preserved. It embeds a stable ATCT launcher path and a best-effort real
Codex fallback path, so an unavailable ATCT launcher does not make `codex`
unusable.

The installer does not run a monitor, start the daemon, touch Codex state, or
modify the current shell while installing. The agent must not invoke this
installer against the human's actual home or profile as part of development or
tests.

## Shim dispatch

The generated script calls an internal `atct codex shim run -- "$@"` entry
point. The `--` delimiter is mandatory at this boundary: every argument after
it is an opaque Codex argument and is never parsed by ATCT.

The run entry point resolves the real Codex executable while explicitly
skipping an ATCT shim marked by the generated script. This is required both
for the shim itself and for a direct `atct codex monitor` command when the shim
directory is first on `PATH`; neither path may recurse into the shim. If no
real executable can be resolved, the command returns the normal command-not-
found failure without starting an App Server.

Before dispatching, it checks the current directory against the local ATCT
store using the existing canonical project resolution (`Store.ResolveProject`)
and the current directory/worktree rules. A missing store or
`ErrProjectNotFound` means "not registered" and selects direct pass-through.
Other lookup errors also fail open to direct Codex with a diagnostic. The
check neither starts a daemon nor changes the store, and an inactive project
with no goals is still registered.

The shim uses a conservative, shared pass-through classification. It first
scans only documented Codex global options that can precede a subcommand,
consuming each option's documented value(s) without changing the original
argument vector. It then classifies the first remaining token. A
non-interactive command in the existing command set (including `exec`, `e`,
`review`, `app-server`, `login`, `logout`, and management commands), plus
`--help`, `-h`, `--version`, and `-V`, is passed to the real binary. Thus both
`codex --config model=\"gpt-5\" exec ...` and `codex --profile work review ...`
pass through unchanged. Unknown or malformed leading options remain
interactive rather than being guessed at. All argument vectors retain their
original ordering and values on either branch.

This deliberately uses a small, versioned table of Codex's documented global
options rather than parsing or normalizing the opaque command line. The table
is limited to finding the command boundary; it must support the value-taking,
boolean, repeatable, and `--option=value` spellings that Codex accepts before a
subcommand. Tests pin every supported spelling that precedes each
non-interactive command. Adding a newly documented global option is a table and
test update, not a reason to forward unknown options to a monitor.

For a registered interactive launch, the run entry point invokes the existing
supervisor with an internal automatic-commander mode:

```text
role = commander
scope = current canonical project
goal = empty
task = empty
fallback = enabled for setup failures
```

The mode is not exposed as a worker selector and cannot be supplied by a
Codex argument. Explicit `atct codex monitor --role commander` remains the
existing fail-closed role-aware command; explicit subcommander and executor
launches retain their required selectors and are still the only supported
worker entry points.

## Monitor lifecycle

The automatic commander path reuses goal 206's existing sequence:

1. resolve the canonical project and scope before child processes start;
2. reap stale monitor records and start one per-invocation App Server socket;
3. initialize the App Server and capture the pre-launch thread IDs for the
   exact cwd and CLI source;
4. start `codex --remote unix://...` before the TUI can accept user input;
5. discover one new exact-cwd CLI thread, resume it, and attach the bridge;
6. stream only project-actionable ATCT lines into the existing FIFO;
7. submit one `turn/start` text item when the selected thread is idle, using
   both `turn/completed` and idle status notifications as release signals;
8. on thread discovery or bridge failure after launch, disable only delivery
   and leave the TUI active; on TUI exit, clean up the App Server, socket, and
   monitor record.

The queue remains process-local. ATCT's persisted decisions and the next
monitor's snapshot remain authoritative if a supervisor exits. No current TUI
is discovered as the new thread because the baseline ID set and exact cwd are
required.

## Evidence and design choice

The selected design is a global PATH shim plus local registration lookup and
the existing Go supervisor. The agmsg Codex monitor documentation uses the
same important lifecycle boundary: intercept interactive launches before the
TUI, pass non-interactive commands and non-monitor projects through, connect a
remote TUI to an App Server, serialize injected turns, and use idle/watchdog
fallbacks because `turn/completed` is not reliable. See
<https://github.com/fujibee/agmsg/blob/main/docs/codex-monitor-beta.md>.

The alternatives were considered explicitly:

1. A shell function only: avoids filesystem PATH recursion, but does not give
   a durable global command and depends on every shell being configured.
2. A per-project enable/mode flag: makes dispatch easy, but adds the rejected
   project opt-in state and can drift from ATCT's registered-project truth.
3. A global PATH shim with a local registered-project check (selected): one
   explicit installation provides the ordinary `codex` experience, while the
   no-project and non-interactive branches remain transparent. The marked shim
   resolver and explicit automatic-commander mode address recursion and worker
   scope without changing goal 206's bridge.

## Failure behavior

- Shim installation errors do not alter an existing `codex` file or profile
  content.
- Missing ATCT state, an unregistered project, and a lookup failure select the
  real Codex with unchanged arguments.
- Pre-TUI App Server, socket, initialization, registration, or TUI startup
  failures use the existing monitor warning and run the real Codex. The
  automatic path must not return a role-less or project-unfiltered monitor as
  a substitute for the commander scope.
- Once the remote TUI has started, bridge/SSE/discovery failures print the
  existing session-active warning, stop delivery, and leave the TUI's result
  authoritative.
- Cleanup failures are diagnostic only and never replace a successful Codex
  exit status.

## Verification boundary

Focused tests must cover:

- parser preservation for `codex shim install` and `codex shim run -- ...`;
- atomic executable installation, collision protection, idempotent profile
  editing, and no implicit profile/config/hooks changes;
- real-Codex resolution that skips an installed ATCT shim without recursion;
- registered project, nested directory, worktree, unregistered directory,
  missing database, and lookup-error dispatch;
- `codex`, `codex resume`, and argument preservation on the automatic path;
- every non-interactive command, including `codex --config model=\"gpt-5\" exec`
  and `codex --profile work review`, with every supported leading global-option
  spelling, preserving arguments and exit status without App Server or monitor
  startup;
- automatic launches carrying commander/project scope and falling back on
  setup failure, while explicit goal/task role launches remain unchanged;
- reuse of goal 206's exact-cwd discovery, FIFO/idle delivery, missed
  completion guard, bridge failure, and child cleanup tests;
- a direct explicit monitor invocation with the shim on `PATH` resolving the
  real Codex rather than recursing.

Final verification is the focused shim/supervisor tests, the existing goal 206
monitor tests, `go test ./... -count=1 -timeout 600s`, `go build ./...`, and
`git diff --check`. Tests use temporary homes/profiles and never invoke the
installer against the human's real shell configuration.
