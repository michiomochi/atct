# Goal 225 immutable goal-review snapshot implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Restore six-field human approval information by storing an immutable report snapshot with every `KindGoalReview`.

**Architecture:** A new snapshot table is created atomically with a commander-authorized goal review request. GoalDetail receives and displays the decision's snapshot; human approval applies the decision, while a new commander-only finalizer copies the stored snapshot into `goals`. Legacy `KindCompletion` retains its existing report-before-approval flow.

**Tech stack:** Go, SQLite migrations/sqlc, daemon RPC, MCP shim, HTTP JSON, React/TypeScript, Vitest and Go tests.

## Global constraints

- Do not alter goal-handoff ordering, reviewer binding/authorization, generic decision approve/reject routes, or explicit commander reissue after rejection.
- Do not use `goal_handoffs.complete_report` as a report source.
- `goal.review.complete` must consume a stored approved snapshot and accept no report fields.
- `goal.complete` remains the legacy `KindCompletion` adapter with six report fields; remove its `HasGoalReview` dispatch so historical goal-review rows cannot alter that contract.
- No implementation/delegation starts until commander accepts this plan and reviewer-binding recovery permits canonical receipt.

## Work unit 1: Persist and validate immutable snapshots

**Files:**

- Create: `internal/store/migrations/0026_goal_review_snapshots.sql` (renumber only if another accepted migration lands first).
- Modify: `internal/store/queries/decision.sql`, `internal/store/queries/goal.sql`, regenerated `internal/store/sqlcgen/*`, `internal/domain/model.go`, `internal/store/decision.go`, and `internal/store/goal.go`.
- Test: `internal/store/goal_complete_test.go`, `internal/store/goal_handoff_test.go`, `internal/store/decision_test.go`, and `internal/store/migrations_test.go`.

**Interfaces:**

- `RequestGoalReview(ctx, goalID, commanderID, report domain.CompletionReport) (domain.Decision, error)` atomically inserts `KindGoalReview` and `GoalReviewSnapshot`.
- `FinalizeGoalReview(ctx, goalID, commanderID) (domain.Goal, error)` requires the latest approved review and copies only its stored snapshot.

- [ ] **Step 1: Write failing store tests**

Assert a request without any one of the six fields fails; a successful request stores all six fields linked to its decision; detail/list hydration returns that snapshot only for its matching `goal_review`; direct finalization before approval fails; and finalization copies the snapshot rather than a caller-supplied report.

- [ ] **Step 2: Add the forward migration and sqlc queries**

Create the snapshot table with `decision_id` primary key, `goal_id`, six non-null text columns, `created_by`, and `created_at`. In this same migration, backfill only open reviews whose six canonical goal report columns are all non-empty, and withdraw every other open legacy review with a fixed migration reason. Add insert/select queries plus a transaction-safe query for the latest approved goal-review snapshot.

- [ ] **Step 3: Implement request/finalize store operations**

Keep `requireLatestGoalHandoffForReview` unchanged. After it passes, validate the report and insert the decision, snapshot, and decision-created workflow event in one transaction (do not compose `AskDecision`'s separate transaction). Make `FinalizeGoalReview` reject absent/unapproved snapshots and write `goals` from the selected row only. Make generic goal-review approval reject a review without its snapshot.

- [ ] **Step 4: Cover rejection/reissue and legacy isolation**

Assert rejected snapshot/handoff values remain unchanged; a replacement handoff creates a different snapshot; legacy closed reviews remain history; unbackfillable open reviews are withdrawn; and `CompleteGoalWithReport` still produces only `KindCompletion` even when historical `KindGoalReview` rows exist.

- [ ] **Step 5: Verify and commit**

Run `GOCACHE=/tmp/atct-go-cache-goal-review-snapshot go test ./internal/store -run 'Test.*(GoalReview|GoalCompletion|Snapshot)' -count=1` and `git diff --check`; commit only the listed store/migration paths.

## Work unit 2: Expose the snapshot through daemon, MCP, and HTTP

**Files:**

- Modify: `internal/daemon/handler.go`, `internal/daemon/goal_handoff_test.go`, `internal/daemon/goal_complete_guard_test.go`.
- Modify: `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go`.
- Modify: `internal/httpapi/server.go`, `internal/httpapi/server_test.go`.

**Interfaces:**

- `goal.review.request` and `atct_goal_review_request` require six report fields.
- `goal.review.complete` and `atct_goal_review_complete` take `goal_id` only.
- Goal-detail JSON includes read-only `goal_review_snapshot` for a goal-review decision.

- [ ] **Step 1: Write failing RPC/schema/HTTP tests**

Assert commander-only request requires all six fields, the response/detail includes the exact snapshot, only commander may call no-report finalization after approval, and `goal.complete` always retains its six-field `KindCompletion` schema (including after a historical review exists).

- [ ] **Step 2: Add adapters without widening authority**

Pass report fields only to snapshot request; add an explicit `goal.review.complete` daemon method and MCP tool that route no-report finalization to `FinalizeGoalReview`; remove the `HasGoalReview` branch from `goal.complete`; keep `authorizeCommander` and all handoff ordering checks intact.

- [ ] **Step 3: Verify and commit**

Run `GOCACHE=/tmp/atct-go-cache-goal-review-snapshot go test ./internal/daemon ./internal/mcpshim ./internal/httpapi -run 'Test.*(GoalReview|GoalComplete|Snapshot)' -count=1` and `git diff --check`; commit only these API/test paths.

## Work unit 3: Render snapshot before the human decision

**Files:**

- Modify: `web/src/lib/api.ts`, `web/src/components/GoalDetail.tsx`, `web/src/components/GoalDetail.test.tsx`.
- Modify: `web/src/i18n/en.ts`, `web/src/i18n/ja.ts`, `web/src/i18n/i18n.test.ts` if new labels need catalog coverage.

- [ ] **Step 1: Write failing GoalDetail tests**

Supply an open `goal_review` with six snapshot values. Assert every labeled value is visible before approve/reject and, when a backfilled active goal also has the same six canonical completion fields, `completion-report` is absent so the snapshot review card is the sole pre-approval report. Assert request endpoints still use only the decision ID/rejection reason, and completion/proposed-goal cards remain unaffected when no open `goal_review` exists.

- [ ] **Step 2: Extend typed API and render read-only report**

Add an optional typed `goal_review_snapshot` to `Decision`; render it only for `goal_review`, using the existing completion-report labels and accessibility structure. Pass the open goal-review state into the legacy completion-report section so it is suppressed only for an active open `goal_review`; do not add editable report inputs to the human form.

- [ ] **Step 3: Verify and commit**

Run `npm --prefix web test -- GoalDetail.test.tsx i18n.test.ts`, `npm --prefix web run typecheck`, and `git diff --check`; commit only the listed web paths.
