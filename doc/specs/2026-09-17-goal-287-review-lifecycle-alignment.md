# Goal 287: Human goal review lifecycle alignment

## Purpose

Make the normal delegated-goal lifecycle enforce the same order at the store and guidance boundaries:

1. The commander receives the goal-handoff review.
2. The commander requests human goal review while that handoff remains open.
3. Human approval permits the commander finalizer to atomically close the reviewed handoff and the goal.

An unfinished or blocked goal is reported through the decision channel, not as a completed goal-handoff review.

## Current-state evidence

The current `main` implementation already has the intended positive path:

- `RequestGoalReview` accepts an open commander-received handoff and records the human-review decision without completing the handoff.
- `ApproveGoalReview` records human approval.
- `FinalizeGoalReview` validates that approval and closes the handoff and goal atomically through its transaction-local SQL call.
- `RequestGoalReview` rejects a proposed, not-yet-activated goal with the existing not-active error.

There are two remaining state bypasses:

- `ReceiveGoalHandoffReview` checks for a requested, unreceived handoff but does not reject `review_rejected_at` or `review_rejection_received_at`. A rejected review can therefore be written back into the received state while its rejection evidence remains present.
- The public `Store.CompleteGoalHandoffByReviewer` route used by `atct_goal_handoff_complete` can close a handoff after the commander has received its review, before the human-review decision and finalizer.

The guidance has a related ambiguity: it lists the destination for unsent reports but does not explicitly say where unfinished/blocked work goes or that `atct_goal_handoff_review_request` is terminal. Recent lifecycle incidents used that review request as a blocker/progress message, and a proposed goal was mistaken for a goal-review-reporting state.

## Incident evidence: unfinished blockers used the terminal review channel

The following three observed cases establish that the ambiguity is operational, not hypothetical:

| Goal | Observed transition | Result |
| --- | --- | --- |
| 237 | An unfinished blocker was submitted through `atct_goal_handoff_review_request`. | The request was rejected as the wrong channel. |
| 260 | An unfinished blocker was submitted through `atct_goal_handoff_review_request`. | The request was rejected as the wrong channel. |
| 248 | An unfinished blocker was submitted through `atct_goal_handoff_review_request`. | The request was rejected as the wrong channel. |

The common failure was not the underlying work; the guidance did not name an explicit home for unfinished/blocked work. The corrective rule is therefore a channel distinction: keep unfinished work open and use `atct_decision_ask` for a human choice, while reserving `atct_goal_handoff_review_request` for the terminal ready-for-commander-review state.

## External dependency: rejected plan handoff recovery

The original plan handoff was requested by the stopped subcommander session, while the reissued goal handoff is held by the current subcommander session. ATCT currently binds rejection receipt to the original requester and rejects both rejection receipt and recovery for this state. The commander has assigned that recovery defect to Goal 296 and instructed Goal 287 not to create a replacement plan handoff until it is fixed. This is a workflow dependency, not permission to bypass the plan-review gate or to begin implementation directly.

## Design decisions

1. Reject invalid review receipt states in the store layer. `ReceiveGoalHandoffReview` returns the existing `ErrGoalHandoffReviewState` when either rejection timestamp is present, and leaves the reject report and timestamps unchanged. The normal requested-but-not-yet-received state remains valid.
2. After the Goal 274 main-integration gate is satisfied, guard the shared public direct-close method. `Store.CompleteGoalHandoffByReviewer` returns the same existing error for a received delegated handoff in the commander-review lineage and leaves the handoff unchanged. This closes the bypass for both store and daemon callers without adding a flag, API, or schema.
3. Preserve the existing finalizer. `FinalizeGoalReview` remains the only normal close path after human approval and keeps its transaction-local SQL completion call; it must not be routed through the public direct-close guard.
4. Only after the Goal 284/283 integration gate is satisfied, make the shared ATCT guidance explicit with the smallest possible wording change: `atct_goal_handoff_review_request` is terminal and is used only after declared work is accepted and committed; unfinished/blocked work stays open and uses `atct_decision_ask`; initial approval activates proposed goals before `atct_goal_review_request` can apply. The current owner of `doc/execution-flow.md` remains responsible for that file; this goal does not take it over.
5. Keep ownership boundaries intact. Do not edit `doc/execution-flow.md` or `skills/subcommander/SKILL.md` (currently owned by Goal 286), MCP instruction compression (Goal 290), stale-approval withdrawal (Goal 272), or recovery behavior owned by adjacent goals.

## Scope and constraints

- Do not create or delegate store/test tasks until the Goal 274 main-integration gate has been verified.
- Do not start the `skills/atct/SKILL.md` or lifecycle-document follow-up until the Goal 284/283 integration gate has been verified.
- Modify only the store guards/tests and the minimal shared guidance needed to remove the ambiguity.
- Reuse `ErrGoalHandoffReviewState` and existing lifecycle helpers.
- Update historical test fixtures that used the public reviewer-close method so tests do not encode the bypass. Do not weaken the production guard for fixture convenience.
- Do not merge/rebase onto `main`, push, run daemon operations, or change the database schema/tool surface.
- Record the `skills/atct/SKILL.md` overlap in `needs_review`; Goal 290 owns its broader compression, so this change must remain self-contained.

## Acceptance criteria

- A direct `CompleteGoalHandoffByReviewer` attempt after commander review receipt returns `ErrGoalHandoffReviewState` and does not set completion fields or publish a completion event.
- The daemon/RPC route has the same rejection, regardless of whether it arrives through the session-aware reviewer route or the ordinary goal route.
- `ReceiveGoalHandoffReview` rejects both rejected and rejection-received review states and preserves the existing rejection report and timestamps; a fresh requested review can still be received once.
- `RequestGoalReview` still succeeds with an open received handoff, and `FinalizeGoalReview` still closes the handoff and goal only after human approval.
- A proposed goal is documented as requiring initial approval/activation before completion review; no stale-approval behavior is changed.
- Guidance sends unfinished/blocked work to `atct_decision_ask` and reserves the goal-handoff review request for terminal, ready-for-commander-review state.
- Focused store and daemon tests, plus `git diff --check`, pass.
