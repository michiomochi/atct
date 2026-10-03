# Goal 260: Workflow and Monitor End-to-End Contract

## Goal

Prove one complete, daemon-backed lifecycle delivers each actionable handoff
only to its designated monitor and never emits a liveness action when that
monitor has no actionable work.  This is an integration boundary for the Goal
260 workflow definition and executor monitor rebinding; it does not redesign
either subsystem.

## Decision

Use one deterministic integration scenario with the real daemon, its persisted
handoff transitions, the HTTP/SSE watch path, and the Codex monitor's typed
delivery queue.  Keep focused unit tests for individual table rows and scope
refreshing; the new test only checks their composition.  A synthetic store-only
test would not prove that a committed workflow event reaches the correctly
scoped monitor, while a subprocess/TUI test would add timing and App Server
dependencies without increasing lifecycle coverage.

## Contract

The test creates separate commander, subcommander, and executor sessions for
one project and goal.  It drives these persisted transitions in order:

1. commander requests the goal; only the prestarted goal-scoped subcommander
   monitor receives the startup request.
2. subcommander receives the goal and submits the plan; only the
   project-scoped commander monitor receives the plan-review request.
3. commander receives and accepts the plan; the subcommander receives the
   task-create request, creates the executor task, and delegates it.
4. only the executor monitor receives the task request.  It receives the task,
   submits review, and only the subcommander monitor receives that review
   request.
5. subcommander receives and accepts the task review.  Its executor monitor
   refreshes to an empty effective task selector: it receives no stale task
   event and emits no `monitor.liveness` action.
6. after all task handoffs are accepted, subcommander requests goal review;
   only commander receives it.  Commander receives and completes the goal
   handoff, then requests human goal review.  No merge operation is attempted
   before `atct_goal_review_complete`; only after that approval may the
   commander-owned merge step run, followed by `atct_goal_complete`.

Every delivery assertion is both positive for the named recipient and negative
for the other two monitors.  The test uses one delivery key per lifecycle
phase, so duplicate SSE reconciliation must not create an extra monitor turn.

## Lifecycle and notification ownership

| Boundary | Persisted action | Sole notification target | E2E assertion |
|---|---|---|---|
| Goal startup | `goal.handoff.request` | goal-scoped subcommander | commander is not self-notified |
| Design review | `plan.handoff.review.request` | commander | subcommander/executor receive nothing |
| Task creation | `task_create` receipt/request | subcommander | no executor task exists before plan acceptance |
| Executor work | `task.handoff.request` | executor | only assigned executor receives it |
| Task review | `task.handoff.review.request` | subcommander | executor does not receive its own review |
| Goal review | `goal.handoff.review.request` | commander | only commander receives it |
| Human approval | `goal.review.complete` | human/commander continuation | merge is not eligible beforehand |

## Liveness boundary

Liveness is evaluated after the executor monitor refreshes its effective task
selector from the daemon's current-executor-handoff projection.  A received
task is actionable and may produce a task liveness action.  A review-requested
or completed task is not actionable; the refreshed selector is empty and both
task SSE filtering and `monitor.liveness` are suppressed.  The scenario also
uses a second executor session to prove an empty selector for executor A never
suppresses executor B's received-task delivery.

## Merge boundary

ATCT can prove ordering for an ATCT-controlled merge workflow: human approval
is recorded before the test's merge continuation, and goal completion follows
that continuation.  It cannot prohibit an arbitrary external `git merge` run
outside ATCT.  If repository-wide prevention of such a command is required,
that is a separate Git hook or server-side protection goal; this goal must not
claim that daemon state alone blocks it.

## Scope and test entry point

- Add the composition test beside the monitor/watch integration tests in
  `cmd/atct/`, where unexported scoped-watch and typed-bridge APIs are
  available.
- Reuse `internal/e2e/full_flow_test.go`'s real daemon/HTTP stack helper, or
  extract only that helper into an internal test package if package visibility
  requires it.  Do not duplicate a fake lifecycle.
- Primary entry point:
  `GOCACHE=/private/tmp/goal260-go-cache go test ./cmd/atct -run TestWorkflowMonitorEndToEndContract -count=1`.
- Regression entry point:
  `GOCACHE=/private/tmp/goal260-go-cache go test ./internal/workflow ./internal/store ./internal/daemon ./cmd/atct -count=1`.

## Non-goals

- No new workflow states, monitor identity persistence, daemon routing rules,
  or production Markdown parsing.
- No source implementation, task declaration, executor pane launch, commit,
  merge, or human-review action occurs until this spec and plan are accepted
  through `atct_plan_handoff_review_*`.
