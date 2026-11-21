# Server-Resolved Stop Hooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Claude Code and Codex Stop hooks use one server-resolved session identity and remove the obsolete yielded-handoff signal.

**Architecture:** Both hooks send their raw JSON input to one CLI mode. The CLI forwards the harness `session_id` to a daemon RPC; the daemon maps the session key to an ATCT agent session and evaluates the role's unfinished work. SessionStart provides the same key for `atct_session_identify` before either hook can evaluate it.

**Tech Stack:** Go RPC daemon and CLI; Bash/JSON plugin hooks; existing shell and Go tests.

## Global Constraints

- The only stop decision body is `{"decision":"block","reason":"..."}` from the shared CLI/RPC path.
- Do not infer an ATCT session from cwd, PID, a latest session, or a role environment variable.
- A malformed, unknown, or unreadable ATCT session is fail-closed once; `stop_hook_active: true` is always silent.
- Do not add database migrations or persistent hook state.
- Remove `handoff yielded` completely; do not leave a legacy command or watch event.

---

### Task 1: Resolve stop work in the daemon by session key

**Files:**

- Modify: `internal/store/store.go`
- Modify: `internal/daemon/handler.go`
- Create: `internal/daemon/stop_check_test.go`

**Interfaces:**

- Consumes: `session.stop_check` RPC params `{ "session_key": "<harness-session-id>" }`.
- Produces: either an empty RPC payload or `{ "decision": "block", "reason": "ATCT work remains: ..." }`; an unknown key is an RPC error whose CLI rendering is fail-closed.

- [x] **Step 1: Write daemon regressions for canonical session ownership.**

  Create a fixture with three identified agent sessions whose `session_key` values are literal harness IDs. Cover commander with an active project goal, subcommander with its received goal handoff, executor with its received unfinished task handoff, an unrelated session with the same project, and an executor with two unfinished received task handoffs. Assert the first three and the inconsistent executor return a block response; assert the unrelated session returns empty.

  Name the production break each case catches: using the latest session instead of the key, inspecting another role's work, or checking only one executor handoff.

- [x] **Step 2: Run the new daemon test and verify RED.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./internal/daemon -run '^TestSessionStopCheck' -count=1 -v
  ```

  Expected: FAIL because `session.stop_check` is not dispatched.

- [x] **Step 3: Add a key lookup and read-only daemon evaluator.**

  Add a narrow exported store method that delegates to the existing `GetAgentSessionIDByKey` query. In `Daemon.dispatch`, add `session.stop_check`; look up the exact session key, call `deriveSessionRole`, and reuse the current open-work predicates for commander and subcommander.

  For the executor path, enumerate current goals, their tasks, and task handoffs. A handoff belongs to this session only when `ReceivedBy` equals the resolved agent session ID and it has `ReceivedAt != nil`, `CompletedReportAt == nil`, and `RecoveredAt == nil`. Return one block response when any such handoff exists. Keep all paths read-only.

- [x] **Step 4: Run the daemon regression and role preservation tests.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./internal/daemon -run '^(TestSessionStopCheck|TestSessionRole)' -count=1 -v
  ```

  Expected: PASS. The role tests prove that extracting the shared predicates does not change `session.role`.

### Task 2: Route both Stop hooks through the daemon RPC

**Files:**

- Modify: `cmd/atct/main.go`
- Modify: `cmd/atct/stop_check.go`
- Modify: `cmd/atct/stop_check_test.go`
- Modify: `hooks/stop`
- Modify: `hooks/codex-hooks.json`
- Modify: `cmd/atct/codex_monitor_supervisor.go`
- Modify: `cmd/atct/codex_monitor_lifecycle_test.go`
- Modify: `tests/wrapper_test.bash`

**Interfaces:**

- Consumes: raw Stop hook JSON on stdin containing `session_id` and `stop_hook_active`.
- Produces: the daemon's exact block JSON, or empty stdout for an active hook/no remaining work.

