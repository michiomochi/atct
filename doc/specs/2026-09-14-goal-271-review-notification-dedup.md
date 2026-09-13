# Goal 271: Received Review Lifecycle Notification Deduplication

## Observed persisted state

Goal 246 handoff `goal-246-goal-review-recovery-20260914` recorded `review_requested_at=2026-09-13T16:21:24.116018Z` and `review_received_at=2026-09-13T16:23:47.661516Z`. It remains open because human goal-review Decision 753 is open. The receipt is therefore valid and the old request is superseded for that handoff.

## Root cause

The store publishes distinct committed lifecycle events. Reconciliation correctly selects only the highest state (`goal.handoff.review.receive`) and its per-watch delivery key includes event name, handoff ID, and generation. However, the Codex monitor bridge queue also keys items by event name. When a monitor is active, a queued `*.handoff.review.request` survives arrival of the later `*.handoff.review.receive`; both are injected as turns once the session returns idle. They are historical duplicates for one handoff lifecycle, not separate pending work.

## Required behavior

At the bridge boundary, compare only `*.handoff.review.*` actions for the same handoff by their RFC3339Nano lifecycle generation: the incoming action replaces queued actions with a strictly earlier generation; an equal generation is deduplicated; an earlier incoming generation is discarded. The generation is the timestamp selected by reconciliation for the current transition, not the arrival order or phase name. This preserves a later retry request or rejection receipt and prevents a delayed old snapshot from replacing it. Do not remove the active turn, actions for another handoff, or non-review-lifecycle actions. Live and reconciliation delivery retain their existing per-watch deduplication.

## Scope

Modify only Codex bridge queue supersession and focused tests. Do not change Goal 269 lifecycle order, Goal 270 activation, Goal 246 records, producer event persistence, general watch reconciliation, or existing notification history.
