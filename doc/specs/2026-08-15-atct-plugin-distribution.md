# ATCT Plugin Distribution Design

Date: 2026-08-15
Status: approved
Depends on: `doc/specs/2026-08-15-atct-design.md`, `doc/plans/2026-08-15-atct-core.md` (all 17 tasks complete)

## 1. Problem

ATCT ships two binaries. `atct-mcp` is an MCP stdio server that forwards to a Unix socket, and
`atct daemon` is the process that owns the socket, the SQLite database, the HTTP API, and the
embedded Web UI.

`atct-mcp` is useless on its own. It connects to `~/.atct/atct.sock`, and if no daemon owns that
socket the shim starts, registers eight tools, and fails on the first call. Nothing in the MCP
configuration of either Claude Code or Codex starts a second process.

The goal is an installation where a user adds ATCT once and it works, without being told to keep a
daemon running in another terminal.

## 2. What Crit does, and what we take from it

Crit is the reference the user named. Its actual process model, from
`doc/research/crit-design.md`:

- The distribution unit is one binary, but the runtime is not one process. `cmd/crit/cli_serve.go`
  re-executes the same binary under a hidden `_serve` subcommand, and that child becomes the daemon.
- Running daemons are registered in `~/.crit/sessions/<session-key>.json` with PID, port, working
  directory, branch, and review path. The session key is a SHA-256 prefix of `cwd + NUL + branch`,
  so **each working directory and branch gets its own daemon on its own port**.
- A failed liveness check deletes the stale registry entry.
- The daemon is short-lived: `killDaemonOnApproval` terminates it once a review is approved.

We take two of these and reject one.

**Take: self-start.** The user never runs a daemon command. The tool they already invoke brings the
daemon up.

**Take: a registry with liveness checking and stale repair.** A PID file that is never validated is
worse than no PID file.

**Reject: per-working-directory daemons.** Crit can shard by directory because a review is scoped to
one checkout. ATCT cannot. The inbox is defined in the core design as "unanswered decisions across
every project", and the success criterion is that a human watching only the inbox never misses a
decision. Sharding the daemon shards the inbox and breaks that criterion. **ATCT runs exactly one
daemon per user.**

Rejecting the shard has a cost Crit does not pay: with a single fixed socket, concurrent starts race
each other. Section 4 handles that with a lock.

## 3. Scope

In scope for v1:

- `atct ensure` — an idempotent start-or-reuse command
- `atct stop` — explicit shutdown
- A daemon registry at `~/.atct/daemon.json`
- A lock at `~/.atct/daemon.lock` that makes concurrent `ensure` calls produce one daemon
- `atct-mcp` starting the daemon itself before serving
- A Claude Code plugin: manifest, MCP declaration, and a skill
- GoReleaser configuration for macOS and Linux

Out of scope for v1:

- A Codex plugin adapter. Claude Code goes first; the research could not establish Codex hook
  ordering or process ownership, and the shim-level start does not depend on either.
- Windows. The transport is a Unix domain socket. Supporting Windows requires a named-pipe
  abstraction, which is a larger change than this design.
- ~~A `SessionStart` hook.~~ **Reversed on 2026-08-17.** The original reasoning treated a hook
  purely as a way to pre-start the daemon, which the shim already does. That missed the actual
  use: **a hook is how an agent learns to use ATCT at all.** Registering eight tools does not
  make an agent reach for them, and a skill file only fires when its description happens to
  match. Installing the plugin has to be enough. See section 6a.
- Slash commands.
- An OS service (`launchd` / `systemd`). It would be the most robust option but requires an
  installer, which conflicts with "install once and it works".

## 4. Process model

### One daemon per user

The daemon owns `~/.atct/atct.db`, `~/.atct/atct.sock`, and the HTTP listener. There is one per
user. It is not scoped to a session, a project, or a branch.

The daemon does **not** stop when a session ends. A human keeps the Web UI open to watch the inbox,
and agent sessions come and go underneath it. This is the deliberate opposite of Crit's
`killDaemonOnApproval`. Shutdown is an explicit `atct stop`.

### Registry

`~/.atct/daemon.json` records the running daemon:

