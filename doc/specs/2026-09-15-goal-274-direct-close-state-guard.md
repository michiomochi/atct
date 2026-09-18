# Goal 274: Delegated Goal Handoff Direct-Close State Guard

## Goal

Prevent a normal delegated goal handoff from being completed through either
direct-close store API. A delegated handoff must be closed by the recorded
reviewer after review receipt.

## Scope

- `CompleteGoalHandoff` rejects any non-system completion report for a
  delegated handoff with `ErrGoalHandoffReviewState`, both before and after a
  review request.
- `CompleteGoalHandoffForGoal` inherits that rejection because it resolves the
  handoff then calls `CompleteGoalHandoff`.
- `CompleteGoalHandoffByReviewer` remains the only normal delegated completion
  route after review receipt.
- Self-claim handoffs and the reclaim/release system reports continue to close
  as they do now.

## Decision

Put the guard in `CompleteGoalHandoff`, the shared direct-close operation.
It already has the resolved handoff identities and every `ForGoal` direct
close flows through it. Restricting the existing review-request condition
would still leave the pre-request bypass; duplicating a guard in `ForGoal`
would create two inconsistent policies.

The guard distinguishes a real delegation via `handoffIsDelegation` and
allows only the two existing system reports for that delegation. Self-claims
remain outside the guard. The reviewer-specific API is unchanged.

## Tests

Add focused table coverage in `internal/store/goal_handoff_test.go` for both
direct-close APIs before and after review request, asserting
`ErrGoalHandoffReviewState` and no completion timestamp. Retain or adapt the
self-claim and reclaim/release coverage so their successful behavior remains
explicit. Do not change Goal 268's review-receive mutation guard.
