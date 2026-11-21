# Drop legacy task/goal claim columns

## Goal

Remove the legacy `claimed_by` and `claimed_at` columns from `tasks` and `goals` while preserving the domain API fields. Their values will be derived from the open handoff owner and timestamp.

## Constraints

- Keep `projects.claimed_by` and the public API field names unchanged.
- Keep `claimIsRunning`, `ClaimLiveness`, and `GoalClaimLiveness` contracts unchanged.
- Add a migration using direct `ALTER TABLE ... DROP COLUMN`; do not rebuild tables.
- Convert valid legacy claims to self-directed open handoffs; skip claims whose session no longer exists.
- Do not modify web callers or weaken foreign-key enforcement.
- Do not commit the changes.

## Approach

1. Add a migration that converts claims with registered sessions into received self-handoffs, drops invalid legacy claims by dropping the columns, and update `schema.sql`.
2. Change task and goal read queries to derive `claimed_by` and `claimed_at` from open handoffs, then remove write/release paths that target the deleted columns.
3. Preserve handoff cleanup on status transitions and withdrawals, and repair test fixtures to register sessions and claim the delegator's parent resource.
4. Add migration regression coverage, run the migration against a copy of the live database, and execute the focused and full test suites.

## Verification

- Count the baseline `internal/store` failures and report the post-change count.
- Verify valid and invalid legacy claims on a copied database without modifying `~/.atct/atct.db`.
- Run `GOCACHE=/tmp/gc37 go test ./...`.
