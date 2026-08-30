# Codex Plugin Hook Separation

Date: 2026-08-30
Status: proposed

## Problem

The Codex plugin manifest currently registers `hooks/hooks.json`. That file is a
Claude Code hook definition: all three commands use `${CLAUDE_PLUGIN_ROOT}`.
Codex does not set that variable, so its attempt to run the `Stop` command
resolves to `/hooks/stop` and ends with exit 127. The same registration also
puts the Claude-only SessionStart and PreToolUse commands in Codex's hook
surface.

The repository owns this registration. User-level Codex configuration and
hooks, dotfiles, homes/profiles, releases, and publishing are outside this
change.

## Decision

Remove only the `"hooks": "./hooks/hooks.json"` property from
`.codex-plugin/plugin.json`.

`hooks/hooks.json` and all three executable hook scripts remain unchanged.
Claude Code continues to consume its existing hook definition, retaining:

- `SessionStart` for `startup|clear|compact` via `hooks/session-start`;
- `PreToolUse` for `AskUserQuestion` via `hooks/pre-ask`; and
- `Stop` via `hooks/stop`.

With no hook registration in the Codex manifest, Codex has no reason to invoke
any command containing `${CLAUDE_PLUGIN_ROOT}`. Its MCP, skills, metadata, and
version remain registered as they are today.

## Alternatives considered

1. **Remove the hook registration from the Codex manifest (recommended).** It
   is a one-property boundary change that prevents every Claude-only command
   from reaching Codex while leaving Claude's shared hook files intact.
2. Add Codex-specific environment-variable fallback or wrapper logic to the
   hook commands. This would make Codex execute lifecycle behavior designed for
   Claude Code, adds a second runtime contract, and leaves the other two Claude
   hook events incorrectly registered.
3. Split or delete `Stop` from the shared hook definition. This avoids the
   observed exit 127 but either preserves the incorrect SessionStart/PreToolUse
   registration or breaks Claude Code's Stop handoff reporting.

## Implementation and tests

The implementation changes `.codex-plugin/plugin.json` only. It neither edits
`hooks/hooks.json` nor the scripts beneath `hooks/`.

Extend the existing plugin static-contract coverage in
`tests/wrapper_test.bash` to parse `.codex-plugin/plugin.json` and require that
the manifest has no `hooks` property. Keep its existing assertions that
`hooks/hooks.json` contains the exact report-only Claude `Stop` command and
that the SessionStart and PreToolUse sections remain present. Run:

```sh
bash tests/wrapper_test.bash
```

The test boundary is declarative: the repository cannot emulate Codex's plugin
host in this test suite, but it can prove the manifest no longer grants Codex a
path to the Claude hook file and that the Claude hook definition is preserved.

## Acceptance criteria

- `.codex-plugin/plugin.json` does not declare `hooks`.
- `hooks/hooks.json` still declares the existing Claude Code SessionStart,
  PreToolUse, and Stop entries with their existing commands.
- `bash tests/wrapper_test.bash` verifies both boundaries.
- No user configuration, dotfiles, homes/profiles, release state, or publish
  operation is changed.
