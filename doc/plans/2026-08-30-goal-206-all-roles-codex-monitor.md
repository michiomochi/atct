# Role-configurable Codex monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Launch newly created Codex monitor processes with commander/project,
subcommander/goal, or executor/task notification scope while preserving the
legacy unscoped monitor and Claude watches.

**Architecture:** Add an explicit `codex monitor` configuration that is parsed
before Codex passthrough arguments and resolved before the supervisor starts an
App Server. Represent project, goal, and task as one watch-scope value consumed
by the SSE URL builder, server filter, client filter/formatter, and Codex
bridge. Use the existing `DetectionEvent` identity fields for future detections;
do not implement review producers or review APIs here.

**Tech Stack:** Go, existing `cmd/atct` CLI and watch/monitor bridge, HTTP SSE,
existing store/domain event types, Go unit tests.

## Global Constraints

- `commander` requires no selector and watches its current project;
  `subcommander` requires `--goal`; `executor` requires `--task`.
- An explicit role/selector error fails closed before any normal Codex or App
  Server process starts. Legacy no-role monitor setup failures remain fail-open.
- Preserve normal `codex`, non-interactive Codex pass-through, legacy no-role
  `atct codex monitor`, and all Claude `atct watch` behavior.
- Add `task_id` SSE filtering and preserve existing task handoff/detection
  routing. Do not create review event producers, review storage, or review APIs.
- Start through `herdr pane run <pane> atct codex monitor ...` only after the
  handoff request succeeds and before the worker process starts. Never retrofit
  a running Codex process.
- Do not add dependencies or change monitor registry stop/reap selection.

---

### Task 1: Parse and validate explicit monitor configuration

**Files:**

- Modify: `cmd/atct/main.go`
- Modify: `cmd/atct/main_test.go`

**Interfaces:**

- Produces: `codexMonitorConfig{Role string, GoalID string, TaskID string, Explicit bool}` in `cliConfig`.
- Consumes: `parseArgs([]string) (cliConfig, error)` and the literal `--`
  passthrough boundary.

- [ ] **Step 1: Write failing parser tests**

```go
func TestParseArgsCodexMonitorRoleScopes(t *testing.T) {
    cfg, err := parseArgs([]string{"codex", "monitor", "--role", "executor", "--task", "845", "--", "-m", "gpt-5"})
    if err != nil || cfg.codexMonitorRole != "executor" || cfg.codexMonitorTaskID != "845" || !cfg.codexMonitorExplicit {
        t.Fatalf("config = %#v, err = %v", cfg, err)
    }
}

func TestParseArgsCodexMonitorRejectsInvalidRoleScope(t *testing.T) {
    for _, args := range [][]string{{"codex", "monitor", "--role", "executor"}, {"codex", "monitor", "--role", "commander", "--goal", "206"}} {
        if _, err := parseArgs(args); err == nil { t.Fatalf("parseArgs(%q) succeeded", args) }
    }
}
```

- [ ] **Step 2: Run the parser tests to verify failure**

Run: `go test ./cmd/atct -run 'TestParseArgsCodexMonitor(RoleScopes|RejectsInvalidRoleScope)' -count=1`

Expected: FAIL because monitor flags are currently passed through as raw Codex arguments.

- [ ] **Step 3: Implement the parser and structural validation**

Parse `--role`, `--goal`, and `--task` only before `--`; preserve all later
arguments byte-for-byte in `codexArgs`. Require exactly the selector matrix in
the global constraints, reject duplicate/unknown monitor flags, and leave no
role configuration on the legacy form.

- [ ] **Step 4: Run parser tests and format**

Run: `gofmt -w cmd/atct/main.go cmd/atct/main_test.go && go test ./cmd/atct -run 'TestParseArgsCodexMonitor' -count=1`

Expected: PASS, including existing raw-argument and pass-through tests.

### Task 2: Resolve explicit scopes before process launch

**Files:**

- Modify: `cmd/atct/codex_monitor_supervisor.go`
- Modify: `cmd/atct/codex_monitor_lifecycle_test.go`
- Modify: `cmd/atct/codex_monitor_test.go`

**Interfaces:**

- Consumes: parsed `codexMonitorConfig` and project/goal/task lookup seams.
- Produces: `watchScope{ProjectID, GoalID, TaskID, Role}` supplied to
  `runCodexMonitorWatch` before `startProcess(codexMonitorAppServer, ...)`.

- [ ] **Step 1: Write failing supervisor tests**

