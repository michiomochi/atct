# Goal 249 Monitor Liveness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep scoped subcommander/executor monitors alive through transient watch/daemon failures and give idle eligible workers a bounded ten-minute recheck prompt.

**Architecture:** Extend `watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor` with an invocation-local liveness scheduler and recovery classification. `reconcileWatchScope` remains canonical and supplies the last successful scoped snapshot; `consumeWatchEventsWithStateAndScopeAndSinkAndInterval` evaluates the ten-minute tick even on quiet SSE. A watcher posts health to the daemon HTTP server, which validates then writes via Store; the Codex bridge admits liveness lines and queues them without interrupting a turn.

**Tech Stack:** Go, `net/http` SSE, existing ATCT watch/reconciliation API, Codex App Server bridge, Go unit tests.

## Global Constraints

- Use the cadence selected by Decision #697: exactly one eligible liveness prompt per monitor invocation per 10 minutes.
- Limit prompts to goal-scoped subcommander and task-scoped executor monitors; never prompt a project-wide commander monitor.
- Reconcile the existing scoped canonical state before evaluating a prompt; an open human decision is a blocker and suppresses/reset prompts.
- Keep state-change delivery maps independent from liveness scheduling; do not persist or replay prompts.
- Preserve recoverable watch/daemon failures as nonterminal for the Codex TUI and bridge.
- Commander health visibility is the project-scoped `GET /api/monitor-health` read of an observational `monitor_health` row; it contains only lifecycle metadata and its selector, never canonical goal/task content. `monitor_id` includes process start time; current reads require a 75-second `last_seen_at` lease, cleanup marks rows stopped, and writes prune stopped/expired rows after 24 hours.

---

### Task 0: Merge and verify the Goal 251 delivery baseline

**Files:**

- Modify only through a normal merge: repository worktree and Git metadata
- Inspect: `cmd/atct/watch.go`, `cmd/atct/codex_monitor.go`
- Test: `cmd/atct/watch_scope_test.go`, `cmd/atct/codex_monitor_test.go`

**Interfaces:**

- Consumes: `main` commit `6d91281` and Goal 251's `deliveryGeneration`,
  `codexMonitorAction`, and `pruneQueuedApprovalsLocked` behavior.
- Produces: a clean baseline whose HEAD includes `6d91281`; Task 1 must retain
  delivery generations, structured actions, and same-goal stale-approval prune.

- [ ] **Step 1: Merge current main normally**

Run: `git status --short && git show --no-patch --oneline 6d91281 && git merge main`

Expected: normal merge (fast-forward is valid for linear history), no
rebase/reset/amend, with Goal 249's untracked spec/plan preserved.

- [ ] **Step 2: Prove the merge baseline**

Run: `git merge-base --is-ancestor 6d91281 HEAD && git diff --check`

Expected: exit 0.

- [ ] **Step 3: Run Goal 251 preservation tests before executor handoff**

Run: `go test ./cmd/atct -run 'Test(CodexMonitorQueuePrunesQueuedApprovalAfterGoalHandoffReceive|CodexMonitorQueueKeepsActiveApprovalAfterGoalHandoffReceive|ReconcileWatchScopeCodexMonitorPrunesApprovalAfterGoalReceive|WatchPlanReviewDeliveryUsesLifecycleGeneration)$'`

Expected: PASS. A Goal 249 liveness action must not change those results.

### Migration serial contract

- [ ] Task 2 creates only `internal/store/migrations/0030_monitor_health.sql`.
- [ ] Never edit main migrations `0023` through `0029`.
- [ ] Before final integration run migration-parity verification against current
  main. Goal 228 renumbers its unintegrated carrier/stop-report 0030/0031 to
  0031/0032 after Goal 249 lands; Goal 245 chooses a later serial.

### Task 1: Model scoped liveness eligibility and bounded prompt scheduling

**Files:**

- Modify: `cmd/atct/watch.go`
- Modify: `cmd/atct/watch_scope.go`
- Modify: `cmd/atct/codex_monitor.go`
- Test: `cmd/atct/watch_test.go`
- Test: `cmd/atct/watch_scope_test.go`
- Test: `cmd/atct/codex_monitor_test.go`

