# Retire Legacy Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make named goal-review the only goal-completion authority, migrate open legacy decisions to withdrawn, and fail closed at the retired public entry points.

**Architecture:** Treat the Go lifecycle and its live Go consumers as one compile unit: remove the legacy Store/domain authority and update daemon, HTTP, wakeup, CLI, and E2E callers together. Add a forward-only SQLite migration for existing open rows. Keep old RPC/MCP names only as stable diagnostics, and remove their actionable UI/documentation.

**Tech Stack:** Go, SQLite embedded migrations, sqlc, MCP shim, React/TypeScript, Vitest.

## Global Constraints

- The worktree is already rebased onto current `main` / `v0.63.11` at `5c726f9`. Do not merge, push, or run daemon operations.
- Use migration suffix `0045_retire_legacy_completion.sql`; main's current integrated suffix is `0044_fixed_width_timestamps.sql`.
- Do not edit `schema.sql` for the data-only migration. Run `script/schema-check.sh` after query/migration changes and regenerate sqlc output with the repository command.
- Preserve the named `goal_review` request → human approval → merge → finalize lifecycle and same-handoff rejection recovery.
- Keep `CompletionReport` and report fields required by named goal-review. Remove only the legacy completion authority and its consumers.
- Keep `atct_goal_complete` and RPC `goal.complete` registered with their existing input shape, but make them side-effect-free migration diagnostics. Do not create a replacement legacy adapter.
- A worker may run only the package-scoped verification commands listed in its task. The subcommander runs the final repository-wide verification after all task reviews.

---

### Task 1: Remove the Go legacy authority and all live Go consumers

**Files:**
- Modify: `internal/domain/status.go`
- Modify: `internal/store/goal.go`
- Modify: `internal/store/queries/goal.sql`
- Regenerate: `internal/store/sqlcgen/goal.sql.go`
- Modify: `internal/daemon/handler.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/store/wakeup.go`
- Modify: `cmd/atct/pending.go`
- Modify: `cmd/atct/watch.go`
- Modify: `internal/mcpshim/tools.go`
- Modify: `internal/store/goal_complete_test.go`
- Modify: `internal/store/goal_handoff_test.go`
- Modify: `internal/store/goal_test.go`
- Modify: `internal/store/wakeup_test.go`
- Modify: `internal/store/completion_template_test.go`
- Modify: `internal/daemon/goal_complete_guard_test.go`
- Modify: `internal/daemon/handler_test.go`
- Modify: `internal/daemon/goal_handoff_test.go`
- Modify: `internal/daemon/pending_response_test.go`
- Modify: `internal/httpapi/server_test.go`
- Modify: `internal/mcpshim/schema_test.go`
- Modify: `cmd/atct/pending_test.go`
- Modify: `cmd/atct/watch_test.go`
- Modify: `internal/e2e/flow_test.go`
- Modify: `internal/e2e/full_flow_test.go`

**Interfaces:**
- Retire `KindCompletion`, `CompleteGoal*`, `FinalizeGoalWithReport`, `ApproveCompletion`, `RejectCompletion`, and completion-only sqlc queries.
- Keep the public `goal.complete`/`atct_goal_complete` transport shape, but return a stable diagnostic before goal/decision mutation.
- Require nonzero session identity and reviewer-owned handoffs in both task and goal handoff completion paths.

- [ ] **Step 1: Add failing regression coverage.** Assert that the retired goal-complete RPC/MCP calls return a diagnostic containing both `atct_goal_review_request` and `atct_goal_review_complete` without changing goal or decisions. Assert that session-zero task/goal handoff completion returns the identity diagnostic without changing the handoff. Rewrite Go E2E fixtures to exercise named goal-review request/approve/reject/resubmit paths instead of `KindCompletion`.
- [ ] **Step 2: Run focused tests to verify the old behavior fails the new contract.**

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./internal/store ./internal/daemon ./internal/httpapi ./internal/mcpshim ./internal/e2e ./cmd/atct -run 'Test.*(GoalComplete|Handoff|Completion|Pending|Wakeup|FullFlow|GoalReview)' -count=1
  ```

- [ ] **Step 3: Implement the compile-unit deletion.** Remove the legacy domain constant, Store methods, completion-only queries, and generated functions. Regenerate sqlc output. Make `goal.complete` fail closed without Store lookup/mutation; retain the MCP registration and input schema. Remove HTTP completion approve/reject branches, completion-specific wakeup checks, and old pending/watch guidance. Make dispatch and helpers reject session zero before any unowned Store fallback. Preserve named goal-review and nonzero reviewer paths.
- [ ] **Step 4: Run the focused tests again.** The command in Step 2 must pass, including named goal-review and nonzero handoff completion coverage; no Go source or test may reference the removed `KindCompletion` authority.
- [ ] **Step 5: Commit the compile-unit change.**

  ```sh
  git add internal/domain/status.go internal/store internal/daemon internal/httpapi internal/mcpshim/tools.go cmd/atct internal/e2e
  git commit -m "feat: retire legacy completion authority"
  ```

### Task 2: Withdraw open legacy decisions with migration 0045

**Files:**
- Create: `internal/store/migrations/0045_retire_legacy_completion.sql`
- Create: `internal/store/legacy_completion_migration_test.go`
- Verify: `schema.sql` remains unchanged
- Verify: `internal/store/sqlcgen/` remains generated and clean after `script/schema-check.sh`

**Interfaces:**
- Converts only pre-existing `decisions(kind = 'completion', status = 'open')` rows to withdrawn.
- Keeps closed legacy history, goal status, goal report, and non-completion decisions unchanged.

- [ ] **Step 1: Add failing migration coverage.** Build a fixture containing open, answered, and applied legacy completion rows plus a goal report. Assert the expected post-migration state, including the exact reason `legacy completion retired; request a named goal review` and a populated `answered_at` only for the open row.
- [ ] **Step 2: Run the migration test to verify failure.**

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./internal/store -run 'Test.*LegacyCompletionMigration' -count=1
  ```

