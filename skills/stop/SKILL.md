---
name: stop
description: Use when the human asks to stop the ATCT answer Monitor for a Claude Code session or the token-bound Codex monitor started with atct codex monitor.
---

# Stop

Choose the branch for the harness that owns the session.

The harness Stop hook is separate from this monitor-stop procedure. Claude and
Codex both send the harness `session_id` to the shared server-resolved check;
when the session still owns work, it blocks the turn. It does not stop a
monitor or daemon.

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

Only after `atct codex monitor stop` returns status 0 may the user relaunch a
new token-bound process:

```bash
atct codex monitor -- <codex args>
```

The wrapper creates a monitor token and follows the assignment derived by the
server after session identification and claim or handoff receipt. `--role`,
`--project`, `--goal`, `--task`, and `--scope` are not monitor options.
Restarting does not retrofit a running Codex process.

There is no `start`, `restart`, or `exit` monitor subcommand. A safe restart is
`atct codex monitor stop`, check that its status is 0, then launch with
the generic command above; after a nonzero status
or reported failure, do not relaunch. A literal Codex argument `stop` goes
after `--`, for example `atct codex monitor -- stop`.

To stop the daemon separately, use `atct daemon stop`.

`Monitor` and `TaskStop` are Claude Code features; this skill only uses them in
the Claude Code branch. The MCP response attachment is the shared foundation
for both harnesses.