**Interfaces:**

- Consumes: `watchScope`, `watchReconciliation`, the `consumeWatchEventsWithStateAndScopeAndSinkAndInterval` reconciliation ticker, and `codexMonitorBridge.LineSinkWithContext`.
- Produces: an invocation-local `watchLivenessState` whose `PromptDue(now, scope, state) bool` emits only `atct monitor liveness: recheck goal 249` or `atct monitor liveness: recheck task 812`.

**Executor boundary:** The executor writes tests and implementation, runs only
the named commands, and submits a task handoff review. It does not stage,
commit, create another task, or modify this plan. The subcommander receives
the review, verifies it, stages named paths, and creates the task commit.

- [ ] **Step 1: Write failing scope and scheduler tests**

```go
func TestWatchLivenessPromptsOnlyEligibleScopedMonitor(t *testing.T) {
	state := newWatchLivenessState(time.Unix(0, 0))
	if got := state.PromptDue(time.Unix(600, 0), watchScope{Role: "commander", ProjectID: "1", GoalID: "249"}, watchReconciliation{}); got {
		t.Fatal("commander monitor prompted, want no prompt")
	}
	if got := state.PromptDue(time.Unix(600, 0), watchScope{Role: "subcommander", ProjectID: "1", GoalID: "249"}, watchReconciliation{}); !got {
		t.Fatal("eligible goal monitor did not prompt")
	}
	if got := state.PromptDue(time.Unix(600, 0), watchScope{Role: "executor", ProjectID: "1", GoalID: "249", TaskID: "812"}, watchReconciliation{}); !got { t.Fatal("eligible task monitor did not prompt") }
}

func TestWatchLivenessSuppressesOpenHumanDecision(t *testing.T) {
	state := newWatchLivenessState(time.Unix(0, 0))
	blocked := watchReconciliation{Decisions: []watchDecision{{GoalID: "249", Status: "open"}}}
	if got := state.PromptDue(time.Unix(600, 0), watchScope{Role: "subcommander", ProjectID: "1", GoalID: "249"}, blocked); got {
		t.Fatal("open human decision prompted, want suppression")
	}
}
```

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./cmd/atct -run 'TestWatchLiveness(PromptsOnlyEligibleScopedMonitor|SuppressesOpenHumanDecision)$'`

Expected: FAIL because `watchLivenessState` and `PromptDue` do not exist.

- [ ] **Step 3: Add the minimal invocation-local state and rendering path**

```go
const watchLivenessPromptInterval = 10 * time.Minute

type watchLivenessState struct { lastPromptAt time.Time }

