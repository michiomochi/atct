# Goal 230 investigation: reviewer recovery after commander turnover

## Scope and boundary

Decision #622 permits only a recovery for `goal.handoff.review.receive`.
The recovery must not reopen or alter a handoff, create tasks, or use an
existing Goal 225 handoff/session. This investigation read the Goal 225
branch's committed implementation and tests only; it did not call an ATCT API
for Goal 225 or inspect session 5483.

The planned change applies only after Goal 225's reviewed-handoff state machine
is available. The reviewed source base is exactly
`19937307be4794d62e7d8226856dad89f0123c76` (`wt/goal-225`, `feat: add
GoalDetail goal review actions`). Goal 230 currently starts at the older merge
base `16c251b06d2caacd4d39a9f775877dcc9fca85ed`; it has no review-receive
method to change.

## Existing contract in Goal 225

`GoalHandoff` persists `RequestedBy`, review request/receive fields, and the
goal ID in `internal/store/goal_handoff.go`. Goal 225's
`ReceiveGoalHandoffReview` validates handoff goal and state, then accepts only
`receivedBy == handoff.RequestedBy`. Its SQL update in
`internal/store/queries/task.sql` checks state but has no reviewer identity
predicate; the Go store guard is the authorization point.

The daemon's `goal.handoff.review.receive` calls that store method, then builds
a role/evidence response through `receiveGoalReviewResponse`.
`receiveRoleEvidence` and `deriveSessionRole` treat the session in
`projects.claimed_by` for the goal's project as `commander`. They do not
authorize the transition themselves. `authorizeCommander` has the same
project-ID / `ClaimedBy` equality shape, but putting the primary guard there
would leave direct Store callers unsafe.

`RequestGoalHandoff` proves that project claim is the ownership source:
`requireProjectClaimForGoal` obtains the goal's project and compares its
`ClaimedBy` with the requester. `ClaimProject` changes that field after a dead
holder can be replaced. A replacement commander therefore differs from the
original review requester.

## Proposed authorization rule

For an otherwise valid, review-requested and unreceived goal handoff:

1. Allow the recorded `handoff.RequestedBy` exactly as before.
2. Otherwise, load the handoff goal and project. Allow only when
   `project.ClaimedBy == caller`, `caller != 0`, and the current claim differs
   from the recorded requester.
3. Reject every other caller without writing review-receive fields or emitting
   a workflow event.

The recovery uses the stored current project claim, not liveness inference. A
dead requester whose stale claim remains recorded is not recoverable; a claim
turnover is required first. The accepted recovery caller is stored in
`ReviewReceivedBy`, so existing reject/complete guards continue to apply
without a special case.

## Regression matrix

| Scenario | Caller / claim | Expected result |
| --- | --- | --- |
| Normal path | recorded requester; requester holds live claim | receive succeeds; reviewer is requester |
| Live requester protection | another session; requester holds live claim | reject; receive fields/event unchanged |
| Recovery | requester lost claim; caller holds target project claim | receive succeeds; reviewer is caller |
| Stale requester, noncommander | requester lost claim; caller has no claim | reject; no mutation |
| Foreign project | caller claims a different project | reject; no mutation |

The Store fixture needs a second project for the foreign case. Daemon/RPC tests
need a replacement commander call to the named route and must assert target
project commander evidence plus persisted `ReviewReceivedBy`.

## Files expected to change after plan acceptance

- `internal/store/goal_handoff.go`: recovery authorization branch and possibly
  a specific denial error.
- `internal/store/goal_handoff_test.go`: direct state-machine matrix.
- `internal/daemon/goal_handoff_test.go`: named RPC response and scope tests.

No schema, SQL, MCP schema, daemon dispatch, state-transition, or Goal 225
migration change is required: all necessary persisted fields already exist.
