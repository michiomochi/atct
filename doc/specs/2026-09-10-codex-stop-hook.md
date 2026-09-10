# Codex Stop hook

Codex plugin installs a report-only `Stop` hook. An executor launched through
`atct codex monitor --role executor --task <id>` receives `ATCT_TASK_ID` and
`ATCT_BIN`; each completed Codex turn reports `handoff yielded <task-id>`.

Non-executor monitors receive neither variable, so the hook exits successfully
without an ATCT action. Claude's hook remains unchanged.

The monitor fails before starting the TUI if it cannot resolve its own ATCT
executable for an executor launch; starting an executor without its stop-report
path would silently lose the contract.
