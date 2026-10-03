# Stale Proposed Goal Review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use ATCT executor handoffs task-by-task with the superpowers test/review workflow. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 14-day automatic withdrawal with a 7-day "review due" signal to the commander, a way to confirm a goal is still wanted, and a commander withdrawal path for any proposed goal.

**Spec:** `doc/specs/2026-10-03-goal-272-stale-proposed-review.md`

**Architecture:** The daemon stops changing goals. `EvaluateWakeup` lists proposed goals whose last activity (`goals.updated_at` or the latest `goal_confirmations.confirmed_at`) is 7+ days old; the daemon turns each into a `wakeup.goal_review_due` event for the commander's watch and `atct pending` lists them. The commander closes the review with the existing `atct_goal_withdraw` (now valid for any proposed goal) or a new `atct_goal_confirm`.

**Tech Stack:** Go, SQLite/sqlc, existing wakeup/watch/MCP plumbing.

## Global Constraints

- The daemon must not withdraw or otherwise change any goal or decision on its own.
- No new decision kind, UI, or config. One migration, new table only (`0046_goal_confirmations.sql`); do not alter `goals`.
- Do not touch active-goal withdrawal behavior or the commander-only authorization of `goal.withdraw`.
- `go tool sqlc generate` must be run after editing `internal/store/queries/*.sql`; commit the regenerated `internal/store/sqlcgen`.
- Do not hand-edit generated code.
- Executor verification is limited to the focused package tests named in each task. The subcommander runs the full suite, `./script/schema-check.sh`, and the wrapper test after review.

---

### Task 1: Store — remove auto-withdrawal, open proposed withdrawal, add confirmations and the due list

**Files:**
- Create: `internal/store/migrations/0046_goal_confirmations.sql`
- Modify: `internal/store/queries/goal.sql` (and regenerate `internal/store/sqlcgen/goal.sql.go`)
- Modify: `internal/store/goal.go`
- Modify: `internal/store/wakeup.go`
- Modify/Delete: `internal/store/goal_stale_approval_test.go`, `internal/store/goal_withdraw_test.go`
- Create/Modify: tests for confirmation and the due list (e.g. `internal/store/goal_review_due_test.go`)

**Produces:**
- `Store.ConfirmProposedGoal(ctx, goalID int64, note string) error` — proposed goals only, non-empty note, inserts a `goal_confirmations` row.
- `WakeupState.ReviewDueGoals []domain.Goal` plus `store.EventWakeupGoalReviewDue = "wakeup.goal_review_due"` and a store-level way to read each goal's due time (e.g. return the due time with the goal, or export a helper the daemon calls), so the daemon can pass the due time as the wakeup start time.
- `WithdrawActiveGoal` accepts any `proposed` goal (condition `status = 'proposed'` only) and keeps active behavior.

- [ ] **Step 1: Write failing tests**
  - a proposed goal with `updated_at` 8 days ago is in `ReviewDueGoals`; 6 days ago is not; creator `human` and `agent` both count; an active goal never does.
  - after `ConfirmProposedGoal`, the goal leaves `ReviewDueGoals`; with the confirmation backdated 8 days it returns.
  - `ConfirmProposedGoal` rejects an empty/blank note, a non-proposed goal, and an unknown goal.
  - `WithdrawActiveGoal` on a proposed goal that has tasks/handoffs/extra decisions and creator `human` drops it, drops its open tasks, and withdraws its open `goal_approval` with the reason. Existing active-goal withdrawal tests still pass unchanged.
- [ ] **Step 2: Run them, confirm they fail.**
- [ ] **Step 3: Implement.**
  - Delete `ReconcileStaleGoalApprovals`, `staleGoalApprovalAfter`, `staleGoalApprovalReason`, `ErrGoalHasWork`, and the `HasGoalWork` / `ListOpenAgentGoalApprovals` queries. Simplify `WithdrawProposedGoal` to `WHERE id = ? AND status = 'proposed'` and remove the `HasGoalWork` branch in `WithdrawActiveGoal`.
  - Add the migration and queries for `goal_confirmations` (`id`, `goal_id` FK, `note`, `confirmed_at` using the repo's fixed-width timestamp format from `internal/store/timestamp.go`).
  - Add `goalReviewDueAfter = 7 * 24 * time.Hour` and compute `ReviewDueGoals` in `EvaluateWakeup`, preferably with one grouped query for the latest confirmation per proposed goal.
  - Delete `internal/store/goal_stale_approval_test.go` cases that tested auto-withdrawal; keep nothing that asserts the 14-day behavior.
- [ ] **Step 4: Regenerate sqlc and run** `go tool sqlc generate` then `go test ./internal/store -count=1`. Expected: PASS.

### Task 2: Daemon, MCP, watch and pending

**Depends on:** Task 1 (same worker).

**Files:**
- Modify: `internal/daemon/wakeup.go`, `internal/daemon/wakeup_test.go`
- Modify: `internal/daemon/handler.go` (new `goal.confirm`), `internal/daemon/goal_withdraw_test.go` (proposed withdrawal), add a handler test for `goal.confirm`
- Modify: `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go` (new `atct_goal_confirm`; update the `atct_goal_withdraw` description: it now also abandons a proposed goal)
- Modify: `cmd/atct/watch_scope.go`, `cmd/atct/watch.go`, `cmd/atct/pending.go` and their tests
- Modify: `skills/commander/SKILL.md` (a short section: on `wakeup.goal_review_due`, check the goal against current main, then `atct_goal_withdraw` with the reason or `atct_goal_confirm` with the note)

**Interfaces:**
- Consumes: Task 1's `ReviewDueGoals`, `EventWakeupGoalReviewDue`, `ConfirmProposedGoal`.

- [ ] **Step 1: Write failing tests**
  - `runMaintenanceWith` no longer changes any proposed goal or approval, even one 30 days old (replaces the old reconcile tests).
  - `evaluateWith` emits `wakeup.goal_review_due` with the goal id once for a due goal, and not for a 6-day goal; confirming (state cleared) lets a later due time emit again.
  - `goal.confirm` is commander-only (a subcommander or executor is refused), requires a non-empty note, and refuses a non-proposed goal. `goal.withdraw` of a proposed goal succeeds for the commander.
  - MCP schema lists `atct_goal_confirm` with `goal_id` and `note`.
  - the project-scope (commander) watch delivers `wakeup.goal_review_due`; a goal-scoped watch does not. The rendered line names the goal and both closing tools.
  - `atct pending` lists review-due goals.
- [ ] **Step 2: Run them, confirm they fail.**
- [ ] **Step 3: Implement** in the files above. Remove the `ReconcileStaleGoalApprovals` call and `reconcileErr` plumbing from `runMaintenanceWith` (restore the plain `evaluateErr` handling). Emit the wakeup through the existing `recordWakeupEvent` helper with the due time as `startedAt` and `after = 0`.
- [ ] **Step 4: Run** `go build ./...`, `go vet ./internal/... ./cmd/...`, `go test ./internal/daemon ./internal/mcpshim ./cmd/atct -count=1`. Expected: PASS.
