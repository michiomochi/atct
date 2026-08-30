# Global `atct` CLI Distribution

## Problem

Plugin wrappers cache verified versioned binaries in `~/.atct/bin`, but ordinary
terminals do not include that private cache on `PATH`. Thus bare `atct` fails
outside a plugin host. Dotfile edits are not a product distribution mechanism.

## Decision

Extend the shared `bin/_resolve` path (used by `bin/atct` and
`bin/atct-mcp`) with `ensure_terminal_launcher`. After resolving the
repository wrapper and before execing the versioned cache, it atomically writes
an executable, marked launcher at `$HOME/.local/bin/atct`. That launcher
execs the canonical repository `bin/atct` with unchanged arguments; it never
points at `atct-<version>`. Cache download, checksum verification, upgrades,
and pruning therefore remain in the existing resolver.

The resolver creates `~/.local/bin` but never changes a shell profile or
`PATH`. It may replace only a regular file carrying the exact immutable
`ATCT_GLOBAL_LAUNCHER_V1` marker. A symlink, directory, unreadable path, or
unmarked regular file is a non-zero, stderr-only collision error and remains
untouched. Success stays silent on stdout.

If `~/.local/bin` is not on `PATH`, installation still succeeds but docs tell
the user to choose whether to add it; no automatic profile mutation occurs.

## Options

1. **Stable `~/.local/bin` launcher (recommended):** works with the measured
   terminal path, survives cache pruning, and avoids profile edits.
2. **Add `~/.atct/bin` to profiles:** requires user-configuration mutation and
   exposes versioned-cache names.
3. **External package manager:** adds another release channel and excludes
   plugin-only users; defer to a separate packaging goal.

## Scope and ownership

Goal 212 owns generic resolver behavior, Bash tests, and README documentation.
Goal 209 owns Codex-specific shim and manifest files; do not edit
`.mcp.json`, `.codex-plugin/`, or its worktree. Goal 209 may invoke the shared
wrapper and receive the generic launcher as a side effect.

## Verification

Focused Bash tests must cover first install, marked refresh, argument forwarding,
foreign-file collision protection, unchanged profile files, and existing cache
behavior. Existing wrapper, cache-prune, and release tests must pass. Manual
acceptance uses an isolated `HOME` with `$HOME/.local/bin` on `PATH`:
`command -v atct` returns the stable launcher and `atct --version` reaches
the fixture-selected binary.

## Approval gate

No product behavior changes are made by this spec. Home-directory launcher
creation waits for the Goal 212 human ATCT Decision.
