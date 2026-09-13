# Codex Stop Hook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans or superpowers:subagent-driven-development task-by-task.

**Goal:** Report a yielded executor task when Codex stops after a turn.

**Architecture:** Register a Codex-only plugin hook that calls the compatible
`atct` command from `PATH`; the server resolves scope from hook input.

**Tech Stack:** Codex plugin JSON, Go monitor supervisor, Go and Bash tests.

### Task 1: Register and supply the report path

**Files:** `.codex-plugin/plugin.json`, `hooks/codex-hooks.json`,
`cmd/atct/codex_monitor_supervisor.go`

- [x] Add the report-only Stop hook.
- [x] Pass only the monitor binding token to the monitored Codex processes.
- [x] Fail closed with Homebrew guidance when `atct` is unavailable or stale.

### Task 2: Verify the contract

**Files:** `tests/wrapper_test.bash`, `cmd/atct/codex_monitor_lifecycle_test.go`

- [x] Assert the Codex manifest and report-only hook definition.
- [x] Assert executor TUI environment propagation.
- [x] Run the focused Go monitor and wrapper test suites.
