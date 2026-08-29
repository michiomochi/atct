---
name: stop
description: Use when the human asks to stop the ATCT answer Monitor for a Claude Code session or the project-scoped Codex monitor started with atct codex monitor.
---

# Stop

Choose the branch for the harness that owns the session.

## Claude Code

This skill stops the answer-delivery Monitor paired with `atct:start`.

1. Find the task id of the `atct watch` Monitor that `atct:start` attached in
   this session.
2. If the task id is unavailable, say so and do not call `TaskStop`. Never guess
   or substitute another task id.
3. If the task id is available, call `TaskStop` with that task id.

## Codex

Stop the explicit Codex monitor with:

```bash
atct codex monitor stop
```

Run it from the exact monitored project directory: the implementation obtains
the project scope from `os.Getwd`. It considers every live monitor record whose
recorded project path exactly matches that directory, not just one selected
session. A record is stopped only when its supervisor PID and recorded start
time still match; a mismatched or failed record remains in the registry and is
reported as a failure. Any failure makes the command exit nonzero, so do not
claim that every supervisor was stopped. It does not stop the ATCT daemon.
If the current terminal is the monitored Codex TUI, use another shell in that
exact directory.

Only after `atct codex monitor stop` returns status 0 may the user relaunch:

```bash
atct codex monitor -- <codex interactive arguments>
```

There is no `start`, `restart`, or `exit` monitor subcommand. A safe restart is
`atct codex monitor stop`, check that its status is 0, then launch with
`atct codex monitor -- <codex interactive arguments>`; after a nonzero status
or reported failure, do not relaunch. A literal Codex argument `stop` goes
after `--`, for example `atct codex monitor -- stop`.

To stop the daemon separately, use `atct daemon stop`.

`Monitor` and `TaskStop` are Claude Code features; this skill only uses them in
the Claude Code branch. The MCP response attachment is the shared foundation
for both harnesses.