func (s *watchLivenessState) PromptDue(now time.Time, scope watchScope, snapshot watchReconciliation) bool {
	if !watchLivenessEligible(scope) || scopedOpenDecision(scope, snapshot) {
		s.lastPromptAt = now
		return false
	}
	if now.Sub(s.lastPromptAt) < watchLivenessPromptInterval { return false }
	s.lastPromptAt = now
	return true
}
```

Thread this state through the existing reconciliation/timer seam, after a
successful scoped reconciliation. Add a ten-minute ticker to
`consumeWatchEventsWithStateAndScopeAndSinkAndInterval`; on its tick, use the
last successful snapshot and call the sink without reading or modifying
`lastWakeupContent` or any delivery map. Render only the exact selector.
Extend `isCodexMonitorActionLine` with `atct monitor liveness:` and test that
`LineSinkWithContext` queues it while active then starts it after idle.

- [ ] **Step 4: Run focused liveness and scope regression tests**

Run: `go test ./cmd/atct -run 'Test(WatchLiveness|WatchFiltersOtherGoalFromSnapshot|WatchFiltersOtherProjectFromSnapshot|WatchEmitsWakeupAgainAfterStateReturns|CodexMonitorActionLineAdmitsLiveness|CodexScopedLivenessQueuesUntilThreadIsIdle)$'`

Expected: PASS; the task proves cadence, decision-blocking, isolation, and
that wakeup content deduplication did not become prompt deduplication.

- [ ] **Step 5: Submit the executor review request**

```bash
atct task handoff review request <handoff-id> <task-id>
```

The executor report must name changed paths and the exact focused test output.
After accepting the review, the subcommander stages exactly
`cmd/atct/watch.go`, `cmd/atct/watch_scope.go`, `cmd/atct/codex_monitor.go`,
`cmd/atct/watch_test.go`, `cmd/atct/watch_scope_test.go`, and
`cmd/atct/codex_monitor_test.go`, then commits with
`feat: add scoped monitor liveness prompts`.

### Task 2: Report bounded recovery health without disabling a live Codex session

**Files:**

- Modify: `cmd/atct/watch.go`
- Modify: `cmd/atct/codex_monitor_supervisor.go`
- Modify: `cmd/atct/codex_monitor.go`
- Create: `internal/store/migrations/0030_monitor_health.sql`
- Create: `internal/store/monitor_health.go`
- Modify: `internal/httpapi/server.go`
- Create: `internal/httpapi/monitor_health_test.go`
- Test: `cmd/atct/watch_test.go`
- Test: `cmd/atct/codex_monitor_lifecycle_test.go`
- Test: `internal/store/monitor_health_test.go`

**Interfaces:**

- Consumes: `recoverDaemon`, `waitForWatchReconnect`, `watchEnsureMaxFailures`, successful scoped reconciliation, explicit `watchScope{Role, ProjectID, GoalID, TaskID}`, and `runCodexMonitorWithDeps`'s `watchDone` select case.
- Produces: one transition report per `recovering`, `degraded`, and recovered `healthy` state. A watcher POSTs `monitorHealthReport` to `POST /api/monitor-health`; `Server.handleMonitorHealth` validates role/scope/PID/start identity then calls `Store.UpsertMonitorHealth`. For example, `GET /api/monitor-health?project_id=1` returns only current rows.

**Executor boundary:** The executor may write and test only the files named in
this task and submit its review request. It must not stage or commit. The
subcommander accepts/rejects review, stages the named paths, and commits only
after acceptance.

- [ ] **Step 1: Write failing recovery-transition tests**

```go
func TestWatchRecoveryReportsOnceThenHealthy(t *testing.T) {
	// Make the first snapshot fail, then make reconciliation succeed.
	// Assert exactly one recovering report and exactly one healthy recovery report.
}

func TestCodexMonitorKeepsTUIAliveDuringRecoverableWatchFailure(t *testing.T) {
	// Make the first snapshot/SSE request fail then recover inside the watch loop.
	// Assert the remote TUI remains running and watchDone never calls disableMonitor.
}

func TestCodexMonitorDisablesOnlyForTerminalWatchSinkError(t *testing.T) {
	// Return watchSinkError from a disabled bridge and assert the supervisor disables monitoring.
}
```

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./cmd/atct -run 'Test(WatchRecoveryReportsOnceThenHealthy|CodexMonitorKeepsTUIAliveDuringRecoverableWatchFailure)$'`

Expected: FAIL because recoverable watch errors currently reach watchDone and health POST does not exist.

- [ ] **Step 3: Implement a transition-only recovery reporter**

Add a `watchRecoveryState` owned by one watch loop. On the first failed
snapshot/reconciliation/SSE read report `recovering`; at the existing five
ensure failures report `degraded`; after a later reconciliation report
`healthy`. Pass `ensureWatchDaemon(dir)` from `runCodexMonitorWithDeps` through
`runCodexMonitorWatchScoped` into the existing loop. These recoverable errors
must wait with `waitForWatchReconnect`, then snapshot/reconcile/re-subscribe
without returning to `watchDone`; only cancellation and disabled-bridge
`watchSinkError` are terminal watcher returns.

Add migration 0030, `POST /api/monitor-health`, and
`Server.handleMonitorHealth`. The watcher creates its monitor ID from absolute
cwd, role/selector, PID, and process start time; it posts after each successful
reconciliation and transition, while deferred normal cleanup posts stopped.
The handler uses `eventScopeIDs` to validate project/goal/task membership and
rejects malformed, commander, unscoped, mismatched, or forged requests. Health
POST failure is a local nonterminal diagnostic retried after later success.

