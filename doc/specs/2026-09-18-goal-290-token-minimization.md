# Goal 290: Token-Minimal ATCT Delivery Design

## Decision

Classify stop-hook work by the commander's immediate persisted action, rather
than suppressing equal responses. Do not change persisted lifecycle events,
the reconciliation endpoint, or agent-facing action membership. The current
monitor already suppresses review receipts as control-only actions and uses
persisted generations to discard superseded queued review work.

Decision 787 selected a plan before executor research.  This document is
therefore based on the repository's read-only evidence, not a production token
trace.

## Evidence-ranked sources

| Rank | Source | Evidence | Expected reduction | Decision |
| --- | --- | --- | --- | --- |
| 1 | Commander stop-hook overclassification | `internal/daemon/stop_check.go:43-55` blocks the commander on the first `goals.status=active` row, without reading goal/plan handoffs, decisions, or task ownership. Thus received subcommander work and human-wait state generate the same repeated 129-byte stop response although the commander has no immediate operation. | Omit the prompt whenever no persisted commander action exists, while retaining repeated blocks when an immediate action remains. Exact savings depend on the observed number of non-actionable stops; the two-call 129-byte measurement remains the per-response proxy. | Implement a persisted-state actionability classifier; do not deduplicate equal active work. |
| 2 | Re-read skill/prompt text after compaction | `skills/atct/SKILL.md` is 42,442 bytes; `skills/start/SKILL.md` is 8,582 bytes. Orchestration records that compaction re-reads skills and can itself trigger another compaction. | Shrinking the active ATCT skill to a concise operational contract is the only durable way to reduce this harness-controlled repeat. A 6 KiB target saves about 36 KiB (85%) per re-read of the current ATCT skill. | Implement after proving the full invariant checklist with the skill-writing workflow; rationale remains in `doc/execution-flow.md`. |
| 3 | MCP initialize instructions | `internal/mcpshim/instructions.go` is 1,009 bytes and is installed by both `cmd/atct-mcp/main.go:65-68` and `internal/daemon/server.go:72-75`. Tool catalogs expose this shared instruction text with the ATCT tools. | Cap at 350 bytes: save at least 659 bytes (65%) per MCP initialization/catalog exposure. | Implement a concise invariant-only instruction. |
| 4 | Handoff/review reconciliation | `cmd/atct/watch.go:1403-1427` projects one latest lifecycle state; `watch_action.go:40-66` marks `*.handoff.review.receive` control-only; `codex_monitor.go:695-737` prunes older queued review work by RFC3339Nano generation. | No safe additional reduction is demonstrated. | Retain unchanged. |
| 5 | Event payloads | Agent delivery uses formatted ID-only lines in `cmd/atct/watch.go:1677-1727`; full reconciliation/SSE payloads remain transport data and are not directly injected as action prompts. | No safe reduction is demonstrated without losing audit/recovery data. | Retain unchanged. |

Byte counts were measured with:

```sh
wc -c internal/mcpshim/instructions.go hooks/codex-hooks.json hooks/stop hooks/pre-ask \
  skills/{atct,subcommander,executor,start}/SKILL.md cmd/atct/watch.go cmd/atct/watch_delivery.go
```

The command reported `42,442` bytes for `skills/atct/SKILL.md`, `8,582` for
`skills/start/SKILL.md`, and `1,009` for `internal/mcpshim/instructions.go`.

## Stop-hook measurement and boundary

The executed commands were:

```sh
printf '%s' '{"session_id":"01a0b443-b3dc-7bd3-bd26-255920491f9a","stop_hook_active":false}' | atct stop-check --hook-input
printf '%s' '{"session_id":"01a0b443-b3dc-7bd3-bd26-255920491f9a","stop_hook_active":false}' | atct stop-check --hook-input | wc -c
printf '%s' '{"session_id":"01a0b443-b3dc-7bd3-bd26-255920491f9a","stop_hook_active":true}' | atct stop-check --hook-input | wc -c
```