| Field | Purpose |
|---|---|
| `pid` | Liveness check |
| `http_addr` | The address the Web UI is served on, so `ensure` can report it |
| `socket_path` | Recorded for diagnostics; the path itself is fixed |
| `version` | Detect a shim talking to a daemon from a different build |
| `started_at` | Diagnostics |

The registry is written by the daemon after it is listening, not by the caller before it starts. A
caller cannot truthfully record readiness it has not observed.

### `atct ensure`

Idempotent. Returns success when a healthy daemon exists, whether or not this call started it.

1. Acquire `~/.atct/daemon.lock`, blocking for at most **5 seconds**.
2. Read `~/.atct/daemon.json`. If absent, go to 5.
3. Check liveness: the PID exists **and** the socket answers. A PID alone is not proof — the number
   may have been recycled by an unrelated process.
4. If healthy, release the lock and return. This is the common path.
5. Not healthy: remove the stale socket file and the stale registry entry.
6. Start `atct daemon` detached, so it outlives the process that called `ensure`.
7. Wait for the socket to answer, for at most **10 seconds**. On timeout, return an error naming the
   log file.
8. Release the lock.

Both timeouts are fixed constants in v1; neither is configurable. The lock timeout is the shorter of
the two because waiting on a lock means another caller is already inside step 7.

`ensure` writes its human-readable result to **stderr**, never stdout. The shim embeds this logic
and stdout belongs to the MCP protocol; giving the command one output rule avoids a second code path
that could regress it.

**The lock is what makes this correct.** Without it, two shims starting at the same moment both see
no registry, both start a daemon, and one of them loses the socket. Crit avoids this by giving each
daemon its own path; a single-socket design has to serialize instead.

The lock is held across the liveness check and the start, not only across the start. Checking
outside the lock and starting inside it reintroduces the race.

### Version mismatch

If the registry records a `version` different from the caller's, `ensure` reports the mismatch and
does **not** restart the daemon. Another session may be mid-flight against it. Resolving it is
`atct stop` followed by a new call, and the error message says so.

Comparison is exact string equality, not semantic version ordering. A daemon built from a different
commit is a different daemon, and v1 has no compatibility policy to interpret an ordering against.

### `atct stop`

Reads the registry, signals the daemon to terminate, waits for exit, and removes the socket and
registry files. Reports plainly when no daemon is running rather than treating it as an error.

## 5. `atct-mcp` changes

`atct-mcp` runs the same start-or-reuse logic before serving. The logic lives in one package used by
both binaries; it is not reimplemented per binary.

Two constraints:

- **Everything the shim logs goes to stderr.** Stdout carries the MCP protocol. A single stray line
  on stdout corrupts the session.
- **A failed start is reported as a usable error**, naming the daemon log and the `atct stop`
  recovery path. "connection refused" tells the user nothing they can act on.

This is what makes the design harness-independent. A plain MCP registration with no plugin, in any
harness that speaks MCP, gets a working ATCT. The plugin becomes packaging rather than a
requirement.

## 6. Claude Code plugin

```
.claude-plugin/plugin.json     identity
.mcp.json                      MCP server declaration (atct-mcp over stdio)
skills/atct/SKILL.md           how an agent uses the eight tools
```

`.mcp.json` invokes `atct-mcp` from `PATH`. The binary arrives through Homebrew or `go install`, not
through the plugin, so the plugin does not reference `${CLAUDE_PLUGIN_ROOT}` for it.

**The skill is the substantive part.** Registering eight tools does not make an agent use them. The
skill states when to declare tasks, when to claim one, when to ask a decision rather than guess, and
that a task cannot be completed while a decision on it is open. Without it the tools are present and
unused.

The plugin carries no hooks in v1.

## 6a. Making the agent actually use ATCT

Tools alone do not change behaviour. An agent with eight registered tools and no instruction
uses none of them. The skill file helps only when its `description` happens to match what the
agent is already thinking about, which is exactly the wrong condition: ATCT matters most when
the agent has *not* thought to ask a human.

The plugin therefore ships a `SessionStart` hook.

```
hooks/hooks.json     SessionStart, matching startup|clear|compact
hooks/session-start  the script that decides whether to speak
```

**The hook is conditional.** It resolves the working directory against the registered projects
and stays silent when there is no match. Repositories that were never registered with
`atct project add` behave exactly as they did before the plugin was installed.

