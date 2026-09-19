# Goal 271: Received Review Lifecycle Notification Deduplication

## Observed persisted state

Goal 246 handoff `goal-246-goal-review-recovery-20260914` recorded `review_requested_at=2026-09-13T16:21:24.116018Z` and `review_received_at=2026-09-13T16:23:47.661516Z`. It remains open because human goal-review Decision 753 is open. The receipt is therefore valid and the old request is superseded for that handoff.

## Root cause

The store correctly publishes distinct committed lifecycle events, and reconciliation correctly projects the greatest persisted state with an event name, handoff ID, and RFC3339Nano generation. The shared watch selector then classifies both `*.handoff.review.request` and `*.handoff.review.receive` as agent actions. This reaches two consumers: the Claude token-bound monitor writes every selected action directly to stdout, while the Codex bridge queues it when a turn is active. Thus a receipt, which acknowledges review transport rather than asks the receiving role to perform work, is emitted as a second notification in both harnesses. The existing Codex queue can additionally retain an earlier queued request until that receipt arrives.

## Required behavior

Classify `*.handoff.review.receive` as a control-only action in the shared watch action model. Claude must not write it as a monitor turn. Codex must consume it only to remove queued, strictly older review actions for that handoff, without injecting the receipt itself. Compare review lifecycle actions only by the persisted RFC3339Nano generation: an equal generation is deduplicated and an older incoming generation is discarded. A later request retry or rejection/rejection receipt remains an agent-facing action. Do not remove an active turn, another handoff's action, or non-review actions. A request already written to Claude cannot be retracted; the fix prevents the receipt from becoming a second request and lets Codex cancel a request that is still queued.

## Scope

Modify only the shared watch action classification, Claude monitor output, Codex bridge queue handling, and focused tests. Do not change Goal 269 lifecycle order, Goal 270 activation, Goal 246 records, producer event persistence, general watch reconciliation, or existing notification history.
