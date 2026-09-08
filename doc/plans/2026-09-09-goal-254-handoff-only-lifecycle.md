# Goal 254 Handoff-only Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make handoffs and decisions the sole durable workflow state, with one `task.create` contract for both pre-plan and approved-plan task creation.

**Architecture:** Add a task-create handoff that the commander creates atomically when it accepts a plan review. Replace review-work, scope, delivery, and blocker tables with direct reconciliation of handoff and decision rows; the monitor bridge coalesces only in memory. Rename every `task.declare`/`DeclareTasks` API surface to `task.create`/`CreateTasks` without a compatibility alias.

**Tech Stack:** Go, SQLite migrations, sqlc, MCP shim/daemon RPC, `cmd/atct` watcher and Codex bridge, focused Go and Bash tests.

## Global Constraints

- Implement [the approved spec](../specs/2026-09-09-goal-254-handoff-only-lifecycle.md); Goal 253 is historical and must not be resumed.
- Do not retain a `task.declare` RPC, `DeclareTasks` store method, or compatibility alias.
- Do not retain `orchestration_scope`, `orchestration_delivery_leases`, `orchestration_delivery_receipts`, `orchestration_review_work`, or `orchestration_blockers` in the schema or production code.
- Do not add persistent delivery cursors, acknowledgement records, monitor ownership, blocker replacement tables, wrapper restart, or monitor-health recovery.
- Regenerate committed sqlc output with `go tool sqlc generate`; run `script/schema-check.sh` before the final commit.
- Run Go tests with `GOCACHE=/private/tmp/goal254-go-cache`. No Ponytail hooks are used.

---

### Task 1: Add the schema foundation and review-rejection receipt

**Files:**

- Create: `internal/store/migrations/0035_handoff_only_lifecycle.sql`
- Modify: `schema.sql`
- Modify: `internal/store/schema_parity_test.go`
- Modify: `internal/store/migration_integrity_test.go`
- Test: `internal/store/migrations_test.go`

**Interfaces:**

- Consumes: migrations `0031`–`0034` and the current `goal_handoffs`, `task_handoffs`, `plan_handoffs`, and `decisions` rows.
- Produces: `task_create_handoffs`, `task_create_handoff_tasks`, and review-rejection receipt columns. The legacy `orchestration_*` tables remain until Task 4's cleanup migration, so existing lifecycle code remains executable during this stage.

- [ ] **Step 1: Write migration fixture tests before the migration.**

  In `internal/store/migration_integrity_test.go`, create a fixture database at migration 0034 containing one task, goal, and plan handoff plus one decision, and one row in each legacy `orchestration_*` table. Assert that opening it applies 0035, preserves the handoff/decision rows and their IDs, adds the task-create and rejection-receipt schema, and keeps each legacy table present for the staged cutover.

  ```go
  for _, table := range []string{
      "orchestration_scope", "orchestration_delivery_leases",
      "orchestration_delivery_receipts", "orchestration_review_work",
      "orchestration_blockers",
  } {
      assertTablePresent(t, migratedDB, table)
  }
  assertPlanHandoffAndDecisionPreserved(t, migratedDB, planHandoffID, decisionID)
  ```