```go
func TestCodexMonitorExplicitExecutorRejectsUnknownTaskBeforeLaunch(t *testing.T) {
    deps := codexMonitorDeps{startProcess: func(codexMonitorProcessKind, string, []string) (codexMonitorProcess, error) {
        t.Fatal("process must not start for an unresolved explicit task"); return nil, nil
    }}
    _, err := runCodexMonitorWithDeps(cliConfig{codexMonitorAction: "monitor", codexMonitorExplicit: true, codexMonitorRole: "executor", codexMonitorTaskID: "999"}, t.TempDir(), deps)
    if err == nil { t.Fatal("expected explicit task resolution failure") }
}
```

- [ ] **Step 2: Run focused supervisor tests to verify failure**

Run: `go test ./cmd/atct -run 'TestCodexMonitorExplicit.*BeforeLaunch' -count=1`

Expected: FAIL because the current supervisor derives only a cwd project and
falls back to normal Codex on setup errors.

- [ ] **Step 3: Implement canonical resolution and failure split**

Add injected lookup functions for project, goal, and task identity. Resolve an
explicit role to a canonical scope before reap, socket creation, App Server, or
normal-Codex fallback. Return a configuration error for a missing/mismatched
selector. Keep existing `codexMonitorFallback` for the legacy monitor and for
post-validation operational failures.

- [ ] **Step 4: Run focused tests and format**

Run: `gofmt -w cmd/atct/codex_monitor_supervisor.go cmd/atct/codex_monitor_lifecycle_test.go cmd/atct/codex_monitor_test.go && go test ./cmd/atct -run 'TestCodexMonitor(Explicit|Fallback|Lifecycle)' -count=1`

Expected: PASS; explicit failures create no child process, while legacy
fallback tests still pass.

### Task 3: Add task identity to HTTP SSE filtering

**Files:**

- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/server_test.go`

**Interfaces:**

- Consumes: `task_id` query value and `store.DetectionEvent` / handoff event
  payload task identity.
- Produces: `eventFilter{canonicalTaskID int64}` and `eventMatchesTaskID` used
  by `eventPasses`.

- [ ] **Step 1: Write failing SSE tests**

```go
func TestSSEFiltersDetectionEventsByTaskID(t *testing.T) {
    stream, reader := openSSEStream(t, streamCtx, srv.Client(), srv.URL+"/api/events?task_id="+idText(f.task.ID))
    publish(store.EventDetectionCompletionReportMissing, store.DetectionEvent{DetectionID: "other", TaskID: otherTask.ID})
    publish(store.EventDetectionCompletionReportMissing, store.DetectionEvent{DetectionID: "target", TaskID: f.task.ID})
    frame := readSSEFrame(t, reader)
    if !strings.Contains(frame.data, `"task_id":`+idText(f.task.ID)) { t.Fatalf("frame = %+v", frame) }
    _ = stream
}
```

- [ ] **Step 2: Run the SSE tests to verify failure**

Run: `go test ./internal/httpapi -run 'TestSSEFilters.*TaskID' -count=1`

Expected: FAIL because `task_id` is not parsed or applied.

- [ ] **Step 3: Implement canonical task filtering**

Resolve `task_id`, require each filtered event to identify the same task, and
apply it in addition to the existing project/goal predicates. Extend event
identity extraction for existing task handoff/detection payloads without
loosening project or goal filters. Keep task-less project/goal streams unchanged.

- [ ] **Step 4: Run focused HTTP tests and format**

Run: `gofmt -w internal/httpapi/server.go internal/httpapi/server_test.go && go test ./internal/httpapi -run 'TestSSE(Filters.*(ProjectID|GoalID|TaskID)|NoGoalID)' -count=1`

Expected: PASS; task scope excludes other tasks and existing scopes retain
their current events.

### Task 4: Thread scope through watch, formatting, and bridge admission

**Files:**

- Modify: `cmd/atct/watch.go`
- Modify: `cmd/atct/watch_scope.go`
- Modify: `cmd/atct/watch_test.go`
- Modify: `cmd/atct/watch_scope_test.go`
- Modify: `cmd/atct/codex_monitor.go`
- Modify: `cmd/atct/codex_monitor_test.go`

**Interfaces:**

- Consumes: `watchScope` with canonical project/goal/task identifiers.
- Produces: task-aware SSE URL, `watchScopeFilter.delivers`, formatted action
  lines, and bridge-admissible notifications.

- [ ] **Step 1: Write failing task-scope watch and bridge tests**

```go
func TestWatchScopeFilterTaskDeliversOnlyMatchingTask(t *testing.T) {
    filter := newWatchTaskScopeFilter("845")
    if !filter.delivers("handoff_reported", watchDecision{TaskID: "845"}) { t.Fatal("matching task was dropped") }
    if filter.delivers("handoff_reported", watchDecision{TaskID: "846"}) { t.Fatal("other task was delivered") }
}

