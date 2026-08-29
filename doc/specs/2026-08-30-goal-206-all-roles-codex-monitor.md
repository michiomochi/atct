# Goal 206: role-configurable Codex monitor

Date: 2026-08-30

## Outcome

Extend the explicit `atct codex monitor` entry point so a newly launched
Codex process can receive only the ATCT notifications for the role it was
started to perform:

| Explicit role | Required selector | Monitor scope |
|---|---|---|
| `commander` | none | current project |
| `subcommander` | `--goal <goal-id>` | that goal |
| `executor` | `--task <task-id>` | that task |

The role and its selector are launch metadata, not a claim or a substitute for
the worker's required `session_identify`, handoff receipt, and `atct_role`
check. A worker still receives its handoff and proves its daemon-derived role
before it works.

The legacy form remains valid:

```text
atct codex monitor [-- <codex interactive arguments>]
```

It retains the existing project-scoped, fail-open monitor behavior and does
not acquire an implicit role. The new role-aware form is explicit:

```text
atct codex monitor --role commander [-- <codex interactive arguments>]
atct codex monitor --role subcommander --goal <goal-id> [-- <codex interactive arguments>]
atct codex monitor --role executor --task <task-id> [-- <codex interactive arguments>]
```

`--role` is only interpreted before the literal `--`; everything after `--`
continues to be passed to Codex unchanged. Known non-interactive Codex
subcommands retain their existing pass-through behavior and do not start a
monitor.

## Fail-closed explicit configuration

The role-aware contract fails closed. The command must reject, without starting
normal Codex and without starting an App Server, when any of these applies:

- `--role` is not `commander`, `subcommander`, or `executor`;
- `commander` is combined with `--goal` or `--task`;
- `subcommander` lacks `--goal`, has `--task`, or names an unresolved goal;
- `executor` lacks `--task`, has `--goal`, or names an unresolved task;
- an explicit selector cannot be resolved to the current project, or the task
  does not belong to the selected task's project context.

The exact project membership checks use canonical IDs after resolving the
selector. A role-aware invocation must not silently downgrade to a cwd-derived
project monitor, an unfiltered monitor, or ordinary Codex. That downgrade
would let a worker begin without the role/scope guarantee requested at launch.

This is intentionally narrower than the legacy monitor. Its existing setup
and post-launch watcher failures remain fail-open so normal Codex stays usable;
the new fail-closed rule applies only to invalid or unresolvable explicit role
configuration before a process is launched.

## Launch ordering and no retrofit

The delegator records the task handoff request before waking an executor. For
Herdr, the monitored launch replaces a plain worker process launch and happens
after that request succeeds and before the worker process exists:

```sh
herdr pane run <pane> atct codex monitor --role executor --task <task-id> -- <codex args>
```

The analogous commander and subcommander launches use `--role commander` and
`--role subcommander --goal <goal-id>`. The process then follows its ordinary
handoff instructions. A currently running normal Codex process is never
retrofitted: the supervisor's pre-launch baseline and new-thread discovery are
what make attachment safe. Operators must leave an existing pane intact and
start a new monitored process if scoped delivery is required.

This changes neither Claude's `atct watch -project` / `atct watch -goal`
behavior nor Claude's monitor attachment path. It only adds context to the
explicit Codex wrapper.

## Event and filtering contract

Add `task_id` to the SSE event filter alongside the existing `project_id` and
`goal_id` filters. A `task_id` stream sends only events whose event envelope
names that task. It must also apply project and goal constraints when they are
present; mismatched or task-less events do not pass. The task ID is canonicalized
before streaming, just as project and goal IDs are.

The watch layer carries a scope value rather than treating an empty goal ID as
the only distinction. It builds the matching SSE query and applies the same
scope to snapshot and live events. Task scope formats and forwards only the
task handoff/review transitions and detections that name its task. It must not
receive other tasks in the same goal or project.

Existing routing is preserved rather than redefined:

- commander/project delivery remains limited to commander-actionable events;
- subcommander/goal delivery receives that goal's handoff/review transitions;
- executor/task delivery receives its task handoff/review transitions,
  including review receive, complete, and reject that tell it to resume or
  stop;
- existing `handoff_reported`, `handoff_yielded`, and detection routing keeps
  its current destination semantics, augmented only by task identity where the
  event already has one.

The implementation uses the existing generic `store.DetectionEvent` envelope
as the boundary for future review-related detections. That envelope already
contains project/goal/task identity used by filtering and formatting. This goal
does **not** create review event producers, review-state storage, MCP/HTTP APIs,
or review-specific detection producers. Those remain residual work from the
execution-flow review protocol. The monitor must nevertheless make no
assumption that a future detection is project-only: a `DetectionEvent` carrying
`TaskID` follows task filtering.

## Implementation boundary

The CLI parser carries a role plus optional canonical selector inputs into the
monitor supervisor. The supervisor resolves explicit selectors before starting
the App Server, then gives a typed watch scope to `runCodexMonitorWatch`. The
watch URL, `watchScopeFilter`, formatter, Codex action-line whitelist, and HTTP
SSE filter all consume the same scope model. A formatted eligible event is sent
through the existing bridge FIFO; raw SSE frames, keepalives, and watcher
diagnostics remain outside the model input boundary.

The monitor registry may continue to identify processes by project path for
stop/reaping; role/scope metadata is not needed to alter the established
lifecycle contract in this goal.

## Focused verification

Tests must demonstrate all of the following:

- legacy no-role parsing, project filter, and fail-open fallback are unchanged;
- each role accepts exactly its required selector shape, and every invalid or
  unresolved explicit configuration starts neither normal Codex nor an App
  Server;
- supervisor receives the selected scope before it launches the worker;
- `/api/events?task_id=` filters same-goal and other-goal task events, and
  preserves existing project/goal behavior;
- task-scoped watch formatting, deduplication, and Codex action admission
  deliver only the selected task's handoff/detection events;
- project and goal watches continue to route their existing handoff and
  detection events; and
- an existing Codex session is never attached or retrofitted by a new scoped
  invocation.
