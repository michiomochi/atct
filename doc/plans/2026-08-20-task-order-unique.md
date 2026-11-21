# Task sort-order uniqueness implementation plan

## Goal

Preserve the already-implemented per-goal MAX-based sort allocation and make
`(goal_id, sort_order)` unique for new databases and existing databases with
duplicate values. Restore task listing to `sort_order, id` ordering.

## Constraints

- Add only migration `0003`; do not rewrite `0001` or `0002`, and do not add a
  data migration for any other table.
- Migration data repair must order each goal by `created_at, sort_order, id`
  and assign contiguous zero-based values before creating the unique index.
- Validate the repair SQL against the repository's modernc.org/sqlite driver
  before using it in the migration.
- Keep the unique constraint scoped to each goal, keep `domain.Task.Order`
  unchanged, and leave `web/` untouched.
- Verify a copy of the real database only; never open the real database for
  writing.

## Work items

1. Add a migration-focused fixture that proves the repair preserves task count,
   per-goal boundaries, and the requested pre-migration ordering. Use it first
   to exercise the candidate SQL through modernc.org/sqlite and capture RED
   behavior when repair or the index is absent.
2. Add the `0003` migration, the matching unique index to `schema.sql`, and
   change all three task query orderings to `sort_order, id`; regenerate sqlc.
3. Add tests for duplicate rejection, same sort order on another goal,
   deterministic task ordering, and concurrent declarations under
   `SetMaxOpenConns(1)`.
4. Run the required Go and wrapper checks, then migrate a `VACUUM INTO` copy of
   the real database and verify 259 tasks, zero duplicate pairs, and three
   recorded migrations. Report the exact adopted SQL and RED evidence.

## Verification

Run `go tool sqlc generate`, `go build ./...`, the uncached Go test command,
`bash tests/wrapper_test.bash`, and the real-database-copy checks from the
request. Confirm only the requested Go/store files plus this plan are changed;
do not commit or touch `web/`.
