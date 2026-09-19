# Goal 292: executor launch liveness

## Problem

A task handoff can be recorded with `RequestedAt` set while `ReceivedAt` is
still nil.  That is the state left when a subcommander requests an executor
but never starts a monitored executor pane.  The task remains `todo`, while the
current subcommander liveness logic treats every open task handoff as executor
work and suppresses its reminder.  The stop check also allows the
subcommander to stop because it only checks received child work and review
handoffs.

## Outcome

For a task handoff requested by the current subcommander:

1. The subcommander stop check blocks while the handoff is request-only.
2. The goal-scoped subcommander monitor marks the state actionable, so the
   existing liveness prompt reminds the subcommander to start a monitored
   executor.
3. Once the executor receives the handoff, the existing executor-in-progress
   suppression remains unchanged.

The canonical condition is `RequestedAt != nil && ReceivedAt == nil`, with the
handoff still open (not completed or recovered).

## Boundaries

- Do not add or repair session heartbeat data; unknown session liveness is a
  separate recovery problem.
- Do not launch panes, recover handoffs, reassign tasks, or mutate task state
  automatically. The guard exposes the required human/agent action through
  the existing stop-check and monitor paths.
- Do not change behavior for other roles or other goals.
- Do not alter the project-level `pending` presentation; its project-wide
  output is not the role-scoped enforcement path.

## Verification

Add regression coverage for a request-only child task handoff in the
subcommander stop check and scoped liveness decision. Retain coverage proving
that a received task handoff is not treated as a launch reminder. Run the
focused daemon and `cmd/atct` tests, then the full tests for those two
packages.
