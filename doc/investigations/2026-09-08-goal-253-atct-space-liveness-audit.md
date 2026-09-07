# Goal 253 — ATCT space liveness audit

Date: 2026-09-08 (JST)  
Scope: every current ATCT Herdr space, its live monitor registration where
present, and the user-named related goals. This is an observation and design
boundary document; it makes no production change.

## Method and limits

The audit used three independent local sources:

1. `herdr agent list` for the currently extant ATCT panes and each pane's
   `working` / `idle` / `done` state;
2. `~/.atct/codex-monitors/*.json` plus `kill -0 <supervisor_pid>` for monitor
   supervisors that are actually live; and
3. `atct_goal_get`, `atct_goal_sessions`, and readonly SQLite queries for task,
   handoff, role, and open-decision state.

`codex-monitors` is deliberately under `~/.atct`, not a worktree. A missing
worktree-local directory is therefore not evidence that a space is unmonitored.
Conversely, a live supervisor only proves that a monitor was launched; it does
not prove that its selector admitted a particular action or that its current
agent can act on it. The classification below states that distinction.

## Current-space classification

| Current space(s) | Measured state | Classification | Evidence / operational meaning |
| --- | --- | --- | --- |
| `227`, `236`, `237`, `238`, `240`, `245`, `246`, `248`, `251` | pane `done`; each except `251` has a live Codex-monitor record | normal work finished, but follow-up may remain | A done pane is not a restart path. Goals 227/248 await human review; 237/240 are separately blocked by integration work. |
| `173`, `192`, `230` | pane `done`, live monitor record, all recorded tasks terminal | normal completion / pending goal finalization | No executor work remains; do not generate liveness work merely because the monitor remains alive. |
| `221` | pane `idle`, live monitor; all implementation tasks done; open `goal_review` decision 710 | human-decision wait | The open review is intentional. `PromptDue` currently suppresses liveness for a scoped open decision. |
| `228` | pane `done`, live monitor; task 1183 doing and task 1200 todo; open task decision 709 | dependency/merge blocked + human-decision wait | Its own task text requires main integration before migration work. A liveness ping is not a substitute for the required decision/integration. |
| `237` | pane `done`, live monitor; task 1211 todo | dependency/merge blocked | The remaining task explicitly requires merging current main and re-verification. |
| `240` | pane/executor `done`, two live monitors; task 1212 doing | dependency/merge blocked | The outstanding task is the real merge/commit reconciliation, not missing notification delivery. |
| `245` | pane `done`, live monitor; task 1204 doing; open task decision 704 | dependency/merge blocked + human-decision wait | Goal 245 explicitly waits for Goal 249/228 migration ownership before executor delegation. |
| `231`, `234`, `241`, `146`, `249`, `252` | one or more panes `idle`; each has one or more live monitor records | monitored; state needs the owning goal's workflow decision | These are not evidence of a generic monitor failure. Their idle panes are covered by monitor processes, but a monitor must still emit an actionable event and a role-valid session must consume it. |
| `253` | subcommander `working`; executor pane now `done`; two live monitor records | monitored action-loss remediation in progress | Task 1215 reproduced the receipt-suppression loss and is now committed as a safe unit. Task 1216 remains gated on this expanded plan review. |
| `203` (worktree exists; no current Herdr pane or live monitor record) | goal proposed; recorded session `atct-root-commander` derives `subcommander` | session/role mismatch, unmonitored legacy space | This is the documented key-vs-session-ID failure. It is a separate identity/diagnostic solution, not a delivery-selector fix. |
| `145`, `202`, `206`, `213`–`217`, `219`, `222`, `223`, `250` | worktrees exist but no current ATCT Herdr pane and no live monitor record | unmonitored / no current agent space | These are legacy or inactive worktrees. They cannot receive monitor-driven recovery until explicitly resumed through the monitor wrapper. |

The live registry contains records for the root project and worktrees
`146,173,192,221,227,228,230,231,234,236,237,238,240,241,245,246,248,249,251,252,253`.
It has multiple live supervisors for `146,231,234,240,241,249,253`. That proves
coverage was mixed historically, not that every old space bypassed the wrapper.
The correct permanent rule is therefore to surface *monitor coverage and action
outcome* rather than infer either from a pane title.

## Reproduced stop paths

### 1. Received approval erased from recovery

Before Task 1215, `shouldProjectAppliedGoalApproval` suppressed an applied
`goal_approval` when *any* matching goal handoff had `ReceivedAt`. The same
approval therefore ceased to be projected after a reconnect/reconcile exactly
when the commander needed a recovery action. The historical reconciliation path
also bypassed the normal delivery map, allowing repeated actions (the Decision
700 class of duplication).

