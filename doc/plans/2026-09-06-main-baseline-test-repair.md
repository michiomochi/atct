# Main Baseline Test Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore a green main-suite baseline without changing the product contracts that Goal247 depends on.

**Architecture:** Keep production behavior intact. Correct stale test setup for current ownership and completion-review rules, make the MCP wrapper tests hermetic against an inherited wrapper path, and build ignored web assets as the suite prerequisite.

**Tech Stack:** Go, SQLite store/daemon tests, pnpm/Astro generated assets.

## Global Constraints

- Work only in the Goal247 integration worktree; do not touch the root worktree.
- Do not write the shared ATCT database or control its daemon.
- Keep Goal247's five migration-lineage paths unchanged.

---

### Task 1: Correct ownership and review test fixtures

**Files:**
- Modify: `cmd/atct/context_test.go`, `cmd/atct/delegated_task_display_test.go`
- Modify: `internal/daemon/pending_response_test.go`, `internal/e2e/full_flow_test.go`, `internal/httpapi/server_test.go`

**Interfaces:**
- Consumes: task claims create received handoffs and `CompleteGoalWithReport` requires the caller's valid ownership.
- Produces: tests that construct the current valid state before asserting it.

- [ ] **Step 1: Retain each existing failing assertion as the RED evidence**

Run: `go test ./cmd/atct ./internal/daemon ./internal/e2e ./internal/httpapi -count=1`

Expected: the stale ownership and completion-review fixtures fail.

- [ ] **Step 2: Make fixtures model the intended state**

Use received task/goal handoffs where an ownership assertion requires them; attach a six-field completion report before testing goal-review approval/rejection; update only expectations invalidated by the existing contract.

- [ ] **Step 3: Verify GREEN**

Run: `go test ./cmd/atct ./internal/daemon ./internal/e2e ./internal/httpapi -count=1`

Expected: those packages pass once web assets are built.

### Task 2: Make wrapper tests hermetic and build assets

**Files:**
- Modify: `cmd/atct-mcp/main_test.go`
- Generated, untracked: `web/dist/**`

**Interfaces:**
- Consumes: `resolveAtctPath` honors a valid `ATCT_ATCT_BIN`; the web package embeds `web/dist` at compile time.
- Produces: environment-independent wrapper tests and a reproducible full-suite command.

- [ ] **Step 1: Retain the inherited-environment failures as RED evidence**

Run: `go test ./cmd/atct-mcp -count=1`

Expected: the tests that omit `ATCT_ATCT_BIN` fail when the parent environment sets it.

- [ ] **Step 2: Clear only the inherited variable in tests that test fallback precedence**

Call `t.Setenv("ATCT_ATCT_BIN", "")` in those tests; leave configured-wrapper tests unchanged.

- [ ] **Step 3: Verify GREEN and prepare assets**

Run: `go test ./cmd/atct-mcp -count=1 && pnpm --dir web build`

Expected: wrapper tests pass and `web/dist/index.html` exists for Go embedding.

### Task 3: Verify merged integration

**Files:**
- Verify only: all changed files and Goal247 migration paths

- [ ] **Step 1: Run the full merged suite**

Run: `GOCACHE=/private/tmp/goal247-merged-gocache go test ./... -count=1`

Expected: PASS.

- [ ] **Step 2: Check the staged integration**

Run: `git diff --check main..HEAD && git status --short`

Expected: no whitespace errors; only planned tracked changes plus ignored generated assets.