```go
type MonitorHealth struct {
	MonitorID, Role, State, Reason string
	ProjectID                      int64
	GoalID, TaskID                 *int64
	TransitionedAt, LastSeenAt     time.Time
	StoppedAt                      *time.Time
}

func (s *Store) UpsertMonitorHealth(ctx context.Context, health MonitorHealth) error
func (s *Store) ListMonitorHealth(ctx context.Context, projectID int64) ([]MonitorHealth, error)
```

Derive `MonitorID` from the absolute working directory, scope role/selector,
PID, and OS process-start time. Refresh `LastSeenAt` after every successful
reconciliation; list only rows less than 75 seconds old and not stopped. On
normal cleanup mark exactly this monitor ID stopped. During each write prune
stopped or expired rows older than 24 hours. Add tests for PID reuse,
ungraceful expiry, normal cleanup exclusion, pruning, and no history response.

- [ ] **Step 4: Run recovery, bridge, and lifecycle tests**

Run: `go test ./cmd/atct -run 'Test(WatchEnsuresDaemonAfterConnectionFailure|WatchReportsReconnectWhileUnavailable|WatchRecoveryReportsOnceThenHealthy|CodexMonitorKeepsTUIAliveDuringRecoverableWatchFailure|CodexMonitorDisablesOnlyForTerminalWatchSinkError|CodexMonitorWatchDiagnosticsAreDiscardedByHealthGate)$'`

Expected: PASS; all transient errors retain the TUI and each state transition
is bounded.

- [ ] **Step 4a: Run Store and HTTP health access regressions**

Run: `go test ./internal/store -run 'TestMonitorHealth(RejectsForgedIdentity|ExpiresUngracefulProcess|HidesStoppedRow|PrunesExpiredRows)$' && go test ./internal/httpapi -run 'TestMonitorHealth(GetRequiresProject|GetFiltersOtherProject|GetFiltersGoalAndTaskSelector|GetOmitsExpiredAndStoppedRows|PostRejectsCommanderAndMismatchedScope)$'`

Expected: PASS. These tests prove a commander sees only project-scoped current
health, and neither stale/stopped predecessors nor foreign/mismatched rows
appear as recovery.

- [ ] **Step 5: Submit the executor review request**

```bash
atct task handoff review request <handoff-id> <task-id>
```

After acceptance, the subcommander stages exactly the paths listed in this
task and commits with `feat: report monitor recovery health`.

### Task 3: Cover delivery boundaries and document the operational contract

**Files:**

- Modify: `README.md`
- Modify: `skills/start/SKILL.md`
- Modify: `cmd/atct/watch_test.go`
- Modify: `cmd/atct/codex_monitor_test.go`

**Interfaces:**

- Consumes: the liveness prompt and recovery report interfaces from Tasks 1–2.
- Produces: documented 10-minute prompt, human-decision suppression, scope isolation, and nonterminal recovery behavior.

**Executor boundary:** The executor writes documentation/tests and submits its
review request only. The subcommander performs the review, explicit staging,
and commit after acceptance.

- [ ] **Step 1: Write failing integration-style watcher tests**

```go
func TestCodexScopedLivenessQueuesUntilThreadIsIdle(t *testing.T) {
	// Queue a liveness line while the bridge is active.
	// Assert no turn interrupts the active one and the exact line starts after idle.
}

func TestWatchLivenessDoesNotReuseWakeupDeduplication(t *testing.T) {
	// Reconcile the same wakeup state across two liveness intervals.
	// Assert one wakeup rendering and two cadence-separated liveness prompts.
}
```

- [ ] **Step 1a: Run skill-guidance RED pressure scenarios before editing `skills/start/SKILL.md`**

Use `ai-config` to confirm the project-local skill layer, then use
`superpowers:writing-skills` and its TDD prerequisite. In fresh contexts
without the new text, run two pressure scenarios: (a) a subcommander is urged
to start an executor before plan acceptance; (b) an executor is told that an
urgent liveness prompt authorizes commit or crossing an open human decision.
Record the actual baseline response and rationalization in the executor review
report. If a baseline complies, do not add a prohibition for that scenario.

- [ ] **Step 1b: Write the minimal guidance and run GREEN pressure scenarios**

