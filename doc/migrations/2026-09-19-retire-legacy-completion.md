# Retire legacy completion authority

The legacy completion authority is retired. Completed work now goes through a
named goal review so the human approval boundary is explicit.

## Retired calls and diagnostics

`atct_goal_complete` and its `goal.complete` RPC compatibility entry point are
still present for transport compatibility, but they do not complete a goal.
They return this stable diagnostic before looking up or changing Store state:

`goal.complete is retired; use atct_goal_review_request followed by atct_goal_review_complete`

The generic HTTP decision endpoints also refuse the persisted legacy
`completion` kind. Answering one returns HTTP 400 with
`use approve or reject for this decision`; revising an answered or withdrawn
legacy completion returns HTTP 409 with `decision is not open`.

Historical decisions remain readable. The Web UI keeps historical completion
labels and `CompletionReport` fields, but no longer offers completion-only
approve/reject actions.

## Migration 0045

`internal/store/migrations/0045_retire_legacy_completion.sql` performs one
targeted update:

- rows with `kind = 'completion'` and `status = 'open'` become `withdrawn`;
- `answer_text` is set to
  `legacy completion retired; request a named goal review`;
- `answered_at` is populated with `COALESCE(answered_at, CURRENT_TIMESTAMP)`.

Answered, applied, and closed legacy rows are preserved, as are goal status and
completion-report fields. The migration does not rewrite historical decisions
or create a new goal result.

## Replacement sequence

When the work is complete:

1. Call `atct_goal_review_request` with the six-field completion report.
2. The human reviews and approves or rejects the named `goal_review` decision.
3. After approval, call `atct_goal_review_complete` to finalize the goal.

Use `atct_goal_withdraw` for work that is being abandoned rather than
completed. Use ordinary decision tools for questions that are not goal
completion approval.
