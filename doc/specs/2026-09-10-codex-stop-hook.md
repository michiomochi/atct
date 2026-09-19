# Codex Stop hook

Codex plugin installs a `Stop` hook. The hook invokes `atct stop-check
--hook-input` from `PATH`; the CLI resolves the session and scope from the raw
hook input.

When ATCT has unfinished work for the scope, `stop-check` returns Codex's
`{"decision":"block","reason":"..."}` response and Codex continues with
that reason as a new prompt. The hook emits nothing after `stop_hook_active`
is true, so it cannot continue the same stop event indefinitely. It also blocks
when the hook input cannot be resolved. A missing or older CLI returns a
Homebrew install or upgrade instruction.

The monitor passes only `ATCT_MONITOR_TOKEN` for its binding. The hook does not
report `handoff yielded`: the executor is still continuing.
