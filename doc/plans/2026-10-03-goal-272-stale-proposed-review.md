# Stale Proposed Goal Review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use ATCT executor handoffs task-by-task with the superpowers test/review workflow. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 14-day automatic withdrawal with a commander checkpoint: `atct_goal_list` lists proposed goals idle for 7+ days, the commander checks them against main at two fixed points and withdraws the ones that are obsolete. No record of "confirmed" is kept.

**Spec:** `doc/specs/2026-10-03-goal-272-stale-proposed-review.md`

**Architecture:** The daemon stops changing goals. `goal.list` adds `review_due_goals`, filtered from the `ListGoals` result the handler already has. Closing a review reuses the commander-only `atct_goal_withdraw`, now valid for any proposed goal. Everything built for the previous design (confirmation table, `atct_goal_confirm`, wakeup, watch, pending) is removed.

**Tech Stack:** Go, SQLite/sqlc, MCP shim, skills docs.

## Current state of the branch (read this first)

HEAD already contains the previous design: migration `0048_goal_confirmations.sql`, `goal_confirmations` in `schema.sql`, queries `InsertGoalConfirmation` / `ListLatestGoalConfirmations`, `Store.ConfirmProposedGoal`, `WakeupState.ReviewDueGoals` / `ReviewDueAt`, `EventWakeupGoalReviewDue`, the `wakeup.goal_review_due` emission in `internal/daemon/wakeup.go`, `goal.confirm` + `atct_goal_confirm`, watch scope/rendering, a pending section, and a `web/src/lib/events.test.ts` entry. Use `git diff main -- <file>` to see exactly which hunks to drop. Keep from that work only: removal of `ReconcileStaleGoalApprovals` and its helpers, `WithdrawProposedGoal` = `status = 'proposed'` only, the `atct_goal_withdraw` description saying "active or proposed", the proposed-goal withdrawal tests (store and daemon).

## Global Constraints

- The daemon must not withdraw or otherwise change any goal or decision on its own.
- No new table, migration, MCP tool, wakeup, watch event, or pending section. After this work `git diff main` must contain no `goal_confirmations`, `atct_goal_confirm`, `goal.confirm`, `goal_review_due`, `ConfirmProposedGoal`, or `ReviewDue` outside the new `review_due_goals` field in `goal.list`.
- Do not touch active-goal withdrawal behavior or the commander-only authorization of `goal.withdraw`.
- After editing `internal/store/queries/*.sql`, run `go tool sqlc generate` and commit the regenerated `internal/store/sqlcgen`. Do not hand-edit generated code.
- Executor verification is limited to the focused package tests named in each task. The subcommander runs the full suite, `./script/schema-check.sh`, and the wrapper test after review.

---

### Task 1: Remove the confirmation, wakeup, watch and pending machinery

**Files:**
- Delete: `internal/store/migrations/0048_goal_confirmations.sql`, `internal/store/goal_review_due_test.go` (keep its proposed-goal `WithdrawActiveGoal` test by moving that case into `internal/store/goal_withdraw_test.go`)
- Modify: `schema.sql`, `internal/store/queries/goal.sql` (regenerate sqlc), `internal/store/goal.go`, `internal/store/wakeup.go`
- Modify: `internal/daemon/handler.go`, `internal/daemon/wakeup.go`, `internal/daemon/wakeup_test.go`, `internal/daemon/goal_withdraw_test.go`
- Modify: `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go`
- Modify: `cmd/atct/watch.go`, `cmd/atct/watch_scope.go`, `cmd/atct/pending.go` and their `_test.go` files
- Modify: `web/src/lib/events.test.ts`, `skills/commander/SKILL.md` (remove the old section; Task 2 writes the new one)

- [ ] **Step 1:** Remove everything listed under "Current state" except the parts to keep. Keep a daemon test that a 30-day-old proposed goal and its approval are untouched by `runMaintenanceWith`, and keep the commander-withdraws-proposed-goal tests (store: goal with tasks and `creator = 'human'`; daemon: commander allowed, non-commander refused).
- [ ] **Step 2:** `go tool sqlc generate`.
- [ ] **Step 3:** Run `go build ./...`, `go vet ./internal/... ./cmd/...`, `go test ./internal/store ./internal/daemon ./internal/mcpshim ./cmd/atct -count=1`. Expected: PASS.
- [ ] **Step 4:** `git diff main --stat` must show no migration file and no `goal_confirmations` anywhere (`git grep -n "goal_confirmations\|atct_goal_confirm\|goal\.confirm\|goal_review_due\|ReviewDue\|ConfirmProposedGoal"` prints nothing).

### Task 2: `review_due_goals` in `goal.list`, and the checkpoint procedure

**Depends on:** Task 1 (same worker).

**Files:**
- Modify: `internal/daemon/handler.go` (and a test, e.g. in `internal/daemon/goal_list_test.go` or the existing goal.list test file)
- Modify: `internal/mcpshim/tools.go` (the `atct_goal_list` description)
- Modify: `skills/commander/SKILL.md`, `skills/start/SKILL.md`

- [ ] **Step 1: Write failing tests.** `goal.list` returns `review_due_goals` containing a proposed goal whose `updated_at` is 8 days old (both `creator` agent and human) and not a 6-day-old proposed goal, an active goal, or a done goal; each item has `id`, `title` (same `summaryLine(goal.Content)` as the other goal rows) and `updated_at`; the array is `[]` (not null) when nothing is due. Backdate `updated_at` with SQL as the other tests do.
- [ ] **Step 2: Implement.** In the `goal.list` handler, build the list from the `goals` slice it already loads, with `const goalReviewDueAfter = 7 * 24 * time.Hour` in the daemon package, and add `"review_due_goals"` to the `data` map. No store or query change.
- [ ] **Step 3: Docs.** `atct_goal_list` description: mention `review_due_goals`. `skills/commander/SKILL.md`: add the section "Proposed goals due for review": the two checkpoints (session start; right after `atct_goal_review_complete`), the three withdrawal grounds (already on main / replaced by another goal / premise gone), "withdraw with the evidence as the reason via `atct_goal_withdraw`; leave the others untouched, nothing is recorded", and place it at the end of the file as a top-level `##` section (not inside `### Session keys`). `skills/start/SKILL.md` loop step 1 (Look): one sentence pointing to that section when `review_due_goals` is non-empty.
- [ ] **Step 4: Run** `go build ./...`, `go vet ./internal/... ./cmd/...`, `go test ./internal/daemon ./internal/mcpshim -count=1`. Expected: PASS.
