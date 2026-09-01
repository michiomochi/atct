# Goal 225 execution-flow Implementation Plan

> **For ATCT workers:** this plan is executed through `doc/execution-flow.md`: one executor per independent ATCT task, subcommander review after each task, and commander plan review before the first implementation handoff. Do not use a separate subagent orchestration loop.

**Goal:** Make the actual commander / subcommander / executor handoff protocol, role derivation, notifications, and worker lifecycle match `doc/execution-flow.md`.

**Architecture:** Introduce explicit review records/states before exposing their daemon and MCP contracts. State-changing store transactions are the only writers of handoff status and events; daemon, MCP, watch, and monitors consume those transitions. Compatibility aliases protect existing generic task-handoff callers while role-specific names become canonical.

**Tech Stack:** Go, SQLite migrations/sqlc, daemon RPC, MCP shim, SSE, `atct watch`, Codex/Claude monitor wrappers, Bash contract tests.

## Global Constraints

- No implementation task starts until commander accepts this plan through the plan handoff review.
- Preserve B1〜B4 from `doc/specs/2026-09-01-execution-flow-b1-b4.md`.
- The delegator reuses a completed executor pane for an unassigned follow-up task; a new pane requires parallel work, worktree isolation, context exhaustion, or a topic change.
- All state transition tests assert both the handoff record and its derived role/status.
- Generic `handoff.*` / `atct_handoff_*` remain working aliases until all callers move to task-specific names.
- For chezmoi-managed skill sources: edit source, show `chezmoi diff`, obtain explicit approval, then and only then run `chezmoi apply`.

---

## File / responsibility map

- `internal/store/migrations/`: handoff review and plan-review schema changes.
- `internal/store/*handoff.go`, `task.go`, `goal.go`, `decision.go`: transactional state machines and authorization.
- `internal/store/queries/task.sql` and generated sqlc output: query layer for state changes.
- `internal/domain/status.go`: canonical `review` task status and transition validation.
- `internal/daemon/handler.go`: named RPC methods and role-bearing receive results.
- `internal/mcpshim/tools.go`: named MCP schemas and legacy aliases.
- `internal/store/wakeup.go`, `internal/httpapi/server.go`, `cmd/atct/watch.go`: event publication, filtering, and rendering.
- `cmd/atct/*monitor*`, `tests/wrapper_test.bash`, skills sources: lifecycle and operator contract.

## Task 1: Persist handoff review and task state transitions

**Files:**
- Create: the next numbered SQLite migration for plan handoffs and review timestamps/reports.
- Modify: `internal/store/task_handoff.go`, `internal/store/goal_handoff.go`, `internal/store/task.go`, `internal/domain/status.go`, `internal/store/queries/task.sql` and regenerated `internal/store/sqlcgen/*`.
- Test: `internal/store/task_handoff_test.go`, `internal/store/goal_handoff_test.go`, new plan-handoff store tests, `internal/store/task_test.go`.

**Interfaces:**
- Produces `Request*Review`, `Receive*Review`, `Complete*Handoff`, and `Reject*Review` store operations for task, goal, and plan records.
- Produces `TaskReview` as the only new task status; `task` completion transitions it to `done` in the same transaction.

- [ ] **Step 1: Write failing transition tests**

Cover `requested -> received -> review_requested -> review_received -> completed`, reject back to received/doing, wrong reviewer rejection, and no second open handoff. Assert timestamps, reports, task status, and claim ownership after every call.

- [ ] **Step 2: Run focused store tests and record the failures**

Run: `go test ./internal/store -run 'Test(Task|Goal|Plan)Handoff.*Review|TestTask.*Review' -count=1`

Expected: compile or assertion failures because review operations/table/status do not exist.

- [ ] **Step 3: Add the migration and transactional store operations**

Model a review as request/receive fields distinct from completion. Ensure reject clears only reviewer fields, preserves the receiver/claim, and returns the task to `doing`. Ensure completion is authorized to the recorded reviewer and atomically writes `done`.

