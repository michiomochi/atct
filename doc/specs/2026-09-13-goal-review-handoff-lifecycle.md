# Human goal review completes the goal handoff

## Problem

The commander currently closes a goal handoff before requesting human goal
review. A human rejection therefore leaves the subcommander without its
received, open goal handoff; resuming requires a new handoff and a new receipt.

## Goal

Keep a reviewed goal handoff open while human approval is pending. A human
rejection returns that same handoff to the subcommander. Human approval closes
both the goal handoff and the goal through the existing commander-only
`atct_goal_review_complete` operation.

## Lifecycle

1. The subcommander calls `atct_goal_handoff_review_request`.
2. The commander calls `atct_goal_handoff_review_receive` and reviews the
   delivered implementation.
3. The commander either calls `atct_goal_handoff_review_reject`, or requests
   human approval with `atct_goal_review_request`. The latter does not close
   the handoff.
4. On human rejection, the commander calls `atct_goal_handoff_review_reject`
   with the human feedback. The subcommander calls
   `atct_goal_handoff_review_reject_receive`, revises the work, and requests
   review again using the same `handoff_id`.
5. On human approval, the commander calls `atct_goal_review_complete` after
   merging. It atomically completes the received goal handoff and marks the
   goal done. Its handoff completion report is the stored goal-handoff review
   request report; no new MCP input is added.

`atct_goal_review_complete` must refuse an approved human review unless the
latest delegated goal handoff is still received for review by that commander.
This prevents an approval from finalizing a different or already rejected
handoff.

## Store changes

- Split the current completed-handoff predicate into:
  - a commander-review-receipt predicate for opening human review; and
  - a commander-review-completion predicate for finalization.
- Let `RequestGoalReview` accept the first predicate instead of requiring
  `completed_report_at`.
- After a rejected human review, allow another human review only if the same
  handoff has a new `review_requested_at` after that rejection. This requires
  the normal reject-receive and resubmission cycle.
- In `FinalizeGoalReview`, complete the selected handoff and finalize the goal
  in one database transaction. If either update cannot satisfy its state
  guard, commit neither update.

## Notifications and documentation

- A human rejection remains a commander notification. The commander performs
  the normal goal-handoff rejection so the subcommander receives its existing
  role-scoped notification.
- Update `atct watch` guidance for a human rejection from “reissue a goal
  handoff” to “reject the received goal-handoff review”.
- Update `doc/execution-flow.md` so commander review, human review, and both
  reject paths are distinct.

## Tests

- A commander can request human review after receiving, but before completing,
  a goal-handoff review.
- Human rejection followed by goal-handoff rejection, rejection receipt, and
  re-review reuses the original `handoff_id`.
- Human approval followed by `atct_goal_review_complete` completes both the
  goal and the handoff.
- Finalization before approval, after rejection, or after the handoff was
  rejected leaves both records unchanged.
