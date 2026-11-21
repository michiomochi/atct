# Notification delivery repair

## Problem

One long-lived project watch suppresses a second plan-review request after a
reject/re-request cycle because its detection-delivery key contains only the
event name and handoff ID. Separately, a project reconciliation can enqueue
the same applied goal approval repeatedly before a goal handoff is received.
The Codex bridge keeps those lines while a turn is active and later starts
them, even after the canonical handoff receive has made the approval irrelevant.

## Decision

The watch will retain a structured action alongside the rendered line when it
hands an action to the Codex bridge. The action includes its event name and
goal ID; normal text writers remain supported. A goal-handoff receive action
causes the bridge to remove queued, not-yet-started `decision.approved` actions
for that goal before it queues/delivers the receive action. An approval that
already started is not cancelled.

For handoff lifecycle deduplication, the delivery key gains a lifecycle
generation. Plan-review request generations use `ReviewRequestedAt`; other
lifecycle events use the timestamp that determines their current event. Thus a
single canonical request cycle emits once across SSE/reconciliation/reconnect,
while reject followed by a new request using the same handoff ID emits again.

## Rejected alternatives

- Add SSE replay or persistent delivery history: broader protocol/storage work
  and unnecessary for the two reproduced conditions.
- Let the bridge call reconciliation at pump time: adds daemon availability to
  turn delivery and races the snapshot it is trying to validate.
- Coalesce rendered strings only: decision lines do not contain their goal ID,
  so it cannot remove precisely the approval invalidated by a handoff receive.

## Constraints

- Preserve project/goal/task scope filtering and all non-stale lifecycle lines.
- Do not change Goal 249 liveness/health behavior.
- The bridge only prunes actions still in its queue; completed/active Codex
  turns are never modified.
- SSE remains live-only; reconciliation remains the canonical recovery path.

## Verification

1. A long-lived project watch emits the same plan handoff's first and second
   request cycles once each after request → receive → reject → request.
2. Repeated reconciliation before a goal handoff receive may queue approvals,
   but the receive action removes those pending approvals and later idle events
   do not start them.
3. An approval for a different goal and ordinary lifecycle actions remain FIFO
   and are not pruned.