- [ ] **Step 4: Regenerate queries and make focused tests pass**

Run the repository's sqlc generation command from its build tooling, then rerun the Step 2 command.

- [ ] **Step 5: Commit the isolated state-machine change**

Stage only migration, store, generated query, and focused test paths; commit with an explicit state-machine message.

## Task 2: Expose named review and receive-role contracts

**Files:**
- Modify: `internal/daemon/handler.go`, `internal/daemon/task_handoff_test.go`, `internal/daemon/goal_handoff_test.go`, new daemon plan-handoff tests.
- Modify: `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go`.
- Modify: `cmd/atct/main.go` and its handoff command tests if CLI exposes generic names.

**Interfaces:**
- Consumes Task 1 store operations.
- Produces `task.handoff.*`, `goal.handoff.*`, `plan.handoff.review.*`, and receive payload `{data, role, claim_evidence}`.

- [ ] **Step 1: Write failing daemon and MCP schema tests**

Assert that named task handoff tools/routes exist; each receive response contains the role selected from project → goal → task evidence; plan review request has no task claim; and legacy generic task routes delegate to the named implementation.

- [ ] **Step 2: Run focused RPC/schema tests**

Run: `go test ./internal/daemon ./internal/mcpshim -run 'Test.*(Handoff|Plan|Role|Schema)' -count=1`

Expected: failures for missing named/review methods and missing receive payload fields.

- [ ] **Step 3: Implement RPC/MCP adapters without duplicating authorization**

Have each handler call Task 1's transaction, wrap receive with daemon-derived role evidence, register canonical task/goal/plan review names, and retain generic task-handoff aliases as adapters with explicit deprecation coverage.

- [ ] **Step 4: Make contracts pass and check compatibility**

Run the Step 2 command plus `go test ./internal/mcpshim -run 'TestHandoffToolsInjectAgentSessionID|Test.*Legacy.*Handoff' -count=1`.

- [ ] **Step 5: Commit only API and contract-test paths**

## Task 3: Assign commander completion and goal-scoped decisions

**Files:**
- Modify: `internal/store/goal.go`, `internal/store/decision.go`, the next migration if a goal-review record is required.
- Modify: `internal/daemon/handler.go`, `internal/mcpshim/tools.go`.
- Test: `internal/daemon/goal_complete_guard_test.go`, `internal/store/goal_complete_test.go`, `internal/store/decision_constraint_migration_test.go`, `internal/daemon/pending_response_test.go`.

**Interfaces:**
- Consumes Task 1 goal review state and Task 2 role evidence.
- Produces commander-only goal-handoff completion, human goal-review request/reject/approve lifecycle, and goal-scoped decisions without a task id.

- [ ] **Step 1: Validate the current HTTP human-review path before changing it**

Read `internal/httpapi/server.go` and its approval tests named in the investigation. Record whether it can persist `atct_goal_review_*`; if it cannot, add a dedicated goal-review record in the same migration family rather than overloading decisions.

- [ ] **Step 2: Write failing authorization and lifecycle tests**

Assert subcommander cannot close its own goal handoff or write `goal.complete`; commander can accept/reject goal review; human rejection causes only commander to issue a new goal handoff; and taskless commander decisions satisfy the migrated constraint.

- [ ] **Step 3: Implement the validated storage/API path**

Keep the handoff-closure summary distinct from the final six-field `goals` report. Make commander the only writer of both at their separate execution-flow steps.

- [ ] **Step 4: Run focused completion/decision tests**

Run: `go test ./internal/store ./internal/daemon ./internal/httpapi -run 'Test.*(Goal.*(Review|Complete)|Decision.*Constraint|GoalScoped)' -count=1`

- [ ] **Step 5: Commit the commander/human-review change**

## Task 4: Publish and deliver review-state notifications