- [ ] **Step 2: Run the migration fixture test and confirm it fails because 0035 is absent.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./internal/store -run 'TestGoal254Migration' -count=1 -v
  ```

  Expected: FAIL because task-create tables and rejection-receipt columns do not exist.

- [ ] **Step 3: Add migration 0035 and make `schema.sql` match it.**

  Create `task_create_handoffs` with `id`, `plan_handoff_id`, `goal_id`, request/receipt/completion session IDs and timestamps, request/complete reports, and a unique plan-handoff reference. Create `task_create_handoff_tasks(handoff_id, task_id)` with a composite primary key and foreign keys. Add the review-rejection receipt columns required by Task 3. Do not drop legacy tables or indexes in 0035; Task 4 removes their production users before a later cleanup migration drops them. Mirror this intermediate schema in `schema.sql`.

- [ ] **Step 4: Extend schema parity assertions.**

  Update `TestSchemaParityOnMigratedCopiedDatabaseFromEnvironment` so its expected added-table set includes both task-create tables and its expected review-rejection columns. It must still expect the five legacy tables at the 0035 boundary. Keep the environment-backed test optional; the new fixture test must be self-contained.

- [ ] **Step 5: Regenerate SQL bindings and verify schema parity.**

  Run:

  ```sh
  go tool sqlc generate
  GOCACHE=/private/tmp/goal254-go-cache go test ./internal/store -run 'Test(SchemaParity|Goal254Migration|MigrationIntegrity)' -count=1 -v
  ```

  Expected: PASS, including a migration from 0034 with retained handoffs/decisions.

- [ ] **Step 6: Commit the schema boundary.**

  ```sh
  git add schema.sql internal/store/migrations/0035_handoff_only_lifecycle.sql internal/store/sqlcgen internal/store/schema_parity_test.go internal/store/migration_integrity_test.go internal/store/migrations_test.go
  git commit -m "feat: migrate to handoff-only workflow state"
  ```

### Task 2: Add the plan-to-task-create lifecycle and one task.create operation

**Files:**

- Create: `internal/store/task_create_handoff.go`
- Create: `internal/store/task_create_handoff_test.go`
- Create: `internal/store/queries/task_create_handoff.sql`
- Modify: `internal/store/task.go`
- Modify: `internal/store/queries/task.sql`
- Modify: `internal/store/goal_handoff.go`
- Modify: `internal/store/plan_handoff_test.go`
- Modify: `internal/daemon/handler.go`
- Modify: `internal/daemon/goal_handoff_test.go`
- Modify: `internal/mcpshim/tools.go`
- Modify: `internal/mcpshim/schema_test.go`

**Interfaces:**

- Consumes: `CompletePlanHandoff(handoffID, goalID, reviewerID, report)` and a received goal handoff.
- Produces: `TaskCreateHandoff{ID, PlanHandoffID, GoalID, RequestedBy, ReceivedBy, RequestedAt, ReceivedAt, CompletedAt, CreatedTaskIDs}` and RPCs `task.create`, `task.create_handoff.receive`, and `task.create_handoff.complete`.

- [ ] **Step 1: Write the store lifecycle test.**

  In `internal/store/task_create_handoff_test.go`, create commander/subcommander/wrong-session fixtures. Complete a received plan review and assert the same transaction creates one task-create request addressed to the plan submitter. Assert wrong sessions cannot receive, create, or complete; assert the rightful receiver can create idempotently and cannot complete until every returned task has a requested task handoff.

  ```go
  created, err := store.CreateTasks(ctx, taskCreate.ID, subcommanderID,
      "goal-254-impl#1", []string{"Implement task-create lifecycle"},
      []string{"Create the accepted-plan implementation task through the received task-create handoff."})
  require.NoError(t, err)
  require.Equal(t, []int64{created[0].ID}, taskCreate.CreatedTaskIDs)
  require.ErrorIs(t, store.CompleteTaskCreateHandoff(ctx, taskCreate.ID, subcommanderID, "done"), ErrTaskCreateHandoffIncomplete)
  ```

- [ ] **Step 2: Run the focused store test before implementation.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./internal/store -run 'TestTaskCreateHandoff' -count=1 -v
  ```

  Expected: FAIL because the lifecycle type and operations do not exist.

- [ ] **Step 3: Implement the atomic plan acceptance boundary.**

  Have `CompletePlanHandoff` insert the completed plan row and a single task-create request in its existing transaction. The request receiver is `handoff.ReviewRequestedBy`; a duplicate completion must not make a second request. Implement store authorization so the received task-create holder is the only caller allowed to create implementation tasks or complete the handoff.

- [ ] **Step 4: Replace declaration terminology end to end.**

  Rename `Store.DeclareTasks` to `Store.CreateTasks`, its error text and tests to “create”, and daemon method `task.declare` to `task.create`. Make `atct_task_create` invoke only `task.create`. Do not add a fallback switch case or MCP alias for `task.declare`.

  `task.create` accepts a task-create handoff ID when an accepted plan exists. Before any completed plan handoff it creates the design/spec/plan task; after one exists it rejects a request without the received handoff ID. Keep the existing idempotency key/`declare_key` storage column unchanged in this task; its database rename is out of scope for this API-name change.

- [ ] **Step 5: Expose and test the lifecycle through the daemon and MCP schema.**

  Add typed MCP inputs and daemon branches for receiving/completing the task-create handoff and for passing its ID to `task.create`. In `internal/mcpshim/schema_test.go`, assert the only creation RPC emitted by `atct_task_create` is `task.create`, and that `task.declare` is absent from the registered schema/daemon contract.

