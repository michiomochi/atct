# Goal 279: Codex monitor rejection delivery

## Problem

An unreceived `plan|goal|task.handoff.review.reject` action can disappear
between the Codex bridge queue and `turn/start`.  The watcher has already put
the action in its per-lifecycle `wakeupDelivered` map, so reconciliation will
not offer that generation again.  The bridge currently removes the queue head
when `StartTurn` returns `errCodexTurnSubmitUnknown`, or when the App Server
connection closes before a response is known, even though no
`turn/started` notification has confirmed that Codex accepted it.  A later
liveness turn can therefore arrive while the rejection action never did.

This is not a monitor-binding or reconciliation selection failure:

- monitor binding retains the received, incomplete goal/task handoff scope;
- reconciliation projects an unreceived rejection to its submitter with the
  rejection timestamp as generation;
- `selectWatchAgentAction` makes rejection agent-facing and makes only
  `*.handoff.review.receive` control-only;
- the bridge's review-generation logic accepts a later rejection after a
  receipt and preserves a distinct handoff.

Focused tests covering those boundaries pass.  The loss is the remaining
unacknowledged bridge-to-app-server boundary.

## Protocol boundary

`turn/started` cannot acknowledge a particular bridge submission.  It carries
only `threadId` and the server-created `turn.id`; an unknown `turn/start`
response supplies no expected turn ID.  A human or another Codex client can
start a turn on the same thread between the bridge write and the notification.
Therefore matching by thread would falsely dequeue an unreceived rejection.

The current App Server interface has no client-provided idempotency key or
request-to-notification correlation field.  With that interface, an unknown
result has two indistinguishable realities: the server accepted the turn and
the response was lost, or it never accepted the turn.  Exact-once retry is not
implementable at this boundary.

## Decision

Choose safe at-most-once submission over an unprovable retry:

1. Only a successful `turn/start` response with its non-empty `turn.id` may
   dequeue the reserved head.
2. On `errCodexTurnSubmitUnknown` or an App Server-close error, do not use
   `turn/started` as a proxy and do not retry or dequeue the head.  Mark the
   bridge terminal, propagate a `watchSinkError`, and let the supervisor print
   its existing disabled-monitor diagnostic. Ordinary known transient failures
   retain their existing retry behavior.
3. The durable unreceived rejection remains in the handoff table.  Recovery is
   an explicit fresh monitor lifecycle: its reconciliation selects the current
   rejection generation once.  That restart is operator-authorized because it
   may create a second turn if the original unknown submission actually reached
   Codex.
4. `*.handoff.review.receive` remains control-only and may continue to prune
   only older queued review actions for the same handoff.

This removes the silent loss: each monitor makes one confirmed submission or
stops with a visible uncertain-delivery failure.  It does not falsely claim an
exact-once end-to-end guarantee that the App Server cannot provide.

## Alternatives considered

1. Treat same-thread `turn/started` as acknowledgement.  Rejected because it
   has no request correlation and can acknowledge another actor's turn.
2. Persist a cross-process delivery cursor or add App Server idempotency.
   This is the only route to an exact-once protocol but expands storage and the
   App Server contract beyond this minimal repair.
3. Retry every unknown submission immediately.  Rejected because it can create
   duplicate Codex turns when the server accepted the request but its response
   was lost.

## Verification

Add bridge tests for plan, goal, and task rejection actions that simulate:

- a foreign same-thread `turn/started` before an unknown response: the bridge
  must not dequeue the rejection based on that notification;
- an unknown `turn/start`: the watcher terminates with a delivery-uncertain
  error, the supervisor disables the monitor, and no automatic retry creates a
  duplicate turn;
- an App Server-close error during `turn/start`: the reserved head remains
  queued and a later idle notification does not submit it again;
- a fresh monitor reconciliation after that failure selects the durable,
  unreceived rejection generation once;
- a control-only review receipt followed by a newer rejection: the stale
  request remains suppressed and the rejection is delivered once;
- a fresh bound subcommander/executor scope reconciling a rejection alongside
  liveness: rejection is the first bridge turn and liveness remains queued.

No store schema, monitor binding, watcher selection, or daemon API changes are
required.
