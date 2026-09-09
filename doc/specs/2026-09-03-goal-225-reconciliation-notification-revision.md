# Goal 225: reconciliation-first notification revision

## Supersession

This document supersedes the durable-delivery portions of Decision 5 and Task 4 in `2026-09-02-goal-225-execution-flow.md` and its plan. It does not change the handoff state machine, commander-only completion, human review, or the independently pending GoalDetail `goal_review` UI plan.

## Decision

`workflow_event_outbox`, project event sequences, and `watch_delivery_cursors` are not part of the correctness contract. Agent recovery is a scoped reconciliation of the canonical handoff, goal, task, and decision records. SSE is a best-effort wake-up hint only; a lost, duplicated, or reordered live notification must not affect the agent's eventual understanding or next authorized action.

This follows the measured #613 failure and the recovery requirement: a restarted agent must understand the current authority and relevant history, not merely replay transitions it did not render. An outbox cursor can identify a missed event but cannot substitute for that reconstruction.

## Recovery contract

On process start, reconnect, wake-up discrepancy, and a bounded periodic check, a monitor fetches one scoped reconciliation snapshot before it acts.

For a goal scope, the snapshot contains:

- the current goal and all its tasks, statuses, claims, and current role evidence;
- all goal, plan, and task handoff records in that goal scope, including received, review, completed, rejected, and reopened records with their reports/timestamps;
- the goal's decision history, including open, answered, approved, rejected, and applied records with answer/reason and timestamps; and
- the currently actionable subset (open/unapplied decisions and received/review handoffs), explicitly derived from those canonical records rather than from delivery history.

A project scope has the same data for its project, with deterministic pagination/order where the history is too large for one response. A task scope contains its task, parent goal/role evidence, its own handoffs and decisions, and the goal-level records necessary to interpret authority. The client completes every page of its snapshot before rendering an actionable wake-up and performs the next action only against the normal store/RPC authorization checks.

The snapshot is a state-and-history read, not an acknowledgement. There is no watcher cursor, render acknowledgement, event identity, high-watermark, replay, stale-cursor response, or exactly-once/at-least-once delivery claim.

## Live notification behavior

State transitions still publish the existing in-memory/SSE notification after commit as a low-latency hint. The channel may drop notifications for slow or disconnected subscribers. On any received live notification the watcher schedules a scoped reconciliation; it does not treat the event payload as authoritative and does not require an event ID for deduplication. Repeated hints may coalesce into one pending reconciliation.

If no live notification arrives, the periodic reconciliation supplies the recovery path. If a reconciliation fails, the watcher reports the failure and retries; it does not advance durable delivery state because none exists.

## #613 and reopened-handoff consequences

A completion `rejected -> applied` transition like #613 is recovered because the decision-history portion of the scoped snapshot includes the applied/rejected outcome and reason, even though it is no longer an inbox `unapplied_decision`. A completed-handoff reopen is recovered because both the prior completed handoff and the new request/receipt appear in the scoped handoff history. The monitor derives the present required action from the current records; it does not infer it from a missing event sequence.

## Removal and compatibility boundaries

The implementation removes the Goal 225-owned durable-delivery machinery:

- migration 0025's `project_event_sequences`, `workflow_event_outbox`, and `watch_delivery_cursors` data through a new forward-only migration; never rewrite an already-applied migration;
- `internal/store/workflow_events.go`, related sqlc queries/models, transactional `persistWorkflowEvent` calls, outbox retention, and cursor aliases;
- HTTP event replay, reconciliation event/high-watermark fields, cursor lookup/acknowledgement, and stale-cursor (`410`) behavior; and
- watch cursor/replay/dedup paths and their outbox-specific tests.

No outbox record is domain state. Upgrade has no business-data migration: after the replacement snapshot path is verified, old outbox/cursor rows may be dropped. The release must update daemon and watch/monitor clients together. During a deliberately supported mixed-version window, removed cursor query parameters may be ignored and acknowledgement may be a documented no-op; an old client must never cause an outbox table to become required again. The final removal occurs only after that compatibility window is closed.

## Non-goals

- This revision does not modify a handoff, decision, role, goal/task lifecycle, or the GoalDetail UI plan.
- It does not modify Goal 228. Goal 228 is a downstream consumer of the former delivery contract and must be reconciled against this accepted revision before its implementation/planning resumes.
- It does not promise event-by-event audit delivery. Canonical handoff and decision records remain the historical audit source.

## Acceptance criteria

- A fresh, disconnected, or restarted monitor reaches the same actionable role/next-state conclusion from a scoped canonical snapshot as a continuously connected monitor.
- #613-style applied rejection and a completed-handoff reopen are visible from snapshot history without an outbox row or cursor.
- Dropped, duplicated, and reordered SSE hints result only in coalesced extra reconciliations, never skipped state or duplicate state transitions.
- No database table, query, HTTP API, or CLI monitor contract retains an outbox/cursor correctness dependency after the compatibility window.