- [ ] **Step 6: Run focused contract tests.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./internal/store ./internal/daemon ./internal/mcpshim -run 'Test(TaskCreateHandoff|PlanHandoffReviewLifecycle|.*TaskCreate.*|.*TaskDeclare.*)' -count=1 -v
  ```

  Expected: PASS; the old-name cases are replaced with explicit absence/rejection checks.

- [ ] **Step 7: Commit the canonical creation lifecycle.**

  ```sh
  git add internal/store/task.go internal/store/task_create_handoff.go internal/store/task_create_handoff_test.go internal/store/queries/task.sql internal/store/queries/task_create_handoff.sql internal/store/goal_handoff.go internal/store/plan_handoff_test.go internal/store/sqlcgen internal/daemon/handler.go internal/daemon/goal_handoff_test.go internal/mcpshim/tools.go internal/mcpshim/schema_test.go
  git commit -m "feat: route implementation tasks through task create handoffs"
  ```

### Task 3: Make rejection receipt a handoff transition

**Files:**

- Modify: `internal/store/task_handoff.go`
- Modify: `internal/store/goal_handoff.go`
- Modify: `internal/store/queries/task.sql`
- Modify: `internal/store/queries/goal_handoff.sql`
- Modify: `internal/store/task_handoff_test.go`
- Modify: `internal/store/goal_handoff_test.go`
- Modify: `internal/store/plan_handoff_test.go`
- Modify: `internal/daemon/handler.go`
- Modify: `internal/daemon/task_handoff_test.go`
- Modify: `internal/daemon/goal_handoff_test.go`
- Modify: `internal/mcpshim/tools.go`
- Modify: `internal/mcpshim/schema_test.go`

**Interfaces:**

- Consumes: existing `*.handoff.review.reject` transitions.
- Produces: `*.handoff.review.reject.receive` with `review_rejection_received_by` and `review_rejection_received_at` on task, plan, and goal handoffs.

- [ ] **Step 1: Add RED tests for each handoff kind.**

  For task, plan, and goal fixtures, reject a received review, then assert a foreign session cannot record rejection receipt and the original submitter cannot issue a new review request until it has received the rejection. Assert a new review request clears the rejection-receipt phase and starts a new review generation.

- [ ] **Step 2: Run the three lifecycle tests before implementation.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./internal/store -run 'Test(TaskHandoffReviewLifecycle|PlanHandoffReviewLifecycle|GoalHandoffReviewLifecycle)' -count=1 -v
  ```

  Expected: FAIL because review rejection has no receiver fields or receipt operation.

- [ ] **Step 3: Implement receipt state and typed events.**

  Add the columns to the row types and queries, methods named `ReceiveTaskHandoffReviewRejection`, `ReceivePlanHandoffReviewRejection`, and `ReceiveGoalHandoffReviewRejection`, and corresponding daemon/MCP calls. Validate that `received_by` equals the original review requester; leave the task/goal claim open and never fabricate a completion.

- [ ] **Step 4: Update reconciliation event precedence.**

  Extend `watchReconciliationHandoff` and `watchReconciliationHandoffEvent` so an unreceived rejection emits `*.handoff.review.reject`; a received rejection emits a distinct `*.handoff.review.reject.receive`; a later review request replaces both. Use the rejection and receipt timestamps as delivery generations.