- [x] **Step 1: Replace scope-based CLI tests with hook-input tests.**

  In `cmd/atct/stop_check_test.go`, add table cases that pass raw JSON to a testable stop-check renderer: active hook yields `""`; missing session ID and an unavailable daemon yield JSON with `decision == "block"` and an `ATCT stop-check failed:` reason; a daemon response is copied without changing its line or reason. Remove tests that construct `stopCheckScope` directly.

  In `tests/wrapper_test.bash`, replace `test_stop_hook_only_reports` and the role-environment Codex test with one fixture input such as `{"session_id":"hook-session-1","stop_hook_active":false}`. Assert both harness hook commands invoke `stop-check --hook-input`, pass the same stdin, and return the fake CLI's identical JSON. Assert active input invokes neither binary.

- [x] **Step 2: Run the focused CLI and wrapper tests and verify RED.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run '^TestStopCheck' -count=1 -v
  bash tests/wrapper_test.bash
  ```

  Expected: FAIL because the CLI requires role/project/goal/task and the Claude hook emits `handoff yielded`.

- [x] **Step 3: Implement `stop-check --hook-input`.**

  Replace `--role`, `--project`, `--goal`, and `--task` with `--hook-input`. Parse stdin with Go's JSON decoder. On `stop_hook_active`, return no output. Otherwise require a non-empty `session_id`, ensure the daemon without changing ATCT work state, and call `session.stop_check` with that exact value. Convert malformed input, daemon failure, unknown key, and RPC failure into the existing fail-closed JSON response.

  Claude と Codex の hook はともに `PATH` 上の `atct stop-check --hook-input` を実行する。CLI が無い、または plugin より古い場合は Homebrew の install / upgrade 指示を返す。どちらの script も role、scope、`stop_hook_active` を自前で解析しない。

- [x] **Step 4: Remove Codex role environment from the hook contract.**

  Change the monitor environment helper to export only `ATCT_MONITOR_TOKEN`. No `ATCT_ROLE`、project、goal、task、binary-path variables remain. Update lifecycle assertions to require the one-element environment slice.

- [x] **Step 5: Run focused tests and verify GREEN.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run '^(TestStopCheck|TestCodexMonitor.*StopHook)' -count=1 -v
  bash tests/wrapper_test.bash
  ```

  Expected: PASS. The shell fixture proves both transports preserve the same JSON result.

### Task 3: Bind harness session IDs before Stop

**Files:**

- Modify: `cmd/atct/main.go`
- Create: `cmd/atct/session_key_test.go`
- Modify: `hooks/session-start`
- Modify: `hooks/codex-hooks.json`
- Modify: `skills/start/SKILL.md`
- Modify: `skills/atct/SKILL.md`

**Interfaces:**

- Consumes: SessionStart raw JSON with `session_id` and the current project directory.
- Produces: an instruction to call `atct_session_identify(session_key=<exact session_id>)`, or no text outside a registered ATCT project.

- [x] **Step 1: Write a failing CLI test for SessionStart context.**

  Add a table test for a `session-key --hook-input` command: a registered project and `{"session_id":"claude-session-1"}` renders one instruction containing that exact value; missing/blank ID and an unregistered project render nothing. The test catches accidentally accepting an agent name, cwd, or generated replacement key.

