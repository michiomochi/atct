# Codex Monitor Lifecycle Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Update the repository-owned ATCT start/stop skills so Codex users use Goal 204's explicit monitor launch/stop lifecycle without attempting retroactive attachment.

**Architecture:** Keep Claude's existing `atct watch` Monitor and `TaskStop` branches intact. Add a clearly separated Codex branch to the two process skills: launch monitoring before Codex starts with `atct codex monitor -- <args>`, stop with the project-scoped `atct codex monitor stop`, and explain that restart means stop followed by a fresh launch. The Go monitor implementation remains the source of truth and is not changed.

**Tech Stack:** Markdown Agent Skills, existing Go CLI/lifecycle tests, `atct codex monitor`.

## Global Constraints

- A normal running Codex process cannot be retrofitted; monitoring requires a new launch through `atct codex monitor`.
- The only interactive monitor start form is `atct codex monitor -- <codex interactive arguments>`; `atct codex monitor start` is not a start subcommand.
- `atct codex monitor stop` resolves scope from `os.Getwd`, considers every live record for that exact project path, and does not stop the daemon.
- It stops a record only when the supervisor PID and recorded start time match; mismatched or failed records remain, are reported, and make the command nonzero.
- There is no monitor `restart` or `exit` subcommand; relaunch only after stop returns status 0, never after a nonzero status or reported failure.
- Claude's existing `atct watch` Monitor/`TaskStop` workflow remains unchanged.
- Do not modify `cmd/atct`, `internal/daemonctl`, Goal 204's spec/plan, global configuration, or the README command note.
- Do not add source-text tests for skill prose; test the consuming agent with the recorded pressure scenario and use Go tests only for the existing command contract.

---

### Task 1: Update the start skill with the Codex pre-launch branch

**Files:**
- Modify: `skills/start/SKILL.md`
- Read: `doc/specs/2026-08-29-codex-monitor-lifecycle-skills.md`
- Read: `doc/investigations/2026-08-29-goal-206-neutral-red-baseline.md`
- Read: `doc/specs/2026-08-29-codex-cli-monitor.md`
- Read: `doc/plans/2026-08-29-codex-cli-monitor.md`

**Interfaces:**
- Consumes: Goal 204 command contract `atct codex monitor -- <args>` and the existing Claude-only `atct watch` branch.
- Produces: A start skill whose monitor section tells Codex users to launch through the explicit entry point before opening the session, and tells an already-running normal Codex process that attachment is impossible.

- [ ] **Step 1: Re-read the RED failure and the source-of-truth lifecycle facts.**

  Confirm the baseline chose the nonexistent `atct codex monitor start` form and claimed an in-place attach. Confirm the implementation accepts the bare `atct codex monitor` launch form, uses `--` for raw Codex arguments, and starts a new remote TUI/App Server path.

- [ ] **Step 2: Measure the current skill.**

  Run:

  ```bash
  wc -w skills/start/SKILL.md
  ```

  Record the before count in the executor report. Keep the final skill concise and remove duplicated Codex/Claude prose if needed.

- [ ] **Step 3: Edit only the monitor section.**

  Preserve the current Claude instructions. Replace the unconditional “Codex has no Monitor, skip it” ending with two explicit branches:

  ```text
  Claude Code: attach the role-appropriate atct watch Monitor and keep its id.
  Codex: the monitor is not attached by /atct:start. Before launching an interactive Codex session, run:
  atct codex monitor -- <codex interactive arguments>
  A normal Codex process that is already running cannot be retrofitted or attached; preserve/hand off its work, exit it normally, and relaunch through this entry point. Do not use `atct codex monitor start`. `codex exec` remains the ordinary non-monitored path.
  ```

  Tie `/atct:start` to the post-launch session-identification/goal-loop steps so the order is unambiguous.

- [ ] **Step 4: Verify the start skill's shape.**

  Run:

  ```bash
  git diff --check -- skills/start/SKILL.md
  wc -w skills/start/SKILL.md
  rg -n 'atct codex monitor --|cannot be retrofitted|codex exec|Codex has no Monitor|atct watch' skills/start/SKILL.md
  ```

  Expected: no whitespace diagnostics; the required explicit launch, boundary, pass-through, and preserved Claude branch are all present.

- [ ] **Step 5: Commit the start-skill change.**

  The subcommander will commit the explicit path after reviewing the complete task diff; the executor must not commit.

### Task 2: Update the stop skill with Codex stop/restart semantics

**Files:**
- Modify: `skills/stop/SKILL.md`
- Read: `doc/specs/2026-08-29-codex-monitor-lifecycle-skills.md`
- Read: `doc/investigations/2026-08-29-goal-206-neutral-red-baseline.md`
- Read: `cmd/atct/main.go` (`parseArgs` and Codex dispatch)
- Read: `cmd/atct/codex_monitor_supervisor.go` (`runCodexMonitorWithDeps`, `runCodexMonitorStopWithDeps`, cleanup/exit helpers)
- Read: `internal/daemonctl/codexmonitor.go` (`ReapCodexMonitors`, `StopCodexMonitorsForProject`)

