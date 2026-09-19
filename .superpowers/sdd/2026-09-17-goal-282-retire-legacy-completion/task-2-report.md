# Task 2 report: retire open legacy completion decisions

## Result

Migration 0044 retires only persisted legacy completion decisions that are
still open. It leaves answered, applied, and closed legacy rows, along with
the goal and its completion report, unchanged.

## Changes

- Added `internal/store/migrations/0044_retire_legacy_completion.sql` with one
  `UPDATE` limited to `kind = 'completion' AND status = 'open'`.
- The migrated open rows become `withdrawn`, receive the exact answer text
  `legacy completion retired; request a named goal review`, and get an
  `answered_at` value through `COALESCE`.
- Added focused coverage for open, answered, applied, and closed legacy rows,
  plus an open `goal_review` sentinel. The test snapshots each decision's full
  relevant state and asserts the non-completion sentinel and goal status/report
  fields remain unchanged.
- Did not edit `schema.sql`, Task 1 code/report, or unrelated code.

## Verification

Passed:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./internal/store -run 'Test.*(LegacyCompletionMigration|Migration)' -count=1
./script/schema-check.sh
git diff --check
```

The store migration test passed with the non-completion sentinel coverage. The
schema check passed after rerunning the
same script with elevated filesystem access because the sandbox could not open
the default Go cache under `~/Library/Caches`.

## ATCT availability

The required `atct_task_handoff_receive` and `atct_role(expected_role=executor)`
calls were each attempted once with the supplied session credentials. Both
were blocked by the PreToolUse hook with:

`Tool call blocked by PreToolUse hook: ATCT: this session has no live Monitor, so a wakeup would never reach it.`

Neither call was retried. The received handoff was used as implementation
authorization, as instructed. The single review-request attempt after the
commit was blocked by the PreToolUse hook with:

`Tool call blocked by PreToolUse hook: ATCT: this session has no live Monitor, so a wakeup would never reach it.`

It was not retried.

## Paths

- `internal/store/migrations/0044_retire_legacy_completion.sql`
- `internal/store/legacy_completion_migration_test.go`
- `.superpowers/sdd/2026-09-17-goal-282-retire-legacy-completion/task-2-report.md`
