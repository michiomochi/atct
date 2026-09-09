# Decision Owner Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver answered/applied Decisions only to their owner session's role and scope, and prevent foreign polling from consuming them.

**Architecture:** Keep one owner session on `domain.Decision`. Project its current role alongside reconciliation decisions, then have the existing watcher reconciliation path select it by monitor role plus goal/task scope. Enforce the same owner identity before a direct poll mutates state.

**Tech Stack:** Go, SQLite/sqlc, HTTP reconciliation, SSE, Codex monitor bridge, Go tests.

## Global Constraints

- Change only Decision answered/applied routing and consumption.
- Do not modify Goal 176 snapshot behavior, Goal 248 self-notification behavior, or Goal 255 plan-handoff routing.
- Reuse reconciliation for startup, SSE, reconnect, and Codex bridge; do not add a second delivery path.
- Use focused `go test` package commands only.

---

### Task 1: Owner-scoped Decision delivery and polling

**Files:**

- Modify: `internal/store/workflow_events.go`
- Modify: `internal/daemon/handler.go`
- Modify: `cmd/atct/watch.go`
- Modify: `cmd/atct/watch_scope.go`
- Test: `internal/daemon/decision_poll_scope_test.go`
- Test: `internal/daemon/pending_response_test.go`
- Test: `cmd/atct/watch_scope_test.go`
- Test: `cmd/atct/watch_test.go`
- Test: `cmd/atct/codex_monitor_test.go`

**Interfaces:**

- Consumes: `domain.Decision{AgentSessionID, GoalID, TaskID, Status}` and `watchScope{Role, ProjectID, GoalID, TaskID}`.
- Produces: reconciliation Decision projections with `target_role`, and a poll result that leaves a foreign-owned Decision `answered`.

- [ ] **Step 1: Write the failing focused tests**

  Add fixtures for a subcommander-owned answered Decision and a commander-owned
  answered Decision. Assert a project commander watcher suppresses the first
  but receives the second; a matching goal subcommander watcher receives the
  first. Exercise direct reconciliation and SSE-triggered reconciliation, and
  assert the Codex bridge action sink receives no suppressed action. Change the
  foreign direct-poll assertion to require refusal/no transition, then assert
  the owner poll transitions to `applied`. Update the existing pending-response
  fixture so the session polling its decision is that Decision's owner; keep
  its assertion that the separate answered Decision remains in the response.

- [ ] **Step 2: Run the tests to verify the regression**

  Run: `GOCACHE=/private/tmp/goal258-go-cache go test ./cmd/atct ./internal/daemon ./internal/store -run 'Test(WatchScopeProjectDeliversHumanDecisionAnswered|DecisionPollByOwnerSucceedsAfterRefusal|DecisionPollForCommanderAcceptsOtherGoalDecision|CodexMonitor.*Decision)' -count=1`

  Expected: FAIL because project scope currently delivers any human
  `decision.answered` and a foreign direct poll applies it.

- [ ] **Step 3: Implement the smallest shared selector**

  Project each reconciliation Decision's target role from its `agent_session_id`
  using session-role precedence: project claim, then open goal handoff,
  otherwise executor. Preserve Decision goal/task IDs as scope. In
  `reconcileWatchScope`, reject Decision events when the projected role differs
  from `watchScope.Role`; retain existing project/goal/task checks. Ensure
  explicit monitor scopes carry their configured role. Do not alter handoff
  projection roles or snapshot filtering.

  Before calling the mutating store poll for a requested Decision ID, reject a
  caller whose session ID differs from `decision.AgentSessionID`; a rejected
  call must not apply it. Keep polling the owner's own Decision unchanged.

- [ ] **Step 4: Run the focused tests**

  Run: `GOCACHE=/private/tmp/goal258-go-cache go test ./cmd/atct ./internal/daemon ./internal/store -run 'Test(WatchScope|Watch.*Decision|DecisionPoll|CodexMonitor.*Decision)' -count=1`

  Expected: PASS.

- [ ] **Step 5: Subcommander commits accepted work**

  ```bash
  git add internal/store/workflow_events.go internal/daemon/handler.go internal/daemon/decision_poll_scope_test.go internal/daemon/pending_response_test.go cmd/atct/watch.go cmd/atct/watch_scope.go cmd/atct/watch_scope_test.go doc/plans/2026-09-09-decision-owner-routing.md
  git commit -m "fix: route decision notifications to their owner"
  ```
