# Executor monitor-check scope fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make monitor-check accept an assigned executor when any of its live task monitor scopes is healthy, without allowing assigned sessions with no live monitor through.

**Architecture:** Keep monitor-health storage and the existing SQL query unchanged. Make the daemon monitor check consume the complete server-derived `MonitorAssignment`: project scope for commanders, goal scope for subcommanders, and every task's project/goal scope for executors. The executor path returns as soon as one live scope is found and blocks only after all assigned scopes fail.

**Tech Stack:** Go, existing `internal/daemon` tests, existing `internal/mcpshim` role-tool test harness, SQLite-backed store.

## Global Constraints

- Use `doc/specs/2026-09-19-goal-295-executor-monitor-scope.md` as the behavioral source.
- Preserve the no-scope bootstrap exception and the no-live-monitor denial.
- Match any executor assignment scope; do not select only the first task.
- Do not change monitor-health schema, monitor binding, SQL, adjacent goals, or daemon operations.
- Commit only the goal work in this worktree; do not merge, rebase, push, or publish.

---

### Task 1: Add regression coverage for role-scoped monitor checks

**Files:**
- Modify: `internal/daemon/monitor_check_test.go`
- Modify: `internal/mcpshim/schema_test.go` only if needed to exercise the real `atct_role(expected_role=executor)` path with a received task handoff

**Interfaces:**
- Consumes: `Daemon.monitorCheck`, `Store.MonitorAssignment`, `Store.UpsertMonitorHealth`, and the existing daemon/MCP test fixtures.
- Produces: failing tests proving live commander, subcommander, and executor scopes pass; assigned sessions without live monitors are denied; an executor with task scopes in different goals passes if any one is live; and a received task can return a matching executor role through `atct_role`.

- [ ] **Step 1: Add a small live-health fixture helper**

Use the existing `store.MonitorHealth` fields and `store.MonitorHealthID` to insert a fresh `healthy` row with `LastSeenAt: time.Now().UTC()`, the fixture project ID, the role under test, and the relevant goal/task pointers. Do not bypass `Store.UpsertMonitorHealth` or use a fake monitor-check result.

- [ ] **Step 2: Add the live-role tests**

Create assigned sessions using the existing fixture operations, add a matching live row, and assert `monitorCheckDecision(...).Decision == ""` for commander, subcommander, and executor. Keep the existing no-live commander/subcommander tests and add the executor no-live case with `Decision == "block"`.

- [ ] **Step 3: Add the multi-goal executor test**

Receive two open task handoffs for one executor from different goals. Insert a live executor monitor only for the second task's goal. Assert monitor-check allows the session; then stop or omit both rows and assert it blocks. This fails if implementation chooses `Tasks[0]` only.

- [ ] **Step 4: Add the role-tool regression**

With a received executor task and a matching live monitor, call the real role-tool path with `expected_role: "executor"` (or the closest existing daemon-backed harness boundary) and assert the structured result reports `role: "executor"` and `matches: true`. The test must exercise a received handoff, not an unassigned executor.

- [ ] **Step 5: Run the focused tests and verify RED**

Run:

```bash
go test ./internal/daemon -run 'MonitorCheck|Executor.*Role' -count=1
go test ./internal/mcpshim -run 'RoleTool' -count=1
```

Expected: the new executor live-scope tests fail before the daemon fix because the current check constructs a nil goal scope or does not inspect all task scopes. Fix test setup errors before proceeding.

### Task 2: Fix daemon monitor-check scope derivation

**Files:**
- Modify: `internal/daemon/monitor_check.go`
- Test: `internal/daemon/monitor_check_test.go`

**Interfaces:**
- Consumes: `store.MonitorAssignment.Tasks` containing `{ProjectID, GoalID, TaskID}` for every received open executor handoff.
- Produces: `monitorCheck(context.Context, string) (monitorCheckResponse, error)` that checks the correct live role scope without weakening bootstrap or liveness denial.

- [ ] **Step 1: Replace the lossy role projection**

Resolve the session key, call `d.store.MonitorAssignment`, and keep the existing early return when no project/role scope exists. Use one `store.MonitorLiveScope` for commander and subcommander. For executor, iterate `assignment.Tasks`; for each valid task scope, set `goalID := task.GoalID` and call `HasLiveMonitorForScope` with the task's project, `Role: "executor"`, and `&goalID`. Return an empty response on the first live result. After all task scopes fail, return the existing block response. Propagate store errors unchanged.

- [ ] **Step 2: Run the focused tests and verify GREEN**

Run:

```bash
go test ./internal/daemon -run 'MonitorCheck|Executor.*Role' -count=1
go test ./internal/mcpshim -run 'RoleTool' -count=1
```

Expected: all new and existing monitor-check/role tests pass, including the no-live denial and multi-goal any-scope case.

- [ ] **Step 3: Format the changed Go files**

Run:

```bash
gofmt -w internal/daemon/monitor_check.go internal/daemon/monitor_check_test.go internal/mcpshim/schema_test.go
```

Only include `internal/mcpshim/schema_test.go` when Task 1 changed it.

### Task 3: Verify and commit the goal artifacts

**Files:**
- Modify: `internal/daemon/monitor_check.go`
- Modify: `internal/daemon/monitor_check_test.go`
- Modify: `internal/mcpshim/schema_test.go` only if changed in Task 1
- Add: `doc/specs/2026-09-19-goal-295-executor-monitor-scope.md`
- Add: `doc/plans/2026-09-19-goal-295-executor-monitor-scope.md`

**Interfaces:**
- Consumes: the green focused regression suite.
- Produces: a committed root-cause fix and durable spec/plan records.

- [ ] **Step 1: Run the required package checks**

Run:

```bash
go test ./internal/daemon ./internal/mcpshim -count=1
```

Expected: PASS, including live role scopes, no-live denial, and the received executor role-tool path.

- [ ] **Step 2: Run repository verification**

Run:

```bash
go test ./... -count=1
git diff --check
```

Expected: all Go tests pass and `git diff --check` emits no errors. Do not run daemon operations or touch adjacent goal files.

- [ ] **Step 3: Review the diff and commit explicit paths**

Confirm the diff changes only the monitor-check implementation, its regression tests, and Goal 295 spec/plan. Then run:

```bash
git add internal/daemon/monitor_check.go internal/daemon/monitor_check_test.go doc/specs/2026-09-19-goal-295-executor-monitor-scope.md doc/plans/2026-09-19-goal-295-executor-monitor-scope.md
git add internal/mcpshim/schema_test.go  # only if changed
git commit -m "fix: match executor monitor scopes"
```

After the commit, request the goal handoff review immediately with the commit hash, verification commands, and changed paths.

## Self-review

- Root cause and the any-scope decision are recorded in the spec.
- The plan covers every required live-role, no-live, multi-goal, and received-executor role check.
- The implementation changes only the daemon's lossy projection; monitor health and SQL remain unchanged.
- The executor still blocks when every assigned scope lacks a live monitor.
