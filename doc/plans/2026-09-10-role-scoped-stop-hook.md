# Role-scoped Codex Stop Hook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent an ATCT-monitored Codex role from stopping while work assigned to that role remains.

**Architecture:** Add a read-only `atct stop-check` CLI command that resolves one monitor role scope from the local store and emits Codex's Stop-hook continuation JSON only when that scope has unfinished work. The monitor exports its resolved scope to the TUI, and the plugin hook delegates the decision to this command.

**Tech Stack:** Go, SQLite-backed `internal/store`, bash/JSON Codex plugin hooks.

## Global Constraints

- A Stop hook must emit JSON only when it blocks; a scoped check error also blocks.
- Do not use project-wide `pending`; it includes work outside the monitor's role.
- Do not emit `handoff yielded` from Codex while the hook continues the turn.
- A hook invocation with `stop_hook_active: true` is a silent no-op.

---

### Task 1: Implement role-scoped stop-check

**Files:**

- Create: `cmd/atct/stop_check.go`
- Create: `cmd/atct/stop_check_test.go`
- Modify: `cmd/atct/main.go: cliConfig, usage, parseArgs, command dispatch`

**Interfaces:**

- Consumes: `watchScope{Role, ProjectID, GoalID, TaskID}` encoded as `--role`, `--project`, `--goal`, and `--task`.
- Produces: `runStopCheck(cliConfig, atctDir) error`, whose stdout is either empty or `{"decision":"block","reason":"ATCT work remains: ..."}`.

- [ ] **Step 1: Write the failing role-scope tests**

  Add a temporary-store table test that proves an open received executor task blocks, a completed task does not, an open subcommander handoff for its goal blocks, and unrelated goal/project handoffs do not.

  ```go
  func TestStopCheckTextBlocksOnlyWorkInItsRoleScope(t *testing.T) {
      tests := []struct {
          name string
          scope stopCheckScope
          setup func(t *testing.T, s *store.Store, project domain.Project)
          wantBlock bool
      }{
          {name: "open executor task", scope: stopCheckScope{Role: "executor", ProjectID: 1, GoalID: 2, TaskID: 3}, wantBlock: true},
          {name: "completed executor task", scope: stopCheckScope{Role: "executor", ProjectID: 1, GoalID: 2, TaskID: 3}, wantBlock: false},
      }
      // Each setup creates only the asserted handoff state.
  }
  ```

- [ ] **Step 2: Run the new tests and verify RED**

  Run: `go test ./cmd/atct -run TestStopCheck -count=1`

  Expected: FAIL because `stopCheckScope` and `stopCheckText` do not exist.

- [ ] **Step 3: Add the minimal read-only query and CLI parser**

  Implement `stopCheckScope` and `stopCheckText` in `cmd/atct/stop_check.go`. Parse numeric scope IDs and accept only the three role values. Query the existing `store` list methods; do not add migrations or duplicate `pending`.

  ```go
  type stopCheckScope struct {
      Role string
      ProjectID, GoalID, TaskID int64
  }

  func stopCheckText(dir string, scope stopCheckScope) (string, error) {
      // Open work in scope => `{"decision":"block","reason":...}`.
      // No matching work => "".
  }
  ```

  Dispatch the `stop-check` subcommand in `main.go`. For a valid scoped invocation, convert every store/open failure into a block JSON response and exit 0. Invalid role/scope remains a command-line error.

- [ ] **Step 4: Run the role-scope tests and verify GREEN**

  Run: `go test ./cmd/atct -run TestStopCheck -count=1`

  Expected: PASS.

- [ ] **Step 5: Add failure and output-shape coverage**

  Add tests for malformed scope, a missing database, and exact JSON decoding:

  ```go
  var response struct {
      Decision string `json:"decision"`
      Reason   string `json:"reason"`
  }
  if err := json.Unmarshal([]byte(output), &response); err != nil { t.Fatal(err) }
  if response.Decision != "block" || !strings.HasPrefix(response.Reason, "ATCT") { t.Fatal(response) }
  ```

  Verify that a check error emits a continuation response, rather than allowing the monitored role to stop.

- [ ] **Step 6: Run focused tests and commit**

  Run: `go test ./cmd/atct -run TestStopCheck -count=1`

  Expected: PASS.

  ```bash
  git add cmd/atct/main.go cmd/atct/stop_check.go cmd/atct/stop_check_test.go
  git commit -m "feat: block Codex stop for scoped ATCT work"
  ```

