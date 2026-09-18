# Goal 290 Token Minimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut repeated static ATCT context while retaining human approval, actionable notifications, audit data, and recovery.

**Architecture:** Keep the persisted event and monitor-delivery model intact.  Replace only repeated static guidance with a concise invariant contract, and make its size and initializer delivery observable in focused tests.

**Tech Stack:** Go, MCP Go SDK, repository Markdown skills, `go test`.

## Global Constraints

- Do not modify lifecycle event schemas, `cmd/atct/watch*.go`, `cmd/atct/codex_monitor.go`, hooks, or storage.
- Never remove human final approval, an actionable decision question/ID, audit reports, reconciliation, or stale-owner recovery.
- `*.handoff.review.receive` remains control-only and may only prune an older queued action for the same handoff and persisted generation.
- Use `superpowers:writing-skills` before editing `skills/atct/SKILL.md`; do not create `reference/`.

---

### Task 1: Cap MCP fixed instruction context

**Files:**
- Modify: `internal/mcpshim/instructions.go`
- Modify: `internal/daemon/handler_test.go:TestContractB6SessionStartHookMovesFixedInstructionsToMCP`
- Test: `internal/daemon/handler_test.go`

**Interfaces:**
- Consumes: `mcpshim.Instructions`, installed by `cmd/atct-mcp/main.go` and `internal/daemon/server.go`.
- Produces: an initialize instruction string no longer than 350 UTF-8 bytes.

- [ ] **Step 1: Write the failing size-and-content assertion**

Add to `TestContractB6SessionStartHookMovesFixedInstructionsToMCP`:

```go
if got := len(mcpshim.Instructions); got > 350 {
    t.Fatalf("MCP instructions are %d bytes, want <= 350", got)
}
for _, want := range []string{"role", "handoff", "human decision"} {
    if !strings.Contains(strings.ToLower(mcpshim.Instructions), want) {
        t.Fatalf("MCP instructions omit %q: %q", want, mcpshim.Instructions)
    }
}
```

- [ ] **Step 2: Verify it fails**

Run: `go test ./internal/daemon -run TestContractB6SessionStartHookMovesFixedInstructionsToMCP -count=1`

Expected: FAIL because the current 1,009-byte instruction exceeds 350.

- [ ] **Step 3: Replace fixed prose with the invariant-only contract**

In `internal/mcpshim/instructions.go`, replace the current prose with one
short string that says: follow daemon-derived role and handoff state; receive
before work and request review before completion; route human choices through
ATCT; consult the `atct` skill for detailed workflow.  It must not state that
decisions auto-apply or omit human approval.

- [ ] **Step 4: Verify the focused contract**

Run: `go test ./internal/daemon -run TestContractB6SessionStartHookMovesFixedInstructionsToMCP -count=1`

Expected: PASS; the initialize response equals `mcpshim.Instructions`, the
session-start hook does not duplicate it, and the 350-byte cap holds.

### Task 2: Compact the active ATCT skill without changing its contract

**Files:**
- Modify: `skills/atct/SKILL.md`
- Test: `skills/atct/SKILL.md` (manual invariant checklist)

**Interfaces:**
- Consumes: `doc/execution-flow.md` as the canonical detailed state diagram.
- Produces: a concise operational skill that links to the detailed document
  and retains mandatory role, handoff, review, decision, and recovery rules.

- [ ] **Step 1: Invoke the skill-writing workflow and write the failing checklist**

Before editing, invoke `superpowers:writing-skills`.  Record this checklist in
the task report and treat any missing item as failure:

```text
role derivation; receive before work; task-create after accepted plan;
task review request/receive/complete order; human decision only through ATCT;
stale-owner-only recovery; received handoff is never self-closed
```

- [ ] **Step 2: Verify the current baseline**

Run: `wc -c skills/atct/SKILL.md`

Expected: baseline near 42,442 bytes; retain the exact result in the task
report before changing the file.

- [ ] **Step 3: Write the compact skill**

Keep only operational, non-negotiable rules and one link to
`doc/execution-flow.md` for the full transition table.  Preserve the checklist
verbatim in meaning, omit historical anecdotes and duplicated rationale, and
do not create a `reference/` directory.  Target at most 6,144 bytes.

- [ ] **Step 4: Verify size and contract**

Run: `wc -c skills/atct/SKILL.md && rg -n 'role|receive|review|decision|stale|recover|task-create|plan' skills/atct/SKILL.md`

Expected: at most 6,144 bytes and each checklist concept visibly present.

### Task 3: Prove notification and recovery behavior stayed intact

**Files:**
- Test: `cmd/atct/watch_action_test.go`
- Test: `cmd/atct/codex_monitor_test.go`
- Test: `cmd/atct/watch_test.go`
- Test: `internal/mcpshim/notifications_test.go`

**Interfaces:**
- Consumes: unchanged action selection in `cmd/atct/watch_action.go` and queue
  control handling in `cmd/atct/codex_monitor.go`.
- Produces: verification evidence only; no source changes unless an existing
  test exposes an unintended regression from Tasks 1-2.

- [ ] **Step 1: Run action-membership and queue tests**

Run: `go test ./cmd/atct -run 'Test.*(Review.*Receive|Control.*Review|Queued.*Review)' -count=1`

Expected: PASS; review receipt is not a prompt but removes only superseded
queued review work for its handoff.

- [ ] **Step 2: Run reconciliation and MCP decision-payload tests**

Run: `go test ./cmd/atct ./internal/mcpshim -run 'Test.*(Reconcile|Reconnect|UnappliedDecisions|Notification)' -count=1`

Expected: PASS; reconnect reconciliation remains available and an unapplied
decision still includes both `decision_id` and `question`.

- [ ] **Step 3: Run the affected packages and record before/after bytes**

Run: `go test ./internal/daemon ./internal/mcpshim ./cmd/atct -count=1 && wc -c internal/mcpshim/instructions.go skills/atct/SKILL.md`

Expected: PASS; report the two final byte counts and percentage reduction
against 1,009 and 42,442 bytes respectively.

## Plan self-review

- Coverage: Tasks 1-2 cover the only measured repeated static sources; Task 3
  verifies the retained notification, audit, recovery, and decision contract.
- No placeholders: every task names files, checks, and expected results.
- Scope: no task adds telemetry, a cache, a lifecycle, or a new dependency.