This condition is the design. Injecting instructions into every session would make unrelated
work pay for a goal-and-task ceremony it did not ask for, and an agent told to track
everything tracks nothing usefully. **Registration is the opt-in.**

**The hook does not start the daemon.** The shim already calls `Ensure`, and blocking session
startup on a daemon launch trades a visible delay for nothing.


## 6b. Installing without a package manager

Requiring `brew` or `go install` before the plugin is useful puts a developer-only step in
front of every user. Crit has the same shape — its hook calls a bare `crit`, and its README
tells you to install the binary separately — so there is no prior art to copy here.

**The plugin ships wrapper scripts instead of binaries.** `bin/atct` and `bin/atct-mcp` are
shell scripts that resolve a real binary under `~/.atct/bin/`, downloading it from the
GitHub release on first use, then `exec` into it. Installing the plugin is the whole setup.

Two facts were measured before choosing this (2026-08-17, headless spike with dummy
executables):

| Question | Result |
|---|---|
| Does a plugin's `bin/` land on `PATH` for the Bash tool? | **Yes.** A probe script ran and reported its path inside the installed plugin |
| Does an MCP server declared with a bare command name find it? | **No.** `ENOENT: Executable not found in $PATH` |
| Does `${CLAUDE_PLUGIN_ROOT}/bin/...` start it? | **Yes.** The probe was executed and wrote its log |

So `.mcp.json` must reference the wrapper by `${CLAUDE_PLUGIN_ROOT}`, while `atct` on the
command line works through `PATH`. **The two entry points resolve differently and both have
to be covered.**

Constraints on the wrapper:

- **Verify the download against `checksums.txt`** from the same release. The script fetches
  something and then executes it; skipping verification would make a compromised or truncated
  download run as the user.
- **Pin the version.** The wrapper knows which release it belongs to, so a plugin update and a
  binary update stay in step, and a half-updated install cannot happen.
- **Fail loudly and usefully.** No network, no `curl`, an unsupported platform — each says what
  happened and what to do, on stderr, without corrupting the MCP stdio stream.
- Homebrew and `go install` remain supported for people who prefer them.


## 7. Release

GoReleaser builds `atct` and `atct-mcp` for darwin/arm64, darwin/amd64, linux/arm64, and
linux/amd64. Artifacts are published as GitHub Releases with checksums, and a Homebrew tap formula
installs both binaries.

`go install` remains supported and is the path that needs no release infrastructure.

**Publishing is out of this design's control.** The repository has never been pushed. The
configuration is written and verified locally; creating the release, the tap, and the marketplace
entry are separate authorized actions.

## 8. Failure handling

| Situation | Behavior |
|---|---|
| Healthy daemon exists | Reuse. No new process. |
| No registry | Start one. |
| Registry exists, process dead | Remove stale socket and registry, start one. |
| Registry exists, PID alive, socket silent | Treat as unhealthy; do not kill the process; report it. Killing a PID that may have been recycled is unsafe. |
| Two concurrent `ensure` calls | The lock serializes them. One starts, the other reuses. Exactly one daemon. |
| Start times out | Error naming the log file. Do not retry in a loop. |
| Version mismatch | Report it. Do not restart. Point to `atct stop`. |
| `atct stop` with nothing running | Report plainly. Not an error. |

## 9. Testing

- `ensure` with a healthy daemon starts no second process
- `ensure` with a stale registry repairs it and starts exactly one
- **Concurrent `ensure` calls produce exactly one daemon** — the claim-style test, run under `-race`
  with repetition
- A PID that is alive but not answering is reported, not killed
- Version mismatch reports rather than restarts
- `atct stop` removes the socket and registry; a second `stop` is not an error
- The shim writes nothing to stdout before the MCP handshake
- The plugin manifest and `.mcp.json` parse, and Claude Code loads the plugin from a local
  `--plugin-dir`

## 10. Success criteria

1. A user with no daemon running adds the plugin, starts a session, and the first tool call works.
2. Two concurrent sessions produce one daemon, not two.
3. Killing the daemon and starting a new session recovers without manual cleanup.
4. A plain MCP registration with no plugin works the same way.
5. The Web UI stays available after an agent session ends.
