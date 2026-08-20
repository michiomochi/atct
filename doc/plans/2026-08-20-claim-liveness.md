# Claim liveness implementation plan

## Goal

Use the recorded agent-session process identity to separate running claims from
claims that are no longer verifiable, then report stale claims from `pending`
without releasing them automatically.

## Constraints

- Derive `started_at` and its comparison value from the same `ps -p <pid>
  -o lstart=` helper; never use `time.Now()` for process identity.
- Send only the shim PID; the daemon records the PID and process start time.
- Save PID zero when process-start lookup fails, while allowing session
  registration to succeed.
- Require session presence, a live PID, and an exact start-time match for
  `running`; treat an unverifiable claim as `stale`.
- Keep the existing four `pending` reasons, own-claim section, task claim
  columns, context output, SSE/watch/wakeup behavior, and migration fixtures
  unchanged. In particular, retain migration-test `run_id TEXT NOT NULL`.
- Never automatically release a stale claim.

## Work items

1. Add the shared process-start helper and tests for the current and missing
   PID cases.
2. Extend session registration and the MCP run-register request with PID-only
   identity capture, including the PID-zero fallback.
3. Add the store liveness judge with positive, stale, PID-reuse, dead-PID, and
   cross-project coverage.
4. Add a separate stale-claim reason to `pending`, covering own claims,
   running claims, stale claims, and project filtering.
5. Rename the agent-session test file mechanically, regenerate sqlc output,
   run the required Go and wrapper checks, and verify a read-only database copy.

## Verification

Measure `processStartedAt` with the current process and a missing PID, capture
the intentional RED after removing only the start-time comparison, restore the
comparison, run the full uncached Go suite and wrapper tests, and confirm the
45-hour `stock-data` claim appears in the copied database's explicit project
view.
