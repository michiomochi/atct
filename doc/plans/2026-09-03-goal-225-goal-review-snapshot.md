# Goal 225 request-time goal-review report implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use ATCT task handoffs task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a named human goal review approve the six completion-report fields persisted when that review was requested, without duplicating the report in GoalDetail.

**Architecture:** Reuse the existing `goals` completion-report columns. A store transaction validates and writes them while creating `KindGoalReview`; an open review blocks replacement; a new no-input commander finalizer marks the goal done from those stored fields. GoalDetail renders those fields only inside an active open review card and otherwise keeps its existing completion report.

**Tech Stack:** Go, SQLite/sqlc, daemon RPC, MCP shim, React/TypeScript, Vitest and Go tests.

## Global Constraints

- Do not add a snapshot table, decision report payload, migration, or legacy backfill.
- `goal.review.request` requires all six existing completion-report fields and stores them transactionally with the new review.
- `goal.review.complete` accepts only `goal_id`; it never accepts or writes report fields.
- Do not permit report replacement while `KindGoalReview` is open.
- Rejected-review report values are not retained per decision; a later request may replace the goal fields.
- Preserve legacy `goal.complete` / `KindCompletion`, generic decision approve/reject, handoff ordering, and proposed-goal UI.

---

### Task 1: Persist request-time report and no-input finalization

**Files:**

- Modify: `internal/store/goal.go`, `internal/store/queries/goal.sql`, regenerated `internal/store/sqlcgen/goal.sql.go`.
- Test: `internal/store/goal_complete_test.go`, `internal/store/goal_handoff_test.go`.

**Interfaces:**

- `RequestGoalReview(ctx, goalID, commanderID, report domain.CompletionReport) (domain.Decision, error)` validates, updates the existing goal report, and creates `KindGoalReview` in one transaction.
- `FinalizeGoalReview(ctx, goalID, commanderID) (domain.Goal, error)` requires an approved review and marks the goal done without a report parameter.

- [ ] **Step 1: Write failing store tests**

Assert each missing field rejects the request without changing the goal; a successful request persists all six fields; a second report update during an open review fails; finalization before approval fails; and finalization marks done with the six fields persisted at request time.

- [ ] **Step 2: Implement the transactional store operations**

Keep `requireLatestGoalHandoffForReview` unchanged. In one transaction validate the report, reject an open decision, update the existing goal fields, create `KindGoalReview`, and publish its workflow event. Implement no-input finalization that requires an approved review, checks the report is still valid, and only changes status/updated time.

- [ ] **Step 3: Cover rejection and legacy isolation**

Assert rejection keeps the goal active, does not create per-decision history, and a later valid re-handoff/request may replace the goal fields. Assert `CompleteGoalWithReport` still creates only `KindCompletion`.

- [ ] **Step 4: Verify and commit**

Run: `GOCACHE=/tmp/atct-go-cache-goal-review-report go test ./internal/store -run 'Test.*(GoalReview|GoalCompletion)' -count=1`

Expected: PASS.

Run: `git diff --check`

Expected: no output and exit 0.

Commit only the listed store/query/test paths.

### Task 2: Expose report-bearing request and no-input finalizer

**Files:**

- Modify: `internal/daemon/handler.go`, `internal/daemon/goal_handoff_test.go`, `internal/daemon/goal_complete_guard_test.go`.
- Modify: `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go`.

**Interfaces:**

- `goal.review.request` and `atct_goal_review_request` require six report fields.
- `goal.review.complete` and `atct_goal_review_complete` accept `goal_id` only.
- `goal.complete` retains its existing six-field legacy `KindCompletion` schema.

- [ ] **Step 1: Write failing RPC and MCP schema tests**

Assert the commander-only request rejects missing fields and persists the supplied report; only the commander may invoke no-input finalization after approval; caller-supplied final report fields have no route through `goal.review.complete`; and `goal.complete` remains unchanged.

- [ ] **Step 2: Add bounded adapters**

Pass the request report to `RequestGoalReview`, route no-input finalization to `FinalizeGoalReview`, and keep `authorizeCommander` plus all handoff-order checks. Do not add a snapshot response field or modify HTTP detail JSON.

- [ ] **Step 3: Verify and commit**

Run: `GOCACHE=/tmp/atct-go-cache-goal-review-report go test ./internal/daemon ./internal/mcpshim -run 'Test.*(GoalReview|GoalComplete)' -count=1`

Expected: PASS.

Run: `git diff --check`

Expected: no output and exit 0.

Commit only the listed daemon/MCP/test paths.

### Task 3: Render one pre-approval report

**Files:**

- Modify: `web/src/components/GoalDetail.tsx`, `web/src/components/GoalDetail.test.tsx`.
- Modify: `web/src/i18n/en.ts`, `web/src/i18n/ja.ts`, `web/src/i18n/i18n.test.ts` only if a new label is necessary.

**Interfaces:**

- `GoalReview` receives the existing `Goal` report fields from its `GoalResponse`.
- The ordinary `CompletionReport` is suppressed only when `goal.status === "active"` and an open `goal_review` exists.

- [ ] **Step 1: Write failing GoalDetail tests**

Supply an active goal with an open `goal_review` and all six goal fields. Assert the review card shows every labeled value and `completion-report` is absent. Supply a normal completion or proposed-goal response without open `goal_review`; assert its current report/card remains visible.

- [ ] **Step 2: Render the existing fields once**

Pass the goal report into `GoalReview`; display it before approve/reject. Pass the open-review state to `CompletionReport` and suppress that section only in the active/open case. Do not add API types, editable fields, or duplicate report markup.

- [ ] **Step 3: Verify and commit**

Run: `npm --prefix web test -- GoalDetail.test.tsx i18n.test.ts`

Expected: PASS.

Run: `npm --prefix web run typecheck`

Expected: PASS.

Run: `git diff --check`

Expected: no output and exit 0.

Commit only the listed web paths.