- [ ] **Step 3: Add the forward-only migration.** Use one `UPDATE` with `WHERE kind = 'completion' AND status = 'open'`, set `status = 'withdrawn'`, preserve an existing `answered_at` with `COALESCE`, and do not rebuild the decisions table or modify `schema.sql`.
- [ ] **Step 4: Run migration and schema verification.**

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./internal/store -run 'Test.*(LegacyCompletionMigration|Migration)' -count=1
  ./script/schema-check.sh
  ```

- [ ] **Step 5: Commit the migration.**

  ```sh
  git add internal/store/migrations/0045_retire_legacy_completion.sql internal/store/legacy_completion_migration_test.go
  git commit -m "feat: withdraw open legacy completions"
  ```

### Task 3: Remove the actionable Web UI and update MCP user guidance

**Files:**
- Modify: `web/src/lib/ui.ts`
- Modify: `web/src/lib/ui.test.ts`
- Modify: `web/src/lib/api.test.ts`
- Modify: `web/src/components/GoalDetail.tsx`
- Modify: `web/src/components/GoalDetail.test.tsx`
- Modify: `web/src/i18n/en.ts`
- Modify: `web/src/i18n/ja.ts`
- Modify: `web/src/i18n/i18n.test.ts`
- Modify: `internal/mcpshim/instructions.go`
- Create: `doc/migrations/2026-09-19-retire-legacy-completion.md`

**Interfaces:**
- Historical completion decisions remain inspectable but never receive an approve/reject card.
- Named `goal_review` decisions remain actionable.
- MCP instructions and migration guide name `atct_goal_review_request` followed by `atct_goal_review_complete`.

- [ ] **Step 1: Add failing UI/instruction assertions.** Replace completion-card expectations with read-only historical decision assertions, retain positive goal-review controls, and assert the current MCP instruction no longer presents `atct_goal_complete` as the normal completion operation.
- [ ] **Step 2: Run focused UI and MCP tests.**

  ```sh
  pnpm --dir web test -- --run src/lib/ui.test.ts src/components/GoalDetail.test.tsx src/i18n/i18n.test.ts
  GOCACHE=/private/tmp/atct-go-cache go test ./internal/mcpshim -run 'Test.*(Instruction|Schema)' -count=1
  ```

- [ ] **Step 3: Delete the completion action surface.** Remove `findOpenCompletion`, completion-specific approval state/component, obsolete translation keys, and completion-only test fixtures. Keep generic historical rendering and `CompletionReport` rendering used by named review. Update the MCP instructions and write the migration guide with the old calls, stable diagnostics, migration behavior, and replacement sequence.
- [ ] **Step 4: Run the focused tests again.** The commands in Step 2 must pass.
- [ ] **Step 5: Commit the UI and guidance change.**

  ```sh
  git add web internal/mcpshim/instructions.go doc/migrations/2026-09-19-retire-legacy-completion.md
  git commit -m "docs: guide legacy completion migration"
  ```

### Task 4: Update maintained agent instructions and perform final verification

**Files:**
- Modify: `skills/atct/SKILL.md`
- Modify: `skills/commander/SKILL.md`
- Modify: `skills/start/SKILL.md`
- Verify: `skills/subcommander/SKILL.md` keeps `atct_goal_complete` only as a forbidden executor operation
- Modify: `doc/investigations/2026-09-17-goal-282-legacy-completion-retirement.md`
- Modify: `doc/specs/2026-09-17-goal-282-retire-legacy-completion.md`
- Modify: `doc/plans/2026-09-17-goal-282-retire-legacy-completion.md`

**Interfaces:**
- Maintained instructions describe only named goal-review as the completion lifecycle.
- Historical investigation, current spec, and current plan record the widened deletion and migration 0045.

- [ ] **Step 1: Replace stale completion instructions.** Change current operational guidance from `atct_goal_complete` to the named request/approval/finalize sequence while retaining historical notes where they are explicitly records. Use the repository's AI-configuration and skill-writing procedures for edits under `skills/`.
- [ ] **Step 2: Run documentation/configuration checks.** Verify with repository search that no maintained instruction tells an operator to call `atct_goal_complete` for normal completion; the prohibition in `skills/subcommander/SKILL.md` may remain.
- [ ] **Step 3: Commit the maintained documentation.**

  ```sh
  git add skills/atct/SKILL.md skills/commander/SKILL.md skills/start/SKILL.md doc/investigations doc/specs doc/plans
  git commit -m "docs: update goal completion lifecycle"
  ```

## Subcommander final verification

After all executor task handoffs are reviewed and completed, run the checks not delegated to workers:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./... -count=1
pnpm --dir web test -- --run
git diff --check
rg -n 'KindCompletion|CompleteGoalWithReport|ApproveCompletion|RejectCompletion|atct_goal_complete' --glob '*.go' --glob '*.ts' --glob '*.tsx' --glob '*.md' .
```

The final `rg` output is allowed only for the stable retired-entry diagnostic, explicit historical migration records, and the executor prohibition in `skills/subcommander/SKILL.md`; no live completion behavior or normal-operation guidance may remain.