After RED evidence, amend only `skills/start/SKILL.md` with the observable
recipe: liveness is a recheck prompt, not authority; plan acceptance precedes
task handoff; executors submit review rather than commit. Repeat the same two
scenarios with guidance and require compliant role/action boundaries. Include
both transcripts in review. This is separate from wrapper-text regression.

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./cmd/atct -run 'Test(CodexScopedLivenessQueuesUntilThreadIsIdle|WatchLivenessDoesNotReuseWakeupDeduplication)$'`

Expected: FAIL until the liveness path is independent and bridge-compatible.

- [ ] **Step 3: Complete tests and update the user-facing contract**

Document the 10-minute cadence, project-scope exclusion, decision-blocker
suppression, recovery/reconnect behavior, and the fact that prompts are not
durable work items.  Do not tell workers to treat a prompt as permission to
override an open decision or scope boundary.

- [ ] **Step 4: Run the focused command package test suite**

Run: `go test ./cmd/atct`

Expected: PASS.

- [ ] **Step 4a: Run wrapper-text regression separately from skill validation**

Run: `go test ./cmd/atct -run 'Test(CodexMonitorActionLineAdmitsLiveness|CodexMonitorWatchDiagnosticsAreDiscardedByHealthGate)$'`

Expected: PASS. This verifies bridge/output behavior, not pressure-scenario
compliance.

- [ ] **Step 5: Run repository verification and submit the executor review request**

Run: `go test ./... && git diff --check && git diff --no-index --check /dev/null README.md && git diff --no-index --check /dev/null skills/start/SKILL.md`

Expected: `go test ./...` and `git diff --check` exit 0. Each no-index check
exits 1 because the documented file is new/different from `/dev/null`; that is
the expected exit status, and its output must be empty (any output is a
whitespace error).

```bash
atct task handoff review request <handoff-id> <task-id>
```

After accepting that review, the subcommander stages exactly `README.md`,
`skills/start/SKILL.md`, `cmd/atct/watch_test.go`, and
`cmd/atct/codex_monitor_test.go`, then commits with
`docs: describe monitor liveness recovery`.

## Plan self-review

- Spec coverage: Task 1 implements bounded, scope-isolated, decision-aware
  prompting; Task 2 implements transient recovery and commander-visible state;
  Task 3 verifies bridge delivery and documents the contract.
- Placeholder scan: no deferred file paths or implementation choices remain;
  the angle-bracket forms in executor commands identify runtime handoff/task
  values, not deferred implementation work.
- Type consistency: Task 1's `watchLivenessState` is invocation-local and
  receives only `watchScope` and `watchReconciliation`; Task 2's recovery
  reporter carries only metadata and never consumes canonical payloads.

## Plan delta: canonical agent-action selector (requires separate approval)

This delta is deliberately not part of Tasks 1205 or 1206. Do not create an
executor handoff or edit production code for it until its canonical plan
handoff is accepted. It may begin only after Task 1206 has been reviewed and
integrated, so the recovery/health review remains isolated.

### Delta Task A: move notification membership into one canonical selector

**Files:**

- Create: `cmd/atct/watch_action.go`
- Create: `cmd/atct/watch_action_test.go`
- Modify: `cmd/atct/main.go:cliConfig, watch flag parsing, watch dispatch`
- Modify: `cmd/atct/watch.go:runWatch, emitWatchDecisionWithStateAndSinks, writeWatchLineWithActionSink`
- Modify: `cmd/atct/codex_monitor.go:LineSinkWithContext,ActionSinkWithContext,isCodexMonitorActionLine`
- Modify: `cmd/atct/codex_monitor_supervisor.go:codexMonitorWatchOutput.Write`
- Modify: `skills/start/SKILL.md:Claude Code Monitor command only`
- Test: `cmd/atct/watch_action_test.go`
- Test: `cmd/atct/codex_monitor_test.go`
- Test: `cmd/atct/watch_test.go`
- Test: `tests/wrapper_test.bash`

**Interfaces:**

```go
type watchAgentAction struct {
	line, eventName, goalID string
}

func selectWatchAgentAction(line, eventName string, decision watchDecision) (watchAgentAction, bool)

