# Goal 290: Token-Minimal ATCT Delivery Design

## Decision

Reduce repeated, static instructions first.  Do not change persisted lifecycle
events, the reconciliation endpoint, or agent-facing action membership.  The
current monitor already suppresses review receipts as control-only actions and
uses persisted generations to discard superseded queued review work; replacing
that mechanism would trade a small theoretical saving for lost recovery.

Decision 787 selected a plan before executor research.  This document is
therefore based on the repository's read-only evidence, not a production token
trace.

## Evidence-ranked sources

| Rank | Source | Evidence | Expected reduction | Decision |
| --- | --- | --- | --- | --- |
| 1 | Re-read skill/prompt text after compaction | `skills/atct/SKILL.md` is 42,442 bytes; `skills/start/SKILL.md` is 8,582 bytes. Orchestration records that compaction re-reads skills and can itself trigger another compaction. | Shrinking the active ATCT skill to a concise operational contract is the only durable way to reduce this harness-controlled repeat. A 6 KiB target saves about 36 KiB (85%) per re-read of the current ATCT skill. | Implement, preserving all state and authority invariants in the concise text; rationale remains in `doc/execution-flow.md`. |
| 2 | MCP initialize instructions | `internal/mcpshim/instructions.go` is 1,009 bytes and is installed by both `cmd/atct-mcp/main.go:65-68` and `internal/daemon/server.go:72-75`. Tool catalogs expose this shared instruction text with the ATCT tools. | Cap at 350 bytes: save at least 659 bytes (65%) per MCP initialization/catalog exposure. | Implement a concise invariant-only instruction. |
| 3 | Handoff/review reconciliation | `cmd/atct/watch.go:1403-1427` projects one latest lifecycle state; `watch_action.go:40-66` marks `*.handoff.review.receive` control-only; `codex_monitor.go:695-737` prunes older queued review actions by RFC3339Nano generation. | No safe additional reduction is demonstrated. | Retain unchanged. |
| 4 | Event payloads | Agent delivery uses formatted ID-only lines in `cmd/atct/watch.go:1677-1727`; full reconciliation/SSE payloads remain transport data and are not directly injected as action prompts. | No safe reduction is demonstrated without losing audit/recovery data. | Retain unchanged. |
| 5 | Periodic hook/notification work | `hooks/stop` only invokes `atct stop-check`; `watch.go:668-760` retains delivery state across reconnects, and `watch.go:1494-1602` suppresses equal delivery keys/generations. | No safe reduction is demonstrated. | Retain unchanged. |

Byte counts were measured with:

```sh
wc -c internal/mcpshim/instructions.go hooks/codex-hooks.json hooks/stop hooks/pre-ask \
  skills/{atct,subcommander,executor,start}/SKILL.md cmd/atct/watch.go cmd/atct/watch_delivery.go
```

The command reported `42,442` bytes for `skills/atct/SKILL.md`, `8,582` for
`skills/start/SKILL.md`, and `1,009` for `internal/mcpshim/instructions.go`.

## State and safety contract

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

## Implementation boundary

Touch only `internal/mcpshim/instructions.go`, its initialization contract
tests, and the canonical `skills/atct/SKILL.md` (with the skill-writing
workflow).  Do not alter `cmd/atct/watch*.go`, `cmd/atct/codex_monitor.go`,
event schemas, hook execution cadence, or database state.

## Regression observation

The implementation must record before/after byte counts for the MCP
instruction and active skill.  Tests must prove that MCP initialize exposes
the concise instructions, and existing focused watch/Codex tests must keep
proving review-receipt control delivery, generation ordering, queued-action
pruning, reconnect reconciliation, and human-decision visibility.