### Task 2: Export monitor scope and connect the Codex hook

**Files:**

- Modify: `cmd/atct/codex_monitor_supervisor.go: codexMonitorStopHookEnv`
- Modify: `cmd/atct/codex_monitor_lifecycle_test.go: Stop-hook environment assertions`
- Modify: `hooks/codex-hooks.json: Stop command`
- Modify: `tests/wrapper_test.bash: Codex hook registration assertion`
- Modify: `doc/specs/2026-09-10-codex-stop-hook.md: behavior description`

**Interfaces:**

- Consumes: resolved `watchScope` from `runCodexMonitorWithDeps`.
- Produces: `ATCT_BIN`, `ATCT_ROLE`, `ATCT_PROJECT_ID`, plus applicable `ATCT_GOAL_ID` and `ATCT_TASK_ID` in the TUI environment.

- [ ] **Step 1: Write the failing environment and hook assertions**

  Change the lifecycle test to require all scope fields for an executor and add commander/subcommander cases. Change the wrapper test to require `stop-check`, forbid `handoff yielded`, and require the active-hook guard.

  ```go
  want := []string{
      "ATCT_BIN=/opt/atct", "ATCT_ROLE=executor", "ATCT_PROJECT_ID=7",
      "ATCT_GOAL_ID=16", "ATCT_TASK_ID=920",
  }
  if !slices.Equal(tuiEnv, want) { t.Fatalf("env = %#v, want %#v", tuiEnv, want) }
  ```

- [ ] **Step 2: Run the assertions and verify RED**

  Run: `go test ./cmd/atct -run TestCodexMonitor -count=1 && bash tests/wrapper_test.bash`

  Expected: FAIL because non-executor scope is not exported and the hook still reports `handoff yielded`.

- [ ] **Step 3: Export the resolved scope and invoke stop-check**

  Replace the executor-only environment helper with an explicit-monitor scope encoder. Leave legacy/unscoped Codex sessions with no ATCT variables. Update the hook command to:

  ```sh
  input="$(cat)"
  if printf '%s' "$input" | grep -Eq '"stop_hook_active"[[:space:]]*:[[:space:]]*true'; then exit 0; fi
  "$ATCT_BIN" stop-check --role "$ATCT_ROLE" --project "$ATCT_PROJECT_ID" \
    --goal "${ATCT_GOAL_ID:-}" --task "${ATCT_TASK_ID:-}"
  ```

  Read `stop_hook_active` from the JSON hook input so the command emits no output after Codex has already continued this turn. Preserve the command's JSON stdout unchanged.

- [ ] **Step 4: Run integration checks and verify GREEN**

  Run: `go test ./cmd/atct -run TestCodexMonitor -count=1 && bash tests/wrapper_test.bash`

  Expected: PASS.

- [ ] **Step 5: Update the behavior document and commit**

  Replace the report-only description in `doc/specs/2026-09-10-codex-stop-hook.md` with the role-scoped continuation contract. Do not change the Claude hook description.

  ```bash
  git add cmd/atct/codex_monitor_supervisor.go cmd/atct/codex_monitor_lifecycle_test.go hooks/codex-hooks.json tests/wrapper_test.bash doc/specs/2026-09-10-codex-stop-hook.md
  git commit -m "feat: enforce role-scoped Codex stop hooks"
  ```

### Task 3: Verify the complete plugin contract

**Files:**

- Modify: `doc/specs/2026-09-10-role-scoped-stop-hook.md: Verification` only if test names or commands changed.

**Interfaces:**

- Consumes: the public plugin manifest, hook JSON, and monitor behavior from Tasks 1–2.
- Produces: reproducible verification evidence without new runtime behavior.

- [ ] **Step 1: Run all targeted checks**

  Run:

  ```bash
  go test ./cmd/atct -run 'Test(StopCheck|CodexMonitor)' -count=1
  bash tests/wrapper_test.bash
  ```

  Expected: both commands exit 0.

- [ ] **Step 2: Run the complete package test suite**

  Run: `go test ./cmd/atct -count=1`

  Expected: PASS with no test failures.

- [ ] **Step 3: Inspect the final diff and commit documentation adjustment if needed**

  Run: `git diff --check HEAD~2..HEAD` and `git status --short`.

  Expected: no whitespace errors; no unrelated files staged. If Task 3 changed the spec, commit only that file with `docs: verify role-scoped Stop hook`.
