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

This stops every live Codex monitor supervisor for the current exact project
path, not just one selected session. It does not stop the ATCT daemon. If the
current terminal is the monitored Codex TUI, run the stop command from another
shell and wait for it to return before launching again:

```bash
atct codex monitor -- <codex interactive arguments>
```

There is no `start`, `restart`, or `exit` monitor subcommand. A safe restart is
`atct codex monitor stop`, wait for it to return, then launch with
`atct codex monitor -- <codex interactive arguments>`. A literal Codex argument
`stop` goes after `--`, for example `atct codex monitor -- stop`.

To stop the daemon separately, use `atct daemon stop`.

`Monitor` and `TaskStop` are Claude Code features; this skill only uses them in
the Claude Code branch. The MCP response attachment is the shared foundation
for both harnesses.