- [x] **Step 2: Run the test and verify RED.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run '^TestSessionKey' -count=1 -v
  ```

  Expected: FAIL because the subcommand does not exist.

- [x] **Step 3: Add the common SessionStart context command and registrations.**

  Add `session-key --hook-input` to the CLI. It JSON-decodes stdin, resolves the current registered project using the same no-start lookup as `roleProjectRegistered`, and prints exactly:

  ```text
  ATCT session key: <session_id>. Before any other ATCT operation, call atct_session_identify with this exact session_key.
  ```

  Keep `hooks/session-start`'s existing context/daemon behavior, then forward its captured input to this command. Add a Codex `SessionStart` command that pipes its stdin to `atct session-key --hook-input` after the same plugin-version compatibility guard.

- [x] **Step 4: Update the two skills with TDD pressure checks.**

  Before editing, run one fresh-context pressure scenario without the revised text: tell an agent it sees the SessionStart line `ATCT session key: hook-123` and ask which key it will pass to `atct_session_identify`; record its raw answer. Update `skills/start/SKILL.md` and every worker/delegation instruction in `skills/atct/SKILL.md` that says an agent name is suitable to require the exact SessionStart key, with the agent name only when no SessionStart key was emitted.

  Run the same scenario after the edit. It passes only if the agent selects `hook-123` and does not substitute its agent name. Keep this guidance test evidence in the task report; do not add a reference directory.

- [x] **Step 5: Run the command and wrapper tests.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run '^TestSessionKey' -count=1 -v
  bash tests/wrapper_test.bash
  ```

  Expected: PASS.

### Task 4: Delete yielded-handoff behavior and stale documentation

**Files:**

- Modify: `cmd/atct/main.go`
- Modify: `internal/daemon/handler.go`
- Modify: `internal/store/wakeup.go`
- Modify: `cmd/atct/watch.go`
- Modify: `cmd/atct/watch_scope.go`
- Modify: `cmd/atct/watch_action_test.go`
- Modify: affected `*_test.go` files under `cmd/atct/`, `internal/daemon/`, and `internal/httpapi/`
- Modify: `doc/continuous-execution.md`
- Modify: `skills/stop/SKILL.md`

**Interfaces:**

- Removes: `atct handoff yielded <task-id>`, `handoff.yielded`, `EventHandoffYielded`, and `handoff_yielded` watch actions.
- Preserves: handoff completion/review, `detection.monitor_lost`, and monitor stop commands.

- [x] **Step 1: Delete yielded cases from tests first.**

  Remove parser, daemon-event, formatter, scope-filter, selector, and HTTP event fixtures that expect `handoff_yielded`. Add a parser assertion that `atct handoff yielded 1` is rejected and a daemon-dispatch assertion that `handoff.yielded` is unsupported.

- [x] **Step 2: Run the focused deleted-contract tests and verify RED.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct ./internal/daemon ./internal/httpapi -run 'Yielded|HandoffYielded' -count=1 -v
  ```

  Expected: FAIL because the command, RPC, and event remain.

- [x] **Step 3: Remove the command, RPC, event, formatter, and selector paths.**

  Delete the yielded handoff parser branch and `runHandoff` special case, the daemon `handoff.yielded` dispatch case, the store event constant, and all watch-specific event formatting/dedup/filter/action handling. Retain `handoff complete` unchanged. Update continuous execution and stop-skill prose to describe the shared server-resolved check and `detection.monitor_lost` as the recovery signal.

- [x] **Step 4: Run focused removal checks.**

  Run:

  ```sh
  ! rg -n 'handoff yielded|handoff_yielded|handoff\.yielded|EventHandoffYielded' cmd internal hooks skills doc/continuous-execution.md
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct ./internal/daemon ./internal/httpapi -count=1
  ```

  Expected: the search returns no matches and tests pass.

### Task 5: Verify and commit

**Files:**

- Modify: none unless verification identifies a directly related defect.

- [x] **Step 1: Run all verification.**

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./... -count=1
  bash tests/wrapper_test.bash
  git diff --check
  ```

  Expected: all commands pass.

- [ ] **Step 2: Commit the implementation and completed plan.**

  ```sh
  git add cmd/atct internal/daemon internal/store hooks skills tests/wrapper_test.bash doc/continuous-execution.md doc/plans/2026-09-12-server-resolved-stop-hooks.md
  git commit -m "feat: resolve stop hooks by session"
  ```
