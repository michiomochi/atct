# Handoff Model Consumers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every non-`web/dist` consumer and test fixture use open handoffs as the source of task and goal ownership, so the full Go test suite reaches only the known embedded-web failure.

**Architecture:** Keep ownership out of `domain.Task`. Store consumers load open task handoffs once per goal and derive the owner from `received_by`, falling back to `requested_by`. Tests register every session before a handoff-backed claim and replace direct `claimed_by` SQL with handoff rows. Project claims remain the commander source and retain their existing project column.

**Tech Stack:** Go, `database/sql`, SQLite migrations, SQLC-generated queries, package tests.

## Global Constraints

- Do not add `ClaimedBy` or `ClaimedAt` to `domain.Task`.
- Do not modify `web/` TypeScript or embedded-web behavior; ignore only the missing `web/dist` fixture failure.
- Do not delete or weaken tests.
- Do not commit.
- Use `RegisterAgentSession` in fixtures before `ClaimGoal`, `ClaimTask`, or handoff requests that require a registered owner.

---

### Task 1: Update CLI context and pending consumers

**Files:**
- Modify: `cmd/atct/context.go`
- Modify: `cmd/atct/context_test.go`
- Modify: `cmd/atct/pending.go`
- Modify: `cmd/atct/pending_test.go`

**Interfaces:**
- Consume: `Store.ListOpenTaskHandoffsForGoal`, `store.TaskHandoff`, and existing task lists.
- Produce: context and pending output with the same user-facing status text, derived from open handoffs.

- [ ] Load one open-handoff map per active goal in `loadContextSnapshotForProject` and store it on `contextGoal`.
- [ ] Replace every CLI `domain.Task.ClaimedBy` read with the handoff owner (`ReceivedBy`, otherwise `RequestedBy`).
- [ ] Replace the stale-task owner comparison in `pendingTextForProject` with a batch handoff map.
- [ ] Replace test struct literals and release assertions with handoff assertions.
- [ ] Run `go test ./cmd/atct`.

### Task 2: Update end-to-end response assertions

**Files:**
- Modify: `internal/e2e/full_flow_test.go`

**Interfaces:**
- Consume: daemon task responses as `domain.Task` plus `Store.ListOpenTaskHandoffsForGoal`.
- Produce: assertions that verify claim ownership and release through the persisted handoff, without domain claim fields.

- [ ] Keep daemon task response decoding as `domain.Task` for task identity and status.
- [ ] Query the open task handoff after `task.claim` and assert receiver/timestamp there.
- [ ] Query the handoff after completion and assert it is closed.
- [ ] Use an assertion-only struct or the store handoff for concurrent claim ownership.
- [ ] Run `go test ./internal/e2e`.

### Task 3: Repair daemon, HTTP, MCP, and daemon-web fixtures

**Files:**
- Modify: `internal/daemon/server_test.go`
- Modify: `internal/daemon/handler_test.go`
- Modify: `internal/daemon/pending_response_test.go`
- Modify: `internal/httpapi/server_test.go`
- Modify: `internal/mcpshim/schema_test.go`
- Modify: `internal/daemon/web_test.go`

**Interfaces:**
- Consume: existing fixture setup and `RegisterAgentSession`.
- Produce: handoff-backed claims with valid foreign keys and unchanged JSON contract assertions.

- [ ] Register `claimed-agent`, `contract-claimed`, `fixture-run`, `other-run`, and every other direct claim session before the claim call.
- [ ] Ensure shared fixtures register sessions once at their setup boundary, preserving tests that intentionally register live/dead sessions themselves.
- [ ] Keep web tests unchanged except for their handoff fixture setup if required by the failure.
- [ ] Run package-specific daemon, HTTP, MCP, and daemon-web tests.

### Task 4: Migrate store fixtures and fix handoff-aware file conflicts

**Files:**
- Modify: `internal/store/goal_approval_test.go`
- Modify: `internal/store/goal_withdraw_test.go`
- Modify: `internal/store/task_handoff_test.go`
- Modify: `internal/store/task_order_migration_test.go`
- Modify: `internal/store/task_test.go`
- Modify: `internal/store/wakeup_test.go`
- Modify: `internal/store/task.go`

**Interfaces:**
- Consume: handoff helpers and registered sessions.
- Produce: store tests and file-conflict checks that distinguish open handoffs by owner without reading removed task columns.

- [ ] Register all active claim sessions before `ClaimTask`/`ClaimProject` calls.
- [ ] Replace legacy `tasks.claimed_by` fixture INSERT/UPDATE/SELECT with valid open handoff rows or assertions over `ListTaskHandoffs`.
- [ ] Preserve intentional anomaly fixtures using nullable handoff columns.
- [ ] Make file conflict detection inspect all open task handoffs, while allowing overlapping files for the same receiver.
- [ ] Run `go test ./internal/store`.

### Task 5: Full verification and handoff

**Files:**
- Read only: all changed files and test output.

- [ ] Run `gofmt` on changed Go files and `git diff --check`.
- [ ] Run `go test ./...`.
- [ ] Run the required aggregate command and confirm only the known `web/dist` package failure remains.
- [ ] Report evidence and the uncommitted status to `atct-commander` via `herdr agent prompt atct-commander`.