- [ ] **Step 5: Verify store, daemon, and MCP authorization.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./internal/store ./internal/daemon ./internal/mcpshim ./cmd/atct -run 'Test(.*HandoffReview.*Reject|.*ReviewReject.*Receive|Watch.*ReviewReject)' -count=1 -v
  ```

  Expected: PASS, with every foreign-session path rejected.

- [ ] **Step 6: Commit rejection receipt support.**

  ```sh
  git add internal/store/task_handoff.go internal/store/goal_handoff.go internal/store/queries/task.sql internal/store/queries/goal_handoff.sql internal/store/*handoff_test.go internal/store/sqlcgen internal/daemon/handler.go internal/daemon/*handoff_test.go internal/mcpshim/tools.go internal/mcpshim/schema_test.go cmd/atct/watch.go cmd/atct/watch_test.go
  git commit -m "feat: require receipt of handoff review rejection"
  ```

### Task 4: Project recurring wakes directly from handoffs and decisions

**Files:**

- Modify: `internal/store/workflow_events.go`
- Modify: `internal/store/wakeup.go`
- Modify: `internal/store/wakeup_test.go`
- Modify: `cmd/atct/watch.go`
- Modify: `cmd/atct/watch_action.go`
- Modify: `cmd/atct/watch_scope.go`
- Modify: `cmd/atct/watch_test.go`
- Modify: `cmd/atct/watch_scope_test.go`
- Create: `internal/store/migrations/0036_remove_legacy_orchestration.sql`
- Modify: `schema.sql`
- Modify: `internal/store/migration_integrity_test.go`
- Modify: `internal/store/schema_parity_test.go`
- Delete: `internal/store/orchestration_scope.go`
- Delete: `internal/store/orchestration_delivery.go`
- Delete: `internal/store/orchestration_blocker.go`
- Delete: `internal/store/orchestration_review_work.go`
- Delete: their focused `*_test.go` files and `internal/store/queries/orchestration_*.sql`

**Interfaces:**

- Consumes: `WorkflowReconciliation{TaskHandoffs, GoalHandoffs, PlanHandoffs, Decisions, TaskCreateHandoffs}`.
- Produces: one typed `watchAgentAction` for the current phase, targeted only at the phase's rightful role.

- [ ] **Step 1: Replace recovery/review-work tests with phase matrix tests.**

  In `cmd/atct/watch_test.go`, table-drive the spec’s wake-up matrix: initial request, review request/receipt, rejection/rejection receipt, plan-accepted task-create progress, unreceived task handoff, and accepted goal review without a human decision. For each state, assert exactly the named action and assert the action disappears after its stopping transition.

- [ ] **Step 2: Run the phase matrix before deleting the old projection.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./cmd/atct -run 'Test(HandoffOnlyReconciliation|Watch.*Recovery|Watch.*ReviewWork)' -count=1 -v
  ```

  Expected: FAIL because reconciliation still reads recovery/blocker/review-work payloads.

- [ ] **Step 3: Remove durable orchestration producers and consumers.**

  Delete production calls that create scopes, delivery leases/receipts, blockers, and review-work rows. Remove their reconciliation JSON fields and HTTP/API endpoints. Keep `monitor_health` only where it already reports health; do not derive a missing-monitor action from it.

- [ ] **Step 3a: Drop legacy schema only after its production users are gone.**

  Add migration `0036_remove_legacy_orchestration.sql` after the direct handoff/decision projection is implemented. Its fixture begins at the 0035 schema with representative retained handoff and decision rows plus legacy orchestration rows; applying 0036 preserves the retained rows and removes exactly `orchestration_scope`, `orchestration_delivery_leases`, `orchestration_delivery_receipts`, `orchestration_review_work`, and `orchestration_blockers` and their indexes. Update `schema.sql` and parity expectations to this final schema.

- [ ] **Step 4: Select phases directly from handoff state.**

  Extend workflow reconciliation with task-create handoffs, then make `reconcileWatchScope` select action lines from the canonical handoff fields in precedence order. Action selection must be read-only: it cannot receive, complete, reassign, or create any handoff. A one-minute reconciliation repeats an unchanged phase intentionally.

- [ ] **Step 5: Verify right-role and stopping behavior.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./internal/store ./cmd/atct -run 'Test(Workflow.*Reconciliation|HandoffOnlyReconciliation|Watch.*Scope|Watch.*Handoff)' -count=1 -v
  ```

  Expected: PASS; no test or production package references an `Orchestration*` type other than retained monitor health.

- [ ] **Step 6: Commit the direct projection.**

  ```sh
  git add internal/store cmd/atct internal/httpapi
  git commit -m "feat: derive monitor wakeups from handoffs"
  ```

### Task 5: Keep transport coalescing process-local

**Files:**

- Modify: `cmd/atct/codex_monitor.go`
- Modify: `cmd/atct/codex_monitor_test.go`
- Modify: `cmd/atct/watch_delivery.go`
- Modify: `cmd/atct/watch_delivery_test.go`

**Interfaces:**

- Consumes: `watchAgentAction{line, eventName, goalID, taskID, handoffID, deliveryGeneration}`.
- Produces: at most one queued or active Codex action for a `(handoff_id, phase)` while the phase is unchanged.

- [ ] **Step 1: Write bridge tests for duplicate and changed phases.**

  Queue the same handoff/phase twice while a turn is active and assert one pending entry. Then queue the same handoff with `review.reject` after a queued `review.request` and assert the old pending entry is removed and the new phase is delivered after the active turn. Construct a fresh bridge and assert it accepts the still-current action again, proving restart reconstruction does not require a receipt.

- [ ] **Step 2: Run the bridge tests before implementation.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./cmd/atct -run 'TestCodexMonitor.*(Coalesce|Phase|Restart)' -count=1 -v
  ```

  Expected: FAIL because the current queue is FIFO plus delivery reservation and has receipt-era policy.

- [ ] **Step 3: Remove durable delivery controllers.**

  Delete lease/reserve/settle calls from `watch_delivery.go`; the action sink passes typed actions directly to Claude/Codex. In `codexMonitorBridge`, key `queue`/`activeAction` coalescing by handoff ID plus phase and discard a pending action only when reconciliation supplies a different current phase for that handoff. Do not persist this set.

- [ ] **Step 4: Prove Claude/Codex parity.**

  Feed the same reconciliation action slice to a collecting Claude sink and an idle Codex fake. Assert identical order for task-create, rejection receipt, and regular task-handoff phases; assert a second minute repeats the same phase only after the prior turn completes.

- [ ] **Step 5: Run focused transport tests.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./cmd/atct -run 'Test(ClaudeAndCodexAgentActionParity|CodexMonitor.*|Watch.*Reconciliation)' -count=1 -v
  ```

  Expected: PASS, with no HTTP requests to orchestration delivery endpoints.

- [ ] **Step 6: Commit local coalescing.**

  ```sh
  git add cmd/atct/codex_monitor.go cmd/atct/codex_monitor_test.go cmd/atct/watch_delivery.go cmd/atct/watch_delivery_test.go cmd/atct/watch.go cmd/atct/watch_test.go
  git commit -m "fix: coalesce monitor wakes in memory"
  ```

### Task 6: Remove stale names and prove the accepted boundary end to end

**Files:**

- Modify: `internal/e2e/full_flow_test.go`
- Modify: `internal/daemon/pending_response_test.go`
- Modify: `internal/daemon/server_test.go`
- Modify: `internal/httpapi/*_test.go` as required by `CreateTasks` rename
- Modify: `tests/wrapper_test.bash`
- Modify: `skills/atct/SKILL.md`
- Modify: `doc/execution-flow.md`
- Modify: `doc/specs/2026-09-09-goal-254-handoff-only-lifecycle.md` only if implementation reveals a spec contradiction

**Interfaces:**

- Consumes: all lifecycle operations introduced above.
- Produces: a full flow from pre-plan `task.create` through plan approval, task-create receipt/create/delegation/complete, executor receipt, and recurring wake recovery.

- [ ] **Step 1: Replace all production and test references to the retired creation names.**

  Use `rg -n 'task\.declare|DeclareTasks' --glob '!doc/specs/2026-09-09-goal-254-handoff-only-lifecycle.md'` and replace each code, test, skill, and flow reference with `task.create`/`CreateTasks`. The only allowed remaining references are migration assertions that prove the old RPC has no handler.

- [ ] **Step 2: Add the end-to-end lifecycle regression.**

  In `internal/e2e/full_flow_test.go`, have a pre-plan caller use `task.create` for design work; submit/accept a plan; assert direct creation now fails; receive the generated task-create handoff; create tasks idempotently; request each task handoff; complete the task-create handoff; then receive one executor task handoff. Assert no `orchestration_*` schema row or delivery receipt participates.

- [ ] **Step 3: Run static cleanup checks.**

  Run:

  ```sh
  ! rg -n 'task\.declare|DeclareTasks' --glob '!doc/specs/2026-09-09-goal-254-handoff-only-lifecycle.md'
  ! rg -n 'orchestration_(scope|delivery|review_work|blocker)' internal cmd schema.sql --glob '!**/*_test.go'
  script/schema-check.sh
  ```

  Expected: all commands succeed; no removed production API or schema reference remains.

- [ ] **Step 4: Run affected package suites and the full suite.**

  Run:

  ```sh
  GOCACHE=/private/tmp/goal254-go-cache go test ./internal/store ./internal/daemon ./internal/mcpshim ./internal/httpapi ./internal/e2e ./cmd/atct -count=1
  GOCACHE=/private/tmp/goal254-go-cache go test ./... -count=1
  git diff --check
  git status --short
  ```

  Expected: PASS; status contains only the intended Goal 254 files before commit.

- [ ] **Step 5: Commit final compatibility cleanup and verification coverage.**

  ```sh
  git add internal/e2e internal/daemon internal/httpapi internal/mcpshim tests/wrapper_test.bash skills/atct/SKILL.md doc/execution-flow.md
  git commit -m "test: cover handoff-only lifecycle"
  ```

---

## Plan self-review

- Spec coverage: Tasks 1–3 cover migration, task-create handoff, `task.create` unification, and rejection receipt; Tasks 4–5 cover direct recurring projection and process-local coalescing; Task 6 proves removal and end-to-end behavior.
- Placeholder scan: no TODO/TBD steps; each task names paths, transition boundary, and verification command.
- Interface consistency: `CompletePlanHandoff` produces `TaskCreateHandoff`; only its received holder calls `CreateTasks`; every created task must have a requested `TaskHandoff` before task-create completion.