type watchRawLineSink func(string) error
type watchAgentActionSink func(watchAgentAction) error

func watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(
	ctx context.Context, out io.Writer, client *http.Client, retry time.Duration,
	snapshot watchSnapshotFunc, ensure watchEnsureFunc, projectID func() string,
	scope watchScope, raw watchRawLineSink, action watchAgentActionSink,
	reporters ...watchHealthSink,
) error
```

`selectWatchAgentAction` is called only after `formatWatchDecision` and its
existing state/delivery-generation deduplication have selected one output.
Neither function mutates delivery maps, queue state, or `watchScopeFilter`.
Plain `runWatch` and `codexMonitorWatchOutput` keep a `watchRawLineSink` only
for stdout diagnostics. `ActionSinkWithContext(ctx) watchAgentActionSink`
accepts typed selector output and converts it once to `codexMonitorAction`.
`LineSinkWithContext` remains `watchRawLineSink` and is never a selector or an
action entrypoint.

Decision 705 selects a dedicated entrypoint, not a guessed stream contract:
plain `atct watch` preserves human diagnostic stdout; `atct watch --monitor`
emits only selected `watchAgentAction` lines for the attached Claude Monitor.
The CLI flag is valid with exactly one existing selector (`-goal` or
`-project`). `runWatch` constructs a `monitorActionWriter` adapter implementing
`watchAgentActionSink`; it receives the typed selector result exactly once.
Reconnect/ensure/keepalive/registration/unknown diagnostics reach neither this
writer nor Claude's Monitor and remain observable through plain watch output and
the existing monitor-health API.

- [ ] **Step 1: Add failing membership parity tests**

```go
func TestWatchAgentActionSelectorMembership(t *testing.T) {
	// Freeze one table of exact representative event/line rows. True rows are:
	// decision answered/approved/rejected; goal created; wakeup/liveness; goal
	// detection; task and goal handoff requested/received/completed;
	// handoff reported/yielded; task/goal/plan REVIEW requested/received/rejected;
	// the three admitted task-detection suffixes; wakeup discrepancy/evaluate fail.
	// False rows include decision default-applied/pending/opened/expired/unknown,
	// ordinary plan handoff requested/received/completed,
	// keepalive/reconnect/ensure diagnostics, unknown raw text, and every other
	// task-detection suffix. Do not add lifecycle names absent from this table.
}

func TestClaudeAndCodexAgentActionParity(t *testing.T) {
	// Feed every frozen row's typed canonical notification to runWatch's
	// watchAgentActionSink and the Codex ActionSinkWithContext. Assert identical
	// membership and exact line; never feed raw strings to either action sink.
}

func TestClaudeDiagnosticsStayOnStdoutNotActionSink(t *testing.T) {
	// In --monitor mode, selected action writes once to monitorActionWriter;
	// keepalive/reconnect/unknown raw text writes nowhere in that process. In
	// plain mode, the same diagnostics preserve existing human stdout behavior.
}