func TestCodexMonitorActionLineAcceptsTaskHandoff(t *testing.T) {
    if !isCodexMonitorActionLine("atct task handoff review received: task 845") { t.Fatal("task action rejected") }
}
```

- [ ] **Step 2: Run focused watch/bridge tests to verify failure**

Run: `go test ./cmd/atct -run 'Test(WatchScopeFilterTask|WatchEventsURL.*Task|CodexMonitorActionLine.*Task)' -count=1`

Expected: FAIL because the scope has only `goalID`, URLs omit `task_id`, and
the action whitelist has no task transition form.

- [ ] **Step 3: Implement the shared scope path**

Replace goal-string-only internal parameters with a scope value. Add `task_id`
to `watchEventsURL`, task filtering before formatting/deduplication, and stable
task handoff/review line formats. Admit exactly those formatted task lines in
the Codex bridge. Preserve the existing project filter whitelist and the legacy
goal behavior; do not forward raw review API payloads or watcher diagnostics.

- [ ] **Step 4: Run focused tests and format**

Run: `gofmt -w cmd/atct/watch.go cmd/atct/watch_scope.go cmd/atct/watch_test.go cmd/atct/watch_scope_test.go cmd/atct/codex_monitor.go cmd/atct/codex_monitor_test.go && go test ./cmd/atct -run 'Test(Watch|CodexMonitor)' -count=1`

Expected: PASS; a task monitor sees only its task and the existing monitor queue
continues to receive only formatted, eligible action lines.

### Task 5: Wire role scope to the monitor launch and protect compatibility

**Files:**

- Modify: `cmd/atct/codex_monitor_supervisor.go`
- Modify: `cmd/atct/codex_monitor_lifecycle_test.go`
- Modify: `cmd/atct/main_test.go`

**Interfaces:**

- Consumes: validated `watchScope` from Task 2 and `runCodexMonitorWatch` from
  Task 4.
- Produces: a pre-TUI watcher running under the chosen role scope.

- [ ] **Step 1: Write failing launch-order and compatibility tests**

```go
func TestCodexMonitorStartsScopedWatchBeforeTUI(t *testing.T) {
    var order []string
    deps := codexMonitorDeps{
        runWatch: func(context.Context, *codexMonitorBridge, watchScope) error { order = append(order, "watch"); return nil },
        startProcess: recordProcessStart(&order),
    }
    // Run with explicit executor/task configuration and assert "watch" precedes "tui".
}
```

- [ ] **Step 2: Run the launch tests to verify failure**

Run: `go test ./cmd/atct -run 'TestCodexMonitor(StartsScopedWatchBeforeTUI|Legacy.*)' -count=1`

Expected: FAIL because monitor dependencies currently receive no role/task scope.

- [ ] **Step 3: Wire the resolved scope through the supervisor**

Start the scoped watcher before the remote TUI and retain the existing baseline
thread/new-thread discovery sequence. Ensure a legacy invocation creates the
same project-scope watcher it had before. Do not add an attach path for a
pre-existing Codex thread and do not change Claude's `runWatch` entry points.

- [ ] **Step 4: Run package verification**

Run: `gofmt -w cmd/atct/codex_monitor_supervisor.go cmd/atct/codex_monitor_lifecycle_test.go cmd/atct/main_test.go && go test ./cmd/atct -count=1`

Expected: PASS, including legacy monitor lifecycle, pass-through, and no-retrofit
thread-discovery tests.

### Task 6: Review the cross-layer contract

**Files:** all files changed in Tasks 1–5.

- [ ] **Step 1: Inspect the diff against the scope boundary**

Confirm no review producer, review API, review-state migration, Claude watch
behavior change, dependency, or monitor-registry scope change appears in the
diff.

- [ ] **Step 2: Run targeted full verification**

Run: `go test ./cmd/atct ./internal/httpapi -count=1 && git diff --check`

Expected: PASS. If the executor sandbox cannot run a package because it needs a
local listener, record that limitation and leave the command for the
subcommander's review environment.

- [ ] **Step 3: Verify no running-process retrofit was introduced**

Run: `go test ./cmd/atct -run 'TestCodexMonitor.*(Discover|Baseline|Legacy)' -count=1`

Expected: PASS; discovery selects only a new post-launch CLI thread, and no
existing Codex session is attached.
