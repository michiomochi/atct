# Decision owner routing

## Problem

Decision 712 was created by Goal 240's subcommander session 5602, but a
project-scoped commander monitor received `decision.answered`. The watcher
currently treats every human-answered Decision as project-visible. A direct
`decision.poll(id)` can also transition another session's Decision to
`applied`.

## Scope

Goal 258 changes only Decision `answered`/`applied` delivery and consumption.
It does not change Goal 176's goal-snapshot filter, Goal 248's self-notification
rules, or Goal 255's plan-handoff role routing.

## Design

The Decision's `agent_session_id` is the authoritative owner.

1. A direct poll may apply a Decision only when its caller is that owner
   session. A foreign session leaves the Decision answered.
2. Reconciliation projects an owner role derived from the owner session's live
   claim: commander for a project claim, subcommander for an open goal handoff,
   otherwise executor. The Decision's existing goal/task IDs provide its scope.
3. Watch reconciliation emits `decision.answered` only when the monitor role
   equals the projected owner role and its project/goal/task scope matches the
   Decision. Live SSE and reconnects already reconcile through this one path;
   the Codex bridge receives only actions emitted by that path.
4. An owner that is the commander remains deliverable to the commander monitor.
   Unknown or no-session Decisions are not broadened to a project commander.

## Verification

Focused tests cover a subcommander-owned decision being suppressed for a
project commander and delivered to its goal subcommander through both initial
reconciliation and the SSE-triggered reconciliation; the Codex action sink
must see the same result. Poll tests prove that a foreign commander cannot
apply the decision and its owner can.