func TestWatchMonitorModeRejectsUnselectedLines(t *testing.T) {
	// registration/reconnect/keepalive and ordinary plan handoff lifecycle rows
	// never reach monitorActionWriter, while the fixed true baseline rows do.
}
```

- [ ] **Step 2: Run RED tests**

Run: `go test ./cmd/atct -run 'Test(WatchAgentActionSelectorMembership|ClaudeAndCodexAgentActionParity|ClaudeDiagnosticsStayOnStdoutNotActionSink)$'`

Expected: FAIL because the selector and exhaustive frozen baseline do not
exist, Codex still owns a prefix switch, and diagnostics are not separated from
the normal-watch action adapter.

- [ ] **Step 3: Implement the typed selector and adapters**

Create `watch_action.go` with the typed interface above and one exhaustive
event/representative-line/expected-membership baseline table. Copy only the
current `isCodexMonitorActionLine` true cases into true rows. Ordinary plan
handoff requested/received/completed are explicit false rows; only plan review
requested/received/rejected remain true. Replace the Codex prefix switch with
selector delegation. Keep `LineSinkWithContext(ctx) watchRawLineSink` as raw
diagnostic compatibility only: it cannot classify, enqueue, or convert raw
strings. `ActionSinkWithContext(ctx) watchAgentActionSink` alone receives
selector output and converts it to `codexMonitorAction`. In
`writeWatchLineWithActionSink`, retain the existing diagnostic stdout write,
construct one selector result, and send only that typed result to `runWatch`'s
Claude action sink and Codex `ActionSinkWithContext`. Do not move or alter
`watchDeliveryKey`, `watchDetectionDeliveryKey`, wakeup content comparison,
`deliveryGeneration`, or `pruneQueuedApprovalsLocked`.

- [ ] **Step 3a: Add the Claude Monitor-only entrypoint without altering plain watch**

Add `--monitor` only to the `watch` CLI flags. Thread `monitor bool` into
`runWatch` and its testable constructor. Plain mode keeps `os.Stdout` as the
human writer. Monitor mode routes only a true `watchAgentAction` through a
`monitorActionWriter watchAgentActionSink`; do not invoke the raw sink or print
diagnostics in this mode. `skills/start/SKILL.md` changes only the Claude
Monitor commands to `atct watch --monitor -goal <goal_id>` and `--monitor
-project`. Before editing that skill, use `ai-config`, then
`superpowers:writing-skills` and its TDD prerequisite: run RED pressure
scenarios for (a) an agent choosing plain watch because stdout is convenient,
and (b) an agent treating a reconnect diagnostic as an action; add the minimal
command/boundary text; rerun the same scenarios GREEN and retain raw evidence.

- [ ] **Step 3b: Run boundary and attached-Monitor verification separately**

Run Go writer-boundary tests for plain and `--monitor` modes plus the fixed
Claude/Codex typed parity table. Then, in Claude Code, attach one persistent
Monitor to `atct watch --monitor -goal <goal_id>` and inject one selected
baseline sentinel and one reconnect/keepalive diagnostic sentinel through the
test seam. Record the raw Monitor delivery: the selected sentinel appears once;
the diagnostic does not appear. This attached-Monitor evidence is mandatory and
is not replaced by Go tests. If the attachment cannot run or retained raw
evidence cannot be produced, the executor reports unmet verification; the
subcommander rejects review and records a human decision for measurement. It
does not accept, stage, commit, or substitute Go verification.

- [ ] **Step 4: Run selector, bridge, and preservation regressions**

Run: `go test ./cmd/atct -run 'Test(WatchAgentActionSelectorMembership|ClaudeAndCodexAgentActionParity|ClaudeDiagnosticsStayOnStdoutNotActionSink|CodexMonitorActionLineAdmitsLiveness|CodexScopedLivenessQueuesUntilThreadIsIdle|CodexMonitorQueuePrunesQueuedApprovalAfterGoalHandoffReceive|CodexMonitorQueueKeepsActiveApprovalAfterGoalHandoffReceive|WatchPlanReviewDeliveryUsesLifecycleGeneration)$'`

Expected: PASS. The frozen baseline and dual-adapter parity prove identical
action membership/text; the diagnostic test proves stdout does not become an
action; preservation tests prove no stale-approval or lifecycle-generation
regression.

- [ ] **Step 5: Submit executor review; subcommander commits explicit paths**

The executor submits an ATCT review request without staging or committing. On
acceptance, including attached-Monitor evidence, the subcommander stages only
`cmd/atct/main.go`, `cmd/atct/watch.go`, `cmd/atct/watch_action.go`,
`cmd/atct/watch_action_test.go`, `cmd/atct/watch_test.go`,
`cmd/atct/codex_monitor.go`, `cmd/atct/codex_monitor_test.go`,
`cmd/atct/codex_monitor_supervisor.go`, `skills/start/SKILL.md`, and
`tests/wrapper_test.bash`, then commits `refactor: share monitor action selection`.

### Delta self-review

- Scope: selection is centralized; transport remains adapter-specific.
- Preservation: Task 1205 liveness and Task 1206 recovery/health interfaces
  are consumed but not redesigned; Goal 251 generation/pruning remains inside
  the Codex queue.
- Tests: one table drives both adapters from identical inputs and includes
  accepted and rejected notification classes.
