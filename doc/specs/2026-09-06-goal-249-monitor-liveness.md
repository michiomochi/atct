# Goal 249: monitor liveness and recovery

## Purpose

Subcommander and executor monitors must remain useful when their watch stream or
the daemon is temporarily unavailable.  A monitor must recover without ending
the interactive session, periodically invite an idle worker to recheck its
own scoped work even if the canonical state is unchanged, and make recovery
visible to the commander.

## Existing behavior and gap

`cmd/atct/watch.go` already retries a failed snapshot, reconciliation, or SSE
connection every five seconds; it calls `ensureWatchDaemon` and reconciles at
startup, reconnect, every live signal, and every 30 seconds.  Its watch-loop
delivery maps deliberately suppress unchanged wakeup content.  The Codex
supervisor treats watcher diagnostics as nonfatal and leaves the TUI alive.

That is safe recovery but not liveness: a healthy, unchanged goal/task scope
never produces a bounded reminder, and recovery diagnostics are discarded by
the Codex monitor rather than being a commander-visible state transition.

## Integration baseline and ownership

This goal begins only after normally merging `main` commit `6d91281` into its
worktree. The integration baseline preserves Goal 251's delivery-generation
keys in `cmd/atct/watch.go`, structured `codexMonitorAction` queueing and
same-goal stale-approval pruning in `cmd/atct/codex_monitor.go`. Goal 249 may
extend those files but must not restore pre-251 string-only queue behavior,
remove delivery generations, or let a liveness action prune an approval.

Goal 249 owns migration `0030_monitor_health.sql`. It does not modify main's
migrations `0023` through `0029`. Goal 228's currently unintegrated carrier /
stop-report migrations numbered 0030 and 0031 are renumbered to 0031 and 0032
after Goal 249 integrates; Goal 245 uses a later serial. Before final
integration, rerun migration parity against the then-current main migration
set and resolve any serial conflict by preserving Goal 249 as 0030.

## Design

### Scope and eligibility

The feature applies only to explicitly scoped subcommander (`GoalID`) and
executor (`TaskID`) monitors.  A project-wide/commander monitor never emits a
liveness prompt.  A task prompt is limited to its task scope; a goal prompt is
limited to its goal scope.  The existing `watchScopeFilter` remains the sole
authority for deciding whether canonical decisions and handoff records belong
to that scope.

The monitor performs its normal scoped reconciliation before every eligibility
check.  It sends a liveness prompt only when all of the following hold:

- the monitor is connected and its most recent reconciliation succeeded;
- ten minutes have elapsed since the previous liveness prompt for this monitor
  invocation; and
- the scoped canonical snapshot has no open decision awaiting a human answer.

An open human decision is a valid blocker, not a stalled worker.  While it is
open, the liveness timer is reset/suppressed; after the decision is no longer
open, the next eligible ten-minute tick may prompt.  Answered, rejected, and
applied decisions retain their current notification behavior and do not by
themselves block a prompt.

The 10-minute cadence is the default selected in Decision #697.  It is
intentionally independent of the five-second reconnect delay and 30-second
reconciliation poll.

Eligibility is a role contract, not an inference from a non-empty selector:

```go
func watchLivenessEligible(scope watchScope) bool {
	if scope.Role == "subcommander" {
		return scope.ProjectID != "" && scope.GoalID != "" && scope.TaskID == ""
	}
	if scope.Role == "executor" {
		return scope.ProjectID != "" && scope.GoalID != "" && scope.TaskID != ""
	}
	return false
}
```

Commander, legacy/unscoped, and malformed scopes never prompt. Open-decision
suppression uses a pure scope-match helper over the latest
`watchReconciliation`, rather than the stateful `watchScopeFilter.delivers`,
so it cannot mutate wakeup deduplication state.

### Delivery semantics

Add a liveness prompt as a separate watch output/event class, for example:

```text
atct monitor liveness: recheck goal 249
atct monitor liveness: recheck task 812
```

It is not a wakeup, a decision, a handoff, or a detection.  It has a per-watch
monotonic schedule and never uses `lastWakeupContent`, `delivered`,
`wakeupDiscrepancyDelivered`, or `detectionDelivered`.  Thus unchanged
canonical state can still yield one prompt per cadence, while state-change
notifications keep their present deduplication rules.

For a Codex monitor, the existing bridge queue preserves the prompt while the
selected thread is active and starts a normal non-interrupting turn when the
thread becomes idle.  For `atct watch`, it is written to that watch's output.
No prompt persists across a monitor process restart; the fresh invocation
reconciles first and starts a new ten-minute interval.

