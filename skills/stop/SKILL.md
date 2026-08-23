---
name: stop
description: Stop the ATCT answer Monitor for the current Claude Code session without stopping the daemon. Use when the human asks to stop the Monitor created by atct:start.
---

# Stop

This skill stops the answer-delivery Monitor paired with `atct:start`.

1. Find the task id of the `atct watch` Monitor that `atct:start` attached in
   this session.
2. If the task id is unavailable, say so and do not call `TaskStop`. Never guess
   or substitute another task id.
3. If the task id is available, call `TaskStop` with that task id.

To stop the daemon, use `atct daemon stop`.

`Monitor` and `TaskStop` are Claude Code features and are not available in
Codex. A Codex reader must not try to invoke them. The MCP response attachment
is the shared foundation for all harnesses; this skill only controls the
Claude Code Monitor.