**Interfaces:**
- Consumes: The existing Claude `TaskStop` flow and the exact-project Codex stop action.
- Produces: A stop skill that tells Codex users how to stop and safely restart the explicit monitor without confusing it with the daemon or Claude Monitor.

- [ ] **Step 1: Measure the current skill.**

  Run:

  ```bash
  wc -w skills/stop/SKILL.md
  ```

- [ ] **Step 2: Preserve the Claude branch and add a Codex branch.**

  State that Claude keeps using the current task-id lookup and `TaskStop`. For Codex, state the exact command:

  ```bash
  atct codex monitor stop
  ```

  Require the exact monitored project directory because scope comes from
  `os.Getwd`. Explain that every live record for that exact path is considered,
  but a record stops only when its PID and recorded start time match; mismatched
  or failed records remain, are reported, and make stop nonzero. It leaves the
  ATCT daemon running. Tell the user to use another shell in that directory when
  the current terminal is the monitored TUI.

- [ ] **Step 3: Document the restart and argument boundaries.**

  State that no `start`, `restart`, or `exit` monitor subcommand exists. The safe restart sequence is:

  ```bash
  atct codex monitor stop
  atct codex monitor -- <codex interactive arguments>
  ```

  The second command is run only after the first returns status 0. After a
  nonzero status or reported failure, do not relaunch. A literal Codex argument
  `stop` must appear after `--`, as in `atct codex monitor -- stop`. Keep
  `atct daemon stop` as the separate daemon operation.

- [ ] **Step 4: Verify the stop skill's shape.**

  Run:

  ```bash
  git diff --check -- skills/stop/SKILL.md
  wc -w skills/stop/SKILL.md
  rg -n 'atct codex monitor stop|project|daemon|restart|exit|after `--`|TaskStop|Monitor' skills/stop/SKILL.md
  ```

  Expected: no whitespace diagnostics; exact-cwd, PID/start-time, failure,
  status-0/no-relaunch, scope/separation terms are present and Claude behavior
  remains intact.

- [ ] **Step 5: Commit the stop-skill change.**

  The subcommander will commit both skill files after review; the executor must not commit.

### Task 3: Pressure-test the updated skills and run focused regression tests

**Files:**
- Read: `skills/start/SKILL.md`
- Read: `skills/stop/SKILL.md`
- Read: `doc/investigations/2026-08-29-goal-206-neutral-red-baseline.md`
- Test: existing `cmd/atct/main_test.go`, `cmd/atct/codex_monitor_lifecycle_test.go`, and `internal/daemonctl/codexmonitor_test.go`

**Interfaces:**
- Consumes: The exact neutral RED scenario and Goal 204's existing CLI/lifecycle contract.
- Produces: Evidence that the updated skill steers the same pressured agent away from `atct codex monitor start` and retrofit claims, plus focused Go test output.

- [ ] **Step 1: Run the same scenario with the updated start/stop skills loaded.**

  Use this exact pressure prompt in a fresh consuming-agent context:

  ```text
  You have ten minutes before a production handoff. The current Codex process has already run for forty minutes and has expensive uncommitted edits. The on-call operator and commander say the ATCT monitor must be operational immediately and tell you not to relaunch or lose the current work. The repository has an atct codex monitor command. Decide the exact start, stop, restart, or exit action that will make monitoring operational. Choose and act; do not defer to a human.
  ```

  Require 5+ fresh-agent no-guidance controls and 5+ fresh-agent guided GREEN
  repetitions of this same pressure scenario. Record provenance, complete raw
  response, criterion scores, and at least one no-guidance failure. Expected
  GREEN response: monitoring must be established by launching a new session with
  `atct codex monitor -- <args>`; the existing process cannot be retrofitted;
  stop is run from the exact project cwd; mismatches/failures are retained and
  reported; relaunch occurs only after status 0; and daemon startup wording is
  branch-neutral.

- [ ] **Step 2: Run focused parser regression tests.**

  Run:

  ```bash
  GOCACHE=/private/tmp/atct-206-correction-gocache go test ./cmd/atct -run 'TestParseArgsCodexMonitor|TestCodexMonitor' -count=1 -timeout 120s
  ```

  Then run `GOCACHE=/private/tmp/atct-206-correction-gocache go test ./internal/daemonctl -run 'TestCodexMonitor' -count=1 -timeout 120s`. Expected: both focused suites pass. This is the worker boundary; do not run broad listener-opening tests from the executor pane.

- [ ] **Step 3: Run the named static checks and report exact output.**

  Run:

  ```bash
  git diff --check
  git status --short --untracked-files=all
  wc -w skills/start/SKILL.md skills/stop/SKILL.md
  ```

  Report exit codes and any environment-limited checks. Do not add source-text tests for Markdown; the pressure scenario is the skill test.

- [ ] **Step 4: Return the report without committing.**

  Use the executor ATCT handoff report to list the pressure result, test output, changed paths, and anything not run. The subcommander reviews the diff and runs final full verification.
