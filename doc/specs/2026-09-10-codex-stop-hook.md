# Codex Stop hook

Codex plugin installs a role-scoped `Stop` hook. A monitor launched with an
explicit commander, subcommander, or executor scope passes `ATCT_BIN`, role,
project, and applicable goal/task IDs to the TUI. The hook calls `atct
stop-check` with exactly that scope.

When ATCT has unfinished work for the scope, `stop-check` returns Codex's
`{"decision":"block","reason":"..."}` response and Codex continues with
that reason as a new prompt. The hook emits nothing after `stop_hook_active`
is true, so it cannot continue the same stop event indefinitely. It also blocks
when its monitored scope or `atct` executable is unavailable.

Legacy/unscoped Codex sessions receive no ATCT environment and the hook exits
successfully without an ATCT action. The hook does not report `handoff yielded`:
the executor is still continuing. Claude's report-only hook remains unchanged.