`isCodexMonitorActionLine` explicitly admits `atct monitor liveness:`.
`codexMonitorBridge.LineSinkWithContext` then uses its existing
`Enqueue`/`pumpAfterIdle` path: an active turn is not interrupted, a transient
turn submission stays queued, and a disabled bridge error remains terminal.

### Recovery and health state

The watch loop has three observable states:

| State | Entered when | Exited when | Commander-visible result |
| --- | --- | --- | --- |
| `healthy` | initial reconciliation succeeds, or recovery succeeds | a snapshot, reconciliation, or SSE read fails | no transition line |
| `recovering` | the first recoverable watch/daemon failure occurs | a later reconciliation succeeds, or the monitor ends | one scoped recovery-start report |
| `degraded` | daemon ensure reaches its existing five-failure limit | a later successful reconciliation | one scoped degraded report, then one recovery report |

The first failure is reported once, not at every five-second retry.  A
successful post-failure reconciliation reports recovery once and resets the
failure counter.  These transition reports include the role and exact goal or
task selector, never another goal/task's identifiers or canonical payload.

The monitor records those transitions in a small canonical `monitor_health`
read model and exposes current state from a project-scoped HTTP read. The
Codex watcher uses this reporting path rather than converting lifecycle
diagnostics into worker action turns. The commander view may show only
lifecycle state, role, selector, transition time, and reason; it must not
expose a subcommander's task records or an executor's unrelated goal data. A
temporary watch/daemon failure is recoverable: it must not disable the Codex
bridge or end the TUI. A bridge/App Server failure remains a separate terminal
monitor failure under the existing supervisor behavior.

### Monitor-health identity, freshness, and retention

`monitor_health` is a persistent, upserted current-state record, not event
history. A monitor invocation creates an immutable `monitor_id` from its
absolute working directory, role/selector, PID, and OS process-start time.
The start time prevents a reused PID from updating an earlier process's row.
Each successful 30-second reconciliation refreshes `last_seen_at`; lifecycle
transitions additionally update `state`, `reason`, and `transitioned_at`.

For example, `GET /api/monitor-health?project_id=1` returns only *current* rows:

- rows whose `last_seen_at` is no older than 75 seconds (two reconciliation
  intervals plus one 15-second transport allowance); and
- rows not marked `stopped` by normal monitor cleanup.

Normal watcher/supervisor cleanup marks its own row `stopped` before returning.
An ungraceful exit cannot do that, so readers exclude its expired lease rather
than mistaking it for a healthy monitor. Every health write prunes `stopped` or
expired rows older than 24 hours. The API has no history mode; retaining a row
only for bounded cleanup must never make past health look current.

The GET route is the commander's actual read path: a project monitor queries
only its validated `project_id`, with optional `goal_id` or `task_id` selectors
that must satisfy `eventScopeIDs`. The server never returns another project's
row, and a goal/task selector returns only rows carrying that exact selector.
Rows with expired leases or `stopped_at` do not appear after a restart/recovery;
the fresh monitor ID produces the only current row. There is no unfiltered or
history query.

The watcher is the reporter, the daemon HTTP server is the only validator and
writer, and `Store` is the only SQLite writer. `runWatch` and
`runCodexMonitorWatchScoped` create a `watchHealthReporter` using their
reconciliation HTTP client/base URL. It POSTs after every successful
reconciliation and each lifecycle transition; deferred normal cleanup posts
`stopped` for the same identity.

```go
type monitorHealthReport struct {
	MonitorID, AgentKey, Role, State, Reason string
	ProjectID                              int64
	GoalID, TaskID                         *int64
	PID                                    int
	ProcessStartedAt                       time.Time
}

func (s *Server) handleMonitorHealth(w http.ResponseWriter, r *http.Request)
func (s *Store) UpsertMonitorHealth(ctx context.Context, report MonitorHealth) error
func (s *Store) StopMonitorHealth(ctx context.Context, monitorID string, stoppedAt time.Time) error
```

`AgentKey` is the launcher-provided stable session key when available, and is
otherwise empty; it is display metadata, never authorization. The daemon
validates role eligibility, a positive project ID, goal/task membership with
the existing `eventScopeIDs` lookup, PID/process-start presence, and an ID
derived from those fields. It rejects commander, unscoped, malformed,
mismatched, and forged requests with HTTP 400. A failed health POST is a local
diagnostic and is retried after the next successful reconciliation; it cannot
return from the watch loop or disable a healthy TUI.

### API and compatibility boundary

The implementation adds no durable delivery cursor, event replay, new
workflow state transition, or authority mutation. `monitor_health` is an
observational, upserted read model keyed by the monitor invocation and scoped
to one project plus one optional goal or task; it is not a work queue and does
not participate in reconciliation authorization. Reconciliation remains the
canonical source for decisions, handoffs, tasks, and goals; SSE remains only a
best-effort wake-up.