Results were the identical active-goal-handoff block reason, `129` bytes for
the false-state response, and `0` bytes for the true-state response. The
first command was repeated once by the byte measurement: that is two equal
state emissions, not a claim about all historical hook executions.

There is no stop-check event/history table: the local database has
`goals`, `goal_handoffs`, `plan_handoffs`, and related lifecycle tables but no
delivery ledger. It confirms Goal 290 is active and the plan handoff's request,
rejection, and rejection receipt timestamps; it cannot reconstruct historical
stop prompts. Model-specific token counts are unavailable. Bytes are therefore
an explicit transport-prompt proxy, not token counts. The lower-bound estimate
is event count times the exact returned prompt bytes; the harness envelope and
model tokenization are excluded.

`monitor/watch` is not on the stop-hook delivery path. Watch has its own
SSE/reconciliation/action-sink path; the stop hook returns the RPC JSON
directly to the harness. Its generation dedup is useful precedent but not
evidence that stop-check is already deduplicated.

## Commander actionability and safety contract

- Human final approval remains exclusively in the goal-review lifecycle; no
  notification reduction may auto-apply or auto-complete a decision.
- An actionable lifecycle transition remains deliverable once per persisted
  `(handoff, phase, generation)`; review receipt can only be control-only when
  it removes an older queued action for that same handoff.
- Reconnect reconciliation remains authoritative.  Token reduction must never
  delete persisted events, reports, IDs, generations, or replay/recovery data.
- An MCP response with an unapplied decision remains actionable: it retains
  both its ID and question.  Full payload trimming is excluded until measured
  evidence identifies a redundant field that preserves this contract.
- Compact skill text retains: role derivation; receive-before-work; review
  ordering; task-create-after-plan; human decision routing; stale-owner-only
  recovery; and the prohibition on self-closing a received handoff.
- The classifier has no delivery ledger and no new migration. It reads existing
  `goals`, `goal_handoffs`, `plan_handoffs`, `task_create_handoffs`,
  `task_handoffs`, and `decisions` on every stop check. Equal actionable work
  continues to block: this is the guard that keeps the commander available.
- It blocks only for, in priority order: an approved goal-review decision whose
  goal still needs commander merge/finalization/conflict cleanup; an answered
  rejected goal review requiring `goal.handoff.review.reject`; an unreceived
  goal or plan review request for the commander; or an active goal with no
  nonterminal delegated goal handoff, requiring worktree/space/handoff
  preparation. A requested-but-not-yet-received goal handoff is delegated, not
  unassigned; existing liveness/recovery handling remains responsible for a
  stale receiver.
- It does not block for an active goal whose subcommander received the goal
  handoff and is working, an executor-owned task handoff, or an open human
  decision. Those facts are owned by their receiver or the human, not by the
  commander. A new persisted review request, human approval/rejection, or
  unassigned goal is still delivered through the existing monitor/watch action
  path and makes the commander actionable again.
- `stop_hook_active` remains same-cycle recursion prevention. No audit record,
  persisted lifecycle state, human final approval, actionable decision, or
  recovery path is removed.

## Implementation boundary

Touch `internal/daemon/stop_check.go` and focused daemon/CLI/watch tests for
the persisted-state classifier, `internal/mcpshim/instructions.go` and its
initialization contract tests, and the canonical `skills/atct/SKILL.md`
(with the skill-writing workflow). Do not alter `cmd/atct/watch*.go`,
`cmd/atct/codex_monitor.go`, event schemas, hook execution cadence, or database
state.

## Regression observation

The implementation must record before/after byte counts and stop-call counts
by actionability class. Tests must prove the received, review-request,
human-wait, approved-merge/cleanup, rejected-return, and unassigned-goal
transitions; a new actionable persisted event must reach the commander monitor.
Existing focused watch/Codex tests must keep proving review-receipt control
delivery, generation ordering, queued-action pruning, reconnect reconciliation,
and human-decision visibility.
