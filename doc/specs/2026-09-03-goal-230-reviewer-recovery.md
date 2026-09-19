# Goal 230: goal-handoff reviewer recovery

## Goal

Permit the current commander of a goal's project to receive a pending
goal-handoff review only when the handoff's original requester no longer holds
that project claim. Preserve requester-only receipt for the normal path.

## Preconditions

- Goal 225's reviewed goal-handoff state machine has been integrated at exact
  commit `19937307be4794d62e7d8226856dad89f0123c76` or its verified integrated
  descendant. Goal 230's current base is
  `16c251b06d2caacd4d39a9f775877dcc9fca85ed` and is insufficient.
- The target handoff belongs to the supplied goal, has a review request, has
  not already been review-received, and is not complete.
- This change is limited to goal-handoff review receipt. Plan/task review,
  completion, rejection, migration, and workflow-event contracts do not change.

## Integration gate

Before implementation, rebase or otherwise integrate Goal 230 onto the verified
Goal 225 descendant. Stop before resolving any conflict in Goal 225-owned
state-machine or outbox paths: `internal/store/goal_handoff.go`,
`internal/store/task_handoff.go`, `internal/store/queries/task.sql`,
`internal/store/migrations/`, `internal/store/workflow_events.go`,
`internal/store/notify.go`, and their generated SQLC output/tests. Return that
conflict to the commander, because its resolution would decide Goal 225
integration rather than Goal 230 recovery scope.

Before writing a failing recovery test, run a pre-RED presence check against the
integrated base and require `ReceiveGoalHandoffReview` to exist. If it is absent,
stop: the prerequisite was not integrated, so recovery tests would target the
wrong state machine.

## Authorization

`ReceiveGoalHandoffReview(ctx, handoffID, goalID, callerID)` must authorize in
this order after validating identity and state:

1. If `callerID` is the recorded `RequestedBy`, accept exactly as today.
2. Otherwise resolve `goalID` to its project and load that project's current
   `ClaimedBy` value. Accept only if `callerID != 0`, it equals that value,
   and that value differs from the recorded requester.
3. Otherwise return the reviewer-mismatch/authorization error and make no
   database or event change.

This is a handoff-specific recovery, not generalized reviewer delegation. It
cannot be used while the original requester remains the project claimant, by a
subcommander/executor, or by a commander of another project.

The accepted recovery caller is persisted in `ReviewReceivedBy`. Existing
review reject/complete guards use that persisted reviewer, so the rest of the
review lifecycle remains coherent without additional fallback logic.

## State and observability

A successful recovery makes precisely the ordinary receive transition:
`review_requested -> review_received`, with one receive timestamp, one
persisted reviewer, and the existing Goal 225 workflow event. The RPC response
continues to be the ordinary role/evidence response and identifies the caller as
the commander for the target project. Rejected attempts create neither
timestamp, reviewer, nor event.

## Acceptance tests

- Existing requester receipt still passes unchanged.
- A different caller is rejected while the original requester holds the live
  claim.
- After claim turnover, the replacement commander receives successfully and is
  persisted as reviewer.
- A noncommander after turnover is rejected.
- A commander for another project is rejected.
- Each rejection asserts the handoff remains review-requested and no receive
  event/field was produced.

## Non-goals

- Do not modify or use Goal 225's open handoff or session 5483.
- Do not auto-transfer the project claim, infer a claimant from process
  liveness, or reopen completed handoffs.
- Do not change plan or task review authorization.