### Recoverable and terminal error boundaries

`runCodexMonitorWatchScoped` currently passes `ensure=nil` to
`watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor`, and
`runCodexMonitorWithDeps` calls `disableMonitor` for every non-cancelled
`watchDone` error. The first regression test reproduces that a snapshot,
reconciliation, SSE read, or daemon-ensure failure escaping that loop wrongly
disables an otherwise healthy Codex session.

Pass an `ensureWatchDaemon(dir)` closure from `runCodexMonitorWithDeps` through
`runCodexMonitorWatchScoped`. Snapshot, reconciliation, SSE-open/read, and
daemon-ensure errors remain inside the watch loop: it reports bounded recovery,
waits with `waitForWatchReconnect`, snapshots, reconciles, and subscribes
again. The loop returns only on context cancellation or `watchSinkError` from
a disabled bridge. App Server/bridge errors from `bridge.Run`, disabled-bridge
sink errors, and TUI lifecycle errors remain terminal; the supervisor's
`watchDone` case classifies these explicitly rather than disabling on every
watcher error.

On a quiet connected SSE stream,
`consumeWatchEventsWithStateAndScopeAndSinkAndInterval` owns the existing
30-second reconciliation ticker plus a 10-minute liveness ticker. The latter
uses only the most recent successful reconciliation, applies the pure
role/scope and open-decision predicates, and emits through the sink. A sink
failure follows the existing `watchSinkError` boundary; it neither resets
reconnect state nor modifies decision/wakeup/detection delivery maps.

The existing CLI wording for reconnect, keepalive-missing, and daemon-ensure
failures remains available as local diagnostics.  The new state reports are
transition-based, bounded, and do not make every retry a commander event.

## Non-goals

- Changing goal/task/handoff/decision authorization or their lifecycle.
- Prompting project-wide commander monitors.
- Treating a liveness prompt as evidence that a worker is stale or failed.
- Persisting prompts or replaying them after reconnect/restart.
- Replacing the current watch reconnection, reconciliation, or Codex bridge
  queue architecture.

## Delta: canonical agent-action selection

Claude watch output and the Codex bridge may use different delivery adapters,
but they must not independently decide which canonical notifications warrant an
agent action. Today `emitWatchDecisionWithStateAndSinks` formats the watch line
and sends it to the normal sink, while Codex separately repeats a line-prefix
allowlist in `isCodexMonitorActionLine`. This makes the two paths susceptible to
drift; `codexMonitorWatchOutput` repeats the same Codex predicate for output
suppression.

Introduce a package-local canonical `watchAgentAction` value with the exact
formatted line, event name, and goal ID. The canonical selector takes only the
typed canonical notification (`eventName` plus `watchDecision`) after
`formatWatchDecision`; it is the sole owner of whether it is an agent action.
Before changing behavior, freeze the current allowlist as an explicit
event-name/representative-line/membership table, not as prose or a prefix
switch. Its rows must be exactly these current cases:

| Event/line family | Representative line | Action |
| --- | --- | --- |
| Decisions (true) | `atct decision answered (decision_id: 1)`; approved; rejected | yes |
| Decisions (false) | default-applied; pending; opened/asked; expired; unknown decision status | no |
| Goal creation | `atct goal created (goal_id: 1)` | yes |
| Wakeup and liveness | `atct wakeup: ...`; `atct monitor liveness: ...` | yes |
| Goal detection | `atct detection: goal 1 ...` | yes |
| Task handoff lifecycle | requested; received; completed | yes |
| Goal handoff lifecycle | requested; received; completed | yes |
| Report/yield | `atct handoff reported: goal ...`; task; `atct handoff yielded: task ...` | yes |
| Task/goal/plan *review* lifecycle | review requested; received; rejected | yes |
| Accepted task detection suffixes | `is doing without a work lock`; `has no handoff request`; `has a stale claim` | yes |
| Wakeup errors | `atct wakeup discrepancy: ...`; `atct wakeup evaluate failed: ...` | yes |
| Ordinary plan handoff lifecycle | `atct plan handoff requested`; received; completed | no |
| Everything else | keepalive, reconnect/ensure diagnostics, unknown raw text, other task-detection suffixes | no |

The table is a frozen compatibility contract: plan request/receive/complete
lines are explicitly negative rows and must not become actions merely because
they resemble the admitted plan *review* lines. Additions require a separate
approved delta. It must preserve `deliveryGeneration` and every pre-selector
deduplication rule in `emitWatchDecisionWithStateAndSinks`.

The normal watch (Claude) adapter has two deliberately different outputs:
`runWatch` supplies a `watchAgentActionSink func(watchAgentAction) error` to
`watchLoopWithEnsureAndProjectIDAndScopeAndActionSink`; selected typed actions
reach that sink, while its `watchRawLineSink func(string) error` continues to
receive human-readable diagnostic stdout. Local reconnect, ensure, keepalive,
and other diagnostics must never be silently promoted to an agent action.

