# Goal 225: goal-review request-time completion report

## Problem

`KindGoalReview` currently opens a taskless human review without the six-field completion report that the human must evaluate. The older `KindCompletion` flow writes those fields to `goals` before human approval, but displaying both that legacy report and the goal-review card renders the same report twice. The finalization path also currently accepts caller-supplied report fields after approval, so it does not establish one request-time value as the value that becomes final.

## Decision

`goal.review.request` accepts and validates the six existing completion-report fields, then atomically writes them to the existing `goals` row and creates the taskless `KindGoalReview` decision. There is no snapshot table, decision-attached report object, migration, or backfill.

While a goal has an open `KindGoalReview`, no operation may replace its completion report. `goal.review.complete` is commander-only and accepts only `goal_id`; after human approval, it marks the active goal done using the already persisted goal fields. It accepts no report input and does not rewrite the six fields.

The persisted goal fields are the request-time source of truth. GoalDetail shows them in the goal-review card before approval. While that card is open on an active goal, it suppresses the ordinary top-level completion report, so the six fields appear exactly once.

## Lifecycle

1. The existing commander-only ordering guard remains: the latest delegated goal handoff must be received, reviewed, and commander-completed before `goal.review.request`.
2. The commander sends all six non-empty fields to `goal.review.request`. Store validation reuses `validateCompletionReport`, then one transaction updates `goals` and creates `KindGoalReview`.
3. GoalDetail reads the persisted fields with the open `goal_review`, shows them inside the review card before approve/reject, and suppresses the separate legacy completion report for that active review.
4. Human approval marks the decision approved/applied but leaves the goal active. Commander-only `goal.review.complete(goal_id)` verifies the approved review and atomically marks the goal done without accepting or changing report fields.
5. Human rejection records the reason and leaves the goal active with its current report fields. The commander explicitly requests a new handoff; after review, a new `goal.review.request` may replace the goal's report fields with the new request-time report.

## API and data shape

- Extend daemon/MCP `goal.review.request` / `atct_goal_review_request` with the six required completion-report fields.
- Add commander-only daemon/MCP `goal.review.complete` / `atct_goal_review_complete` with only `goal_id`.
- Goal-detail response shape is unchanged: its existing `goal` report fields supply the review card.
- Retain `goal.complete` / `atct_goal_complete` as the legacy `KindCompletion` adapter. It takes six fields, writes the report before its completion decision, and is not the finalizer for a named goal review.

## Compatibility and non-goals

- Preserve goal-handoff ordering, commander authorization, generic `/api/decisions/:id/approve|reject`, and explicit rejection/re-handoff.
- Preserve legacy completion and proposed-goal UI paths whenever there is no open `KindGoalReview`.
- Do not create a snapshot table, new report payload on `Decision`, backfill legacy reviews, or preserve a rejected review's six fields per decision. The `goals` row is intentionally the only persisted report source.
- This does not repair reviewer binding, mutate current handoffs, or use session 5483.

## Acceptance criteria

- `goal.review.request` rejects a missing report field and atomically persists all six request-time fields with the open review.
- An open review prevents report replacement; `goal.review.complete` has no report input and marks done from the request-time fields only.
- An active goal with an open `KindGoalReview` displays its six fields exactly once in the review card, including when those fields were already populated by an earlier legacy path.
- Rejected review values are not preserved per decision; a later re-review overwrites the existing goal report only at its new request time.
- Legacy `KindCompletion` and proposed-goal behavior remain unchanged.
