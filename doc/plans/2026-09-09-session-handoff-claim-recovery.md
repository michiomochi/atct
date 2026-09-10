# Session Handoff Claim Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recover confirmed-stale handoff ownership without ever taking work from a live session.

**Architecture:** Keep liveness and authorization in Store transitions, expose one typed recovery RPC/MCP adapter, and write append-only audit events. Monitors only project stale candidates from durable state; they never mutate ownership.

**Tech Stack:** Go, SQLite, sqlc, daemon RPC, MCP shim, watch/monitor tests.

## Global Constraints

- `doc/execution-flow.md` is canonical; do not add monitor-derived ownership or reopen a completed handoff.
- Only `claimIsDefinitelyDead` or an approved, committed session-discard revocation can authorize recovery; unknown identity remains protected as live.
- Preserve all existing handoff reports, tasks, Decisions, and idempotency keys.
- Do not start implementation before this plan is accepted through `atct_plan_handoff_review_*`.

---

## File / responsibility map

- `internal/store/claim_liveness.go`, `internal/store/agent_session*.go`: definite-stale/discard proof.
- `internal/store/migrations/`, `schema.sql`, `internal/store/queries/`: append-only recovery audit schema and CAS queries.
- `internal/store/{goal_handoff,goal,task_handoff,task_create_handoff}.go`: kind/phase authorization, Goal 240 completion-lineage guard, and recovery transitions.
- `internal/daemon/handler.go`, `internal/mcpshim/tools.go`: one typed recovery route.
- `internal/store/*_test.go`, `internal/daemon/*_test.go`, `internal/mcpshim/*_test.go`, `cmd/atct/watch*_test.go`: safety and monitor-crash regressions.

### Task 1: Establish stale-proof, human discard, and audit primitives

**Files:**
- Modify: `internal/store/claim_liveness.go`, `schema.sql`
- Create: `internal/store/migrations/0038_handoff_recoveries.sql`, `internal/store/handoff_recovery.go`, `internal/store/handoff_recovery_test.go`, `internal/store/queries/handoff_recovery.sql`

**Interfaces:**
- Produces: `CanRecoverSession(ctx, sessionID) (RecoveryProof, error)`, commander-only discard request/commit, and append-only `HandoffRecovery` rows.

- [ ] **Step 1: Write failing Store cases** for live PID/start match, unknown identity, PID/start mismatch, approved human discard while PID remains live, rejected discard, foreign commander, duplicate discard, and old-session mutation after discard. Assert only PID/start mismatch and approved discard permit recovery.
- [ ] **Step 2: Run RED:** `GOCACHE=/private/tmp/goal262-go-cache go test ./internal/store -run 'Test(SessionDiscard|CanRecoverSession|HandoffRecovery)' -count=1 -v` — expected FAIL because the revocation proof/epoch/audit record is absent.
- [ ] **Step 3: Add the smallest schema and CAS helper.** Add session discard fields, `recovered_at` / `recovery_report` terminal fields to replaceable handoff rows, and `handoff_recoveries` unique on `(handoff_kind, handoff_id, recovered_phase, stale_session_id)`. The approved Decision validation and revocation write are one transaction. Every handoff mutation rejects a discarded caller; each recovery CASes the stored stale actor session plus phase. Insert recovery audit only inside the same transaction that terminalizes the old receiver/requester row and creates its new request; return the existing event on exact retry.
- [ ] **Step 4: Re-run focused tests** with the same command; expected PASS.

### Task 2: Recover only the stale goal/plan actor

**Files:**
- Modify: `internal/store/goal_handoff.go`, `internal/store/goal.go`, `internal/store/queries/goal_handoff.sql`, `internal/store/goal_handoff_test.go`, `internal/store/goal_complete_test.go`
- Test: `internal/daemon/goal_handoff_test.go`, `internal/daemon/goal_complete_guard_test.go`

**Interfaces:**
- Produces: `RecoverGoalHandoff(ctx, handoffID, goalID, callerID, reason)` and `RecoverPlanHandoff(ctx, handoffID, goalID, callerID, reason)`.