The Codex adapter receives the same `watchAgentAction` through a typed
`ActionSinkWithContext(ctx) watchAgentActionSink`; it converts it once to the
existing structured `codexMonitorAction` queue item and may queue it while the
thread is active, but must not reimplement classification. By contrast,
`LineSinkWithContext(ctx) watchRawLineSink` is a legacy raw-compatibility path:
its `func(string)` input is diagnostic-only and may not classify prefixes or
enqueue arbitrary strings. A trusted canonical envelope is decoded before this
boundary into `watchAgentAction`, then sent to `ActionSinkWithContext`; raw text
never crosses that typed boundary. `codexMonitorWatchOutput` consumes the same
selector result rather than a local prefix policy. The structured
`codexMonitorAction` queue, `deliveryGeneration`, and stale queued-approval
pruning on `goal.handoff.receive` remain unchanged.

### Claude Monitor entrypoint (Decision 705)

Decision 705 deliberately rejects a stdout/stderr capture assumption. The
Claude-specific entrypoint is therefore `atct watch --monitor` (with the
existing `-goal` or `-project` selector), while plain `atct watch` retains its
existing human-readable stdout contract unchanged. `skills/start/SKILL.md` must
attach Claude's persistent Monitor to `atct watch --monitor -goal <goal_id>` or
`atct watch --monitor -project`, never to the plain command.

`runWatch` receives two writers: the existing `humanWriter` for plain mode and
a `monitorActionWriter` for monitor mode. After formatting and all existing
deduplication, `selectWatchAgentAction` returns one typed value. In monitor
mode that value is serialized exactly once to `monitorActionWriter`; non-action
lines—including registration/roster output, reconnect, ensure, keepalive, and
unknown raw diagnostics—are not written to the Monitor process at all. They
remain on plain-watch human stdout; health/recovery state remains observable via
the existing Goal249 monitor-health HTTP read, without manufacturing agent
actions. There is no stdout-plus-sink duplicate delivery.

```
watchDecision -> format/dedup -> selectWatchAgentAction
                                  | true
             Claude --monitor: monitorActionWriter <- watchAgentAction
             Codex monitor: ActionSinkWithContext  <- same watchAgentAction
                                  | false
             no agent delivery; diagnostics stay plain-watch stdout / health
```

The exact Go seam is `type watchAgentActionSink func(watchAgentAction) error`;
`watchLoopWithEnsureAndProjectIDAndScopeAndActionSink` changes its final action
parameter from `func(codexMonitorAction) error` to that type. The Claude
`monitorActionWriter` adapter and Codex `ActionSinkWithContext(ctx)
watchAgentActionSink` implement the identical type. `type watchRawLineSink
func(string) error` remains diagnostic
only and `LineSinkWithContext` cannot classify it. The required evidence is a
black-box `atct watch --monitor` subprocess against an isolated fixture plus a
writer-boundary integration: inject one selected canonical line and one raw
diagnostic through the test seam; assert stdout contains the selected line once
and contains no diagnostic bytes. This is sufficient because the existing
Claude Monitor contract attaches the CLI output stream, while `runWatch` is the
sole CLI-to-Monitor boundary. It avoids unsafe shared daemon/SSE disruption.
An attached Claude Monitor capture is useful supplementary evidence when an
environment already has one, but is not a gate: diagnostics cannot safely be
injected there without breaking shared infrastructure.

For every row in the frozen baseline plus diagnostics, keepalives, and unknown
raw input, the Claude agent-action adapter and Codex adapter must receive
identical action membership and exact line text. Claude diagnostic stdout is
asserted separately and is never compared as an action channel. The only
permitted action-delivery difference is transport: Claude invokes its action
sink; Codex queues/starts a turn when idle.

## Acceptance criteria

- A transient snapshot, reconciliation, or SSE failure leaves a scoped Codex
  TUI alive; recovery reconciles before resuming prompts.
- A healthy eligible scoped monitor produces no more than one liveness prompt
  per ten minutes, even when wakeup content is unchanged.
- Project-wide monitors never produce a liveness prompt, and goal/task prompts
  never cross their selectors.
- A scope with an open human decision produces no liveness prompt; normal
  decision notifications and subsequent eligibility remain correct.
- Recovery/degraded/healthy transitions are each visible once to the
  commander without exposing out-of-scope canonical state.
- Existing wakeup, decision, handoff, detection, and Codex bridge tests keep
  their semantics.
- For identical formatted notification inputs, Claude and Codex use the same
  canonical action membership and text; only their delivery adapters differ.
