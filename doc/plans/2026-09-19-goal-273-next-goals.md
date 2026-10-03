# Goal 273: Persistent Next Goals Implementation Plan

## Revision 2026-10-03 (human review, decision 855)

The human rejected the goal review with three simplifications. They supersede
every conflicting statement in the original plan below (which is kept as the
record of tasks 1307-1309, 1323, 1324).

1. Drop `legacy_next_steps`: the migration discards the old `next_steps` values.
2. Drop `next_goals.sort_order`: links are a set, listed by `next_goal_id`
   ascending; the contract no longer says "ordered".
3. Drop `goal_review_state_snapshots` and migration 0046 for it. Evidence: main's
   `RejectGoalReview` never restored a previous report, and the same kind of
   table (`goal_review_snapshots`, 0026) was dropped in 0028. A rejected review
   leaves the links as the rejected request set them; the next request replaces
   them.

Goal 282 has since merged to main (`goal.complete` retired, migration
`0045_retire_legacy_completion.sql`). `next_goal_ids` therefore lives only on
`atct_goal_review_request`; `atct_goal_complete` stays a retired stub.

### Task A: Merge current main and resolve conflicts
Merge main into the worktree (the subcommander starts the merge and commits it;
the executor only resolves the conflicted files and does not commit). Conflicts
are in daemon, store, e2e, httpapi tests and skills/atct/SKILL.md. Keep main's
Goal 282 behavior (retired `goal.complete`, review-based flow) and keep this
goal's `next_goals` work on the review path. Rename `0045_next_goals.sql` to
`0046_next_goals.sql` and `0046_goal_review_state_snapshots.sql` to
`0047_goal_review_state_snapshots.sql` so migrations are one linear sequence
(Task B then removes the latter). Goal: `go build ./...` and the focused
packages compile and pass; no design change here.

### Task B: Simplify persistence
Edit `0046_next_goals.sql` in place (never applied anywhere): no
`legacy_next_steps`, no `sort_order`/`UNIQUE(goal_id, sort_order)`; rebuilt
`goals` has neither `next_steps` nor a legacy column. Delete the snapshots
migration, table, queries, sqlc output, `schema.sql` entry, schema validation
(version back down by one), the snapshot write in `RequestGoalReview` and the
restore in `RejectGoalReview` (rejection becomes main's behavior). Queries list
links `ORDER BY next_goal_id`. Replace `replaceNextGoals` order handling with a
set insert (duplicates still rejected). Update store tests (drop ordering,
legacy, snapshot and restore-on-reject tests; add: rejection keeps the rejected
request's links). `./script/schema-check.sh` must pass.

### Task C: Contract, presentation, skills wording
Remove "ordered" from `next_goal_ids` everywhere it is described or asserted:
MCP schema text, daemon/mcpshim tests, HTTP goal detail (summaries sorted by
target ID), web (`GoalDetail`, i18n, tests), skills/atct/SKILL.md,
skills/commander/SKILL.md, tests/wrapper_test.bash. Remove any read path of
`legacy_next_steps` (daemon/store test fixtures that write it). Do not touch
`internal/mcpshim/instructions.go`.

### Verification (subcommander, committed tree, merged main)
`go build ./...`, `go vet ./...`, `go test ./... -count=1`,
`./script/schema-check.sh`,
`ORCHESTRATION_SKILL_PATH=/private/tmp/atct-no-orchestration-skill bash tests/wrapper_test.bash`,
web test / typecheck / build, `git status --short` empty.

---


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