- [ ] **Step 1: Write failing tests** for Goal 230 commander reviewer turnover with a live subcommander receiver, discarded goal receiver, stale plan reviewer, stale requester, live original actor, foreign commander, and each old-session late request/receive/review/complete/reject call. Add the Goal 240 fixture: `RequestedBy` is stale, `ReviewRequestedBy == ReceivedBy` is the live subcommander, `ReviewReceivedBy` is the live current commander. Assert `CompleteGoalHandoffByReviewer` succeeds and that same commander then calls `RequestGoalReview` successfully; assert receiver/claim/review fields are unchanged except normal completion.
- [ ] **Step 2: Run RED:** `GOCACHE=/private/tmp/goal262-go-cache go test ./internal/store ./internal/daemon -run 'Test.*(Goal|Plan)Handoff.*(Recover|Lineage)|Test.*GoalReview.*Lineage' -count=1 -v` — expected Goal 240 completion-to-goal-review case fails on the requester/reviewer equality guard.
- [ ] **Step 3: Implement phase-specific CAS updates and the narrow lineage guard.** Require the target project’s current commander, `CanRecoverSession`, and the exact stored old actor session/phase. For stale reviewer, clear only stale review receipt, keep worker receiver and goal claim byte-for-byte unchanged, then resume ordinary review receive. For stale worker receiver, retain all reports, terminalize the old row with `recovered_at`, and create a new canonical goal request; never overwrite the old receiver. Separately, change `goalHandoffHasCommanderReviewCompletion` to accept the goal-review caller and require that caller to equal the durable `ReviewReceivedBy`, rather than requiring `ReviewReceivedBy == RequestedBy`. Append an audit row only for an actual stale-actor recovery; the lineage guard writes none.
- [ ] **Step 4: Re-run focused tests;** expected PASS, and every denied case has unchanged handoff fields plus no audit row.

### Task 3: Recover only the stale task-create/task actor

**Files:**
- Modify: `internal/store/task_create_handoff.go`, `internal/store/task_handoff.go`, `internal/store/task_create_handoff_test.go`, `internal/store/task_handoff_test.go`
- Test: `internal/daemon/task_create_handoff_test.go`, `internal/daemon/task_handoff_test.go`

**Interfaces:**
- Produces: `RecoverTaskCreateHandoff` and `RecoverTaskHandoff`, callable only by the current received goal holder.

- [ ] **Step 1: Write failing tests** for stale executor receive/review/rejection phases, a stale task-create receiver, same-key `task.create` replay after recovery, concurrent recovery, live executor, another goal’s subcommander, and old executor/session late request/receive/review/complete/reject calls after discard.
- [ ] **Step 2: Run RED:** `GOCACHE=/private/tmp/goal262-go-cache go test ./internal/store ./internal/daemon -run 'Test.*(TaskCreate|Task)Handoff.*Recover' -count=1 -v` — expected stale cases fail.
- [ ] **Step 3: Implement recovery without reusing executor identity.** For stale task reviewer, clear only stale review receipt, leave executor receiver/task claim unchanged, and resume subcommander review. For stale executor receiver, retain old handoff/report, terminalize it with `recovered_at`, and require the normal `atct_task_handoff_request` with a new ID. For task-create, terminalize the stale requester/receiver attempt with `recovered_at`, create a normal replacement request, and let the current subcommander ordinary-receive and replay. Completed task-create rows remain history so a revised plan can create another current attempt; the partial unique index permits only one non-completed/non-recovered row per goal. Every path CASes stale actor session/phase and appends audit atomically.
- [ ] **Step 4: Re-run focused tests;** expected PASS and exact retry returns one audit event.

### Task 4: Expose, project, and verify recovery

**Files:**
- Modify: `internal/daemon/handler.go`, `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go`, `internal/store/workflow_events.go`, `cmd/atct/watch.go`
- Test: `internal/daemon/handler_test.go`, `internal/mcpshim/*_test.go`, `cmd/atct/watch*_test.go`

**Interfaces:**
- Produces: `atct_session_discard_request`, `atct_session_discard`, `atct_handoff_recover({handoff_kind, handoff_id, goal_id?, task_id?, reason})`, and a read-only stale-candidate / audit projection.

- [ ] **Step 1: Write failing transport tests** showing recovery session IDs are never accepted from MCP input; discard requires an applied human Decision and target-project commander; and monitor crash / health expiry emits a candidate but cannot call discard or recovery.
- [ ] **Step 2: Run RED:** `GOCACHE=/private/tmp/goal262-go-cache go test ./internal/daemon ./internal/mcpshim ./cmd/atct -run 'Test.*(HandoffRecover|Monitor.*Recovery)' -count=1 -v` — expected missing route/projection failures.
- [ ] **Step 3: Add one typed adapter and read-only reconciliation projection.** Route caller identity from the MCP session, dispatch only to the matching Store method, and include audit metadata in workflow history. Do not add a monitor mutation path or compatibility aliases.
- [ ] **Step 4: Run final verification:**
  ```bash
  GOCACHE=/private/tmp/goal262-go-cache go test ./internal/store ./internal/daemon ./internal/mcpshim ./cmd/atct -run 'Test.*(HandoffRecover|ClaimLiveness|Monitor.*Recovery)' -count=1 -v
  go test ./internal/store ./internal/daemon ./internal/mcpshim ./cmd/atct -count=1
  git diff --check
  ```
  Expected: PASS; no recovery occurs for live/unknown owners or a monitor failure alone.