**Files:**
- Modify: `internal/store/wakeup.go`, `internal/httpapi/server.go`, `cmd/atct/watch.go`.
- Test: `internal/httpapi/server_test.go`, `cmd/atct/watch_scope_test.go`, `cmd/atct/*watch*_test.go`, monitor tests covering Codex and Claude delivery.

**Interfaces:**
- Consumes Task 1 transition events and Task 3 human goal-review events.
- Produces one scope-filtered event for each request/receive/reject transition.

- [ ] **Step 1: Write failing SSE/watch tests**

For task review, plan review, and goal review events, assert delivery to only the owning goal/project watch, no delivery to another goal, and one formatted wake-up per persisted transition.

- [ ] **Step 2: Run focused notification tests**

Run: `go test ./internal/httpapi ./cmd/atct -run 'Test(SSE|Watch|Codex|Claude).*(Review|Handoff)' -count=1`

- [ ] **Step 3: Add event types and formatter/filter branches**

Publish only inside the successful state-change transaction path. Extend `eventMatchesGoalID`, `eventProjectID`, and `formatWatchDecision` for each new event; do not alter the withdrawal event until its existing publish gate is separately verified.

- [ ] **Step 4: Make focused notification tests pass and commit**

## Task 5: Align worker reuse and role instructions

**Files:**
- Modify: source files that generate `skills/atct/SKILL.md` and `.agents/skills/orchestration/SKILL.md`; locate their chezmoi source before edit.
- Modify: `tests/wrapper_test.bash` and any skill-contract tests.

**Interfaces:**
- Consumes canonical Task 2 names and Task 4 event/monitor behavior.
- Produces one operator contract for reusing an idle executor with an unassigned task and creating a pane only for the four named conditions.

- [ ] **Step 1: Locate management source and write failing contract assertions**

Use `chezmoi source-path` / repository ownership metadata to identify the source. Add assertions for task-specific handoff names, review order, commander completion ownership, and executor reuse conditions.

- [ ] **Step 2: Edit sources only and obtain diff approval**

Run `chezmoi diff` after source edits. Present the diff to the human. Do not run `chezmoi apply` before explicit approval.

- [ ] **Step 3: After approval, apply and run focused wrapper tests**

Run: `bash tests/wrapper_test.bash`

Expected: canonical procedure and wrapper contract tests pass.

- [ ] **Step 4: Commit source/test changes with explicit paths**

## Task 6: Prove end-to-end flow and executor lifecycle

**Files:**
- Modify: the narrowest existing daemon/integration test files for goal/task handoffs and monitor wrappers.
- Test: `internal/daemon/*handoff*_test.go`, `cmd/atct/*monitor*_test.go`, `tests/wrapper_test.bash`.

**Interfaces:**
- Consumes all prior tasks.
- Produces a regression fixture for request → receive → review → reject/retry → complete, final goal review, human approve/reject, and executor reuse/new-pane decision.

- [ ] **Step 1: Add failing end-to-end scenarios**

Exercise commander → subcommander plan review before task request; executor review rejection followed by same-worker retry; later unassigned task handed to the idle executor; and a new executor only when the fixture flags parallel/isolation/context/topic conditions.

- [ ] **Step 2: Run the focused end-to-end suite**

Run the exact test packages selected by the fixture; do not use a broad repository test command in an executor task.

- [ ] **Step 3: Fix only integration seams exposed by the scenarios**

- [ ] **Step 4: Run final focused verification and diff checks**

Run: `git diff --check` and the focused store, daemon, MCP, HTTP/watch, monitor, and wrapper commands above.

- [ ] **Step 5: Commit the integration tests and verified fixes**

## Plan review gate

The subcommander submits this plan through `atct_plan_handoff_review_request`. The commander receives and reviews it. Rejection returns to specification/plan revision; acceptance is the only authorization to declare and delegate Tasks 1–6. During this gate no implementation code, skill source, chezmoi state, or executor handoff for implementation changes is modified.