Task 1215 changes only this path: it routes the projection via
`emitWatchDecisionWithStateAndSinks`, using the shared delivery key and selector,
and removes `ReceivedAt` from eligibility. Focused tests now require a received
handoff to produce exactly one `decision.approved` action across two
reconciliations; done and dropped goals remain silent.

### 2. Selector omission after a completed plan handoff

`watch_scope.go` admits `plan.handoff.complete` to a scope, and
`formatWatchDecision` can render it. But `selectWatchAgentAction` deliberately
does not select it: `watch_action_test.go` freezes an ordinary
`plan.handoff.complete` as `want: false`. The monitor can thus see/render the
event without injecting a Codex/Claude action. This is the measured reason a
manual restart was needed after plan completion.

### 3. Open decision suppresses liveness entirely

`watchLivenessState.PromptDue` resets its timer and returns false whenever
`scopedOpenDecision(scope, snapshot)` is true. The current database has open
task decisions for goals 228 and 245 and open goal-review decisions for 221 and
251. Suppression prevents noise while human judgment is genuinely needed, but
it also hides unrelated recoverable actions if liveness is the only delivery
path. Action delivery must therefore be independent of liveness suppression.

### 4. Role/session identity prevents a valid restart

Goal 203 records the concrete case: a session ID was embedded in a new session
key, producing an executor-derived role where a commander was expected. Goal
221 owns heartbeat leases, stale takeover, and key/instance/owner separation.
Neither can be solved by making liveness more frequent.

### 5. Handoff/review completion has no actionable restart edge

Goal 252 addresses a related, but different, terminal gap: a reviewed delegated
handoff can complete without creation of a human goal-review request. Its
accepted plan owns a read-only detection and delivery keyed by handoff ID and
completion generation. Goal 253 must consume common selector/delivery mechanics
only; it must not recreate Goal 252's validator or wakeup state.

### 6. Dependency and merge stoppage is not an action-loss symptom

Goals 228, 237, 240, and 245 have explicit migration or current-main merge
prerequisites. Auto-prompting them as liveness failures would be false-positive
pressure and risks duplicate migration work.

## Consolidation boundary

| Goal | Ownership retained | Goal 253 integration rule |
| --- | --- | --- |
| 182 | distinguish a silent subcommander from long-running normal work | consume its future health signal as an input; do not add a second health model. |
| 203 | session-key discovery, mismatch diagnosis, recovery guidance | selector actions must remain role-neutral; no key heuristics here. |
| 221 | shared Claude/Codex heartbeat lease and stale takeover | use the same liveness ownership; do not alter lease schema/API. |
| 227 | role preservation around goal-handoff completion/rejection | preserve its receiver/authority lifecycle; test only delivery around it. |
| 228, 237, 240, 245 | their explicit integration and migration dependencies | classify and expose blockers; never auto-resume or duplicate their implementation. |
| 248 | local-response and delegated-handoff notification suppression | preserve bounded origin/suppression semantics; Goal 253 fixes loss after eligibility, not 248's intended self-noise suppression. |
| 252 | missing-human-goal-review detection/recovery | retain its canonical predicate and generation; only share delivery-key/action selection infrastructure. |

## Required expanded acceptance criteria

1. Every eligible commander approval state, including an already-received
   handoff, becomes exactly one commander action per monitor lifecycle.
2. Reconnect/reconcile never makes the same generation replay indefinitely;
   live and reconciled paths share the delivery key.
3. Goal-scoped subcommanders retain independently actionable received/rejected
   lifecycle actions even if project-scope liveness is suppressed.
4. Claude and Codex use one selector membership contract, with focused parity
   tests for every new event class.
5. `plan.handoff.complete` becomes an explicit design choice: either a selected
   actionable recovery event with lifecycle dedupe, or a documented non-action
   with a distinct canonical detection. It must not remain a silently rendered
   event.
6. The monitor exposes coverage/state sufficient to distinguish no wrapper,
   selector suppression, queue loss, role mismatch, human decision wait, and
   dependency/merge block. It must not create a second notification for the
   already-delivered Decision 700 generation.

## Commands and direct evidence

```text
git worktree list --porcelain
herdr agent list
find ~/.atct/codex-monitors -maxdepth 1 -type f
kill -0 <each supervisor_pid>
atct_goal_get / atct_goal_sessions for 182,203,221,227,228,237,240,245,248,252,253
sqlite3 -readonly ~/.atct/atct.db "SELECT ... FROM decisions WHERE status='open'"
rg -n 'selectWatchAgentAction|plan.handoff.complete|scopedOpenDecision|shouldProjectAppliedGoalApproval' cmd internal
```

