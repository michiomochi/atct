# Goal 273: Persistent Next Goals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development for implementation tasks and superpowers:test-driven-development before changing behavior. Follow the task boundaries and verification commands below.

**Goal:** Replace public `next_steps` completion prose with ordered,
persistent `next_goals` links while preserving existing prose internally.

**Architecture:** Add a normalized `next_goals` SQLite table and an internal
`legacy_next_steps` migration column. Store successor IDs as part of the
transaction that persists a review/complete report. Expose shallow successor
summaries from goal detail endpoints and render them in the web UI. Keep
`derived_from` lineage separate.

**Tech Stack:** Go, SQLite, SQLC, MCP/daemon handlers, HTTP JSON, React,
Astro, Vitest.

## Global Constraints

- Work only in the Goal 273 worktree.
- Do not edit Goal 272, 294, 287, or 290 owned files. In particular, leave
  `internal/mcpshim/instructions.go` and `skills/atct/SKILL.md` for Goal 290.
- Use existing helpers and dependencies; do not add a package.
- Use TDD for behavior changes: write the smallest failing test, implement,
  then run the focused command listed for the task.
- Regenerate SQLC with the repository's canonical `go tool sqlc generate`
  command after query/schema changes; review generated diffs rather than
  hand-editing generated files.
- Do not merge, rebase, push, or run daemon operations.

## Task 1: Migration, domain, and store persistence

**Files:** `internal/store/migrations/0044_next_goals.sql` (next available
number), `internal/store/schema.sql`, `internal/store/store.go`,
`internal/store/migrations.go`, `internal/store/queries/goal.sql`,
`internal/store/goal.go`, `internal/domain/model.go`, generated
`internal/store/sqlcgen/*`, and focused store/migration tests.

**Implementation:**

1. Add the migration that creates `next_goals`, rebuilds `goals` without the
   public `next_steps` column, copies its values to `legacy_next_steps`, and
   preserves all unrelated rows/constraints/indexes. Advance the logical
   schema validation from v6 to v7 using the repository's migration/version
   conventions. Keep the five-field done validation; successor links are
   optional.
2. Add SQLC queries for ordered successor summaries/IDs and transactional
   replacement. Keep query column lists explicit so `legacy_next_steps` never
   enters public domain values. Regenerate SQLC.
3. Replace `NextSteps` in public domain/report types with the ordered successor
   input and a small successor summary type where the existing API patterns
   require one.
4. Validate IDs in the store boundary: target exists, belongs to the same
   project, is not the source, and is not duplicated. Preserve caller order
   through `sort_order`.
5. Make review/complete report persistence replace links in the same
   transaction. Ensure review rejection preserves the old links and
   withdrawal/finalization follow the spec.

**Tests:** Add migration tests for v6-to-v7 data preservation/schema shape and
store tests for ordering, validation, replacement, rejection, withdrawal, and
empty successors. Run:

```sh
GOCACHE=/private/tmp/goal273-task1-gocache go test ./internal/store/...
```

## Task 2: Daemon and MCP contract

**Dependencies:** Task 1.

**Files:** `internal/daemon/handler.go`, daemon tests, `internal/mcpshim/tools.go`,
and MCP/daemon contract tests. Do not modify `internal/mcpshim/instructions.go`.

**Implementation:**

1. Remove `next_steps` from review-request and complete input structs,
   parameter conversion, and generated/declared MCP schemas.
2. Add optional ordered `next_goal_ids` input and pass it to the store/domain
   layer without reimplementing validation in each caller.
3. Extend goal detail output with ordered, shallow successor summaries. Reuse
   the existing goal lookup/serialization conventions and avoid recursive
   successor expansion.
4. Make legacy `next_steps` input fail explicitly if the decoder otherwise
   accepts unknown fields; do not silently discard existing caller data.

**Tests:** Cover schema/input conversion, omission of successor IDs, invalid
legacy input, ordered goal detail output, and the no-recursion shape. Run:

```sh
GOCACHE=/private/tmp/goal273-task2-gocache go test ./internal/daemon/... ./internal/mcpshim/...
```

## Task 3: HTTP and web presentation

**Dependencies:** Tasks 1 and 2.

**Files:** `internal/httpapi/server.go` and HTTP tests; `web/src/lib/api.ts`,
`web/src/components/GoalDetail.tsx`, relevant component/API tests, and
`web/src/i18n/en.ts`/`ja.ts`.

**Implementation:**

1. Add ordered shallow `next_goals` summaries to the goal detail response,
   using the same project/authorization boundary as the source goal.
2. Remove `next_steps` from TypeScript models, completion-report rendering,
   fixtures, and translations.
3. Render successor summaries as navigable goal links, including a concise
   empty state and target status; do not render an expandable recursive graph.
4. Keep existing derived-goal display behavior unchanged.

**Tests:** Add HTTP response coverage and React/API coverage for ordered
successors, empty successors, navigation fields, and absence of `next_steps`.
Run:

```sh
GOCACHE=/private/tmp/goal273-task3-gocache go test ./internal/httpapi/...
npm --prefix web test
npm --prefix web run typecheck
```

## Integration verification owned by the subcommander

After all task handoffs are accepted and merged into this worktree by normal
ATCT workflow, inspect the diff for forbidden files and run the focused tests
again, then run the repository's standard verification command documented in
`CONTRIBUTING.md`. Confirm with `git diff --check` that generated SQL/schema
changes and docs are clean. Do not report goal completion until every task is
accepted and the goal review request has been made.

## Handoff order

1. Request plan review for this document.
2. After plan acceptance, declare exactly three tasks and delegate one
   monitored executor per task, in dependency order.
3. Accept each executor's review request only after its focused tests and
   changed-file boundary are verified.
4. Run integration verification, commit the goal work in this worktree, and
   request the goal review with the required report fields.
