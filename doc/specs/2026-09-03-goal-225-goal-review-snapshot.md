# Goal 225: immutable goal-review approval snapshot

## Problem

Commit `4df1859` replaced the pre-approval `KindCompletion` path with a taskless `KindGoalReview` whose question is fixed to “Approve this goal review?”. The old path persisted the six-part completion report before approval, so the human could inspect `work_done`, `now_possible`, `how_to_verify`, `surprises`, `needs_review`, and `next_steps` on GoalDetail. The new path leaves those goal columns blank until a later commander `goal.complete`, and the completed goal handoff supplies only one unstructured `complete_report` string. It cannot be parsed or treated as the six-field approval record.

## Decision

The commander creates an immutable six-field snapshot in the same transaction as `goal.review.request`. The snapshot is linked one-to-one to its `KindGoalReview` decision and is the only report a human sees or approves in the goal-review lifecycle.

The commander owns the report at request time, after the delegated goal handoff has been reviewer-completed. The subcommander/executor handoff `complete_report` remains a separate, unstructured delegation summary. It is never copied into, parsed into, or used to amend the approval snapshot.

## Lifecycle

1. The existing commander-only ordering guard remains: a latest delegated goal handoff must be received, reviewed, and commander-completed before `goal.review.request`.
2. The commander sends all six non-empty report fields to `goal.review.request`. Store validation reuses `validateCompletionReport` and atomically creates both the taskless `KindGoalReview` decision and `goal_review_snapshots` row.
3. GoalDetail reads the snapshot together with the open `goal_review`, renders all six fields before the approve/reject form, and answers only the existing generic decision endpoints. While an active goal has an open `goal_review`, this snapshot card is the sole pre-approval six-field report: GoalDetail suppresses the legacy top-level completion report even if backfilled canonical goal columns contain the same values.
4. Human approval marks the decision approved/applied but leaves the goal active. Commander-only `goal.review.complete` loads the approved decision's stored snapshot, writes those exact fields into `goals`, and marks the goal done. It accepts no replacement report fields.
5. Human rejection records the reason and leaves the snapshot and completed handoff immutable. The commander explicitly requests a new handoff; after its completed review, a new `goal.review.request` creates a new snapshot. No automatic reissue or snapshot reuse occurs.

## API and data shape

- Add `GoalReviewSnapshot` with decision/goal IDs, the six report fields, commander/session ownership, and creation timestamp. Expose it as an optional `domain.Decision.GoalReviewSnapshot`; it is populated only for `KindGoalReview` detail/list responses.
- Add read-only `goal_review_snapshot` to the goal-detail decision JSON. It is present for `KindGoalReview` only; decision history keeps the same association when it includes such a decision.
- Extend daemon/MCP `goal.review.request` / `atct_goal_review_request` with the six required fields.
- Add commander-only daemon/MCP `goal.review.complete` / `atct_goal_review_complete` with only `goal_id`; it finalizes the approved immutable snapshot.
- Retain `goal.complete` / `atct_goal_complete` as the legacy `KindCompletion` adapter: it still takes six fields, writes the report before its completion decision, and must not select a goal-review snapshot.

## Migration and backfill

Use one forward migration to create `goal_review_snapshots` with a primary key/foreign key to `decisions(id)`, a goal foreign key, six non-null report columns, creator session ID, and timestamp. The table is append-only: no update/delete operation exists in store/RPC/MCP.

Existing `KindGoalReview` rows have no recoverable six-field source by default. Do not fabricate one from `complete_report` or the decision question. The migration leaves closed legacy decisions as historical records without a snapshot. It backfills an open review only when all six canonical goal columns are non-empty, then atomically withdraws every remaining open legacy review with a migration reason; the runtime approval path also refuses any review without a snapshot. A withdrawn review requires a commander to explicitly re-handoff and issue a new review snapshot. The migration does not change completed handoffs.

## Compatibility and non-goals

- Preserve `KindCompletion`, its approval/rejection endpoint behavior, existing completion report UI, and legacy MCP method/schema compatibility.
- Preserve goal-handoff review ordering, commander authorization, generic `/api/decisions/:id/approve|reject`, and explicit reject/reissue behavior.
- This does not repair reviewer binding, mutate current handoffs, or use session 5483.

## Acceptance criteria

- A human sees the exact six immutable values before approving an open `KindGoalReview`.
- An active goal with an open `KindGoalReview` shows that six-field report exactly once, in the snapshot review card, including after legacy backfill.
- The goal-review finalizer can only write the approved snapshot, never caller-supplied replacements.
- A rejected review preserves its snapshot and completed handoff; a reissued review receives a distinct snapshot.
- Legacy `KindCompletion` remains independent and unchanged.
- An unbackfillable legacy open goal review cannot be approved and is safely withdrawn for explicit commander reissue.
