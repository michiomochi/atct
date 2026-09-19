# Goal-review lifecycle visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use ATCT executor handoffs task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the human goal-review decision state and the commander-only finalization step visible without changing authorization or lifecycle transitions.

**Architecture:** Reuse the persisted `domain.Decision.Status` and existing goal reconciliation. Add a small lifecycle projection to daemon `goal.get`, forwarded by `atct_goal_get`, for the latest taskless `goal_review`; add a commander-project-only reconciliation event for an active, approved review that still needs `goal.review.complete`. HTTP GoalDetail has a separate response type and is out of scope to avoid duplicating this projection.

**Tech Stack:** Go, existing store/daemon read model, `atct watch`, Go tests.

## Global Constraints

- Preserve Goal 258 decision lifecycle: `open` → `answered` on rejection; `open` → `applied` on approval; goal remains active until commander finalization.
- Preserve Goals 260 and 265: no workflow or stale-action scope changes.
- Do not add a migration, role, MCP tool, configuration, or skill.
- `goal.review.complete` remains commander-only; no read-model value authorizes it.

---

### Task 1: Expose the latest goal-review lifecycle through `atct_goal_get`

**Files:**
- Modify: `internal/daemon/handler.go`
- Test: `internal/daemon/handler_test.go`

**Interfaces:**
- Consumes: `store.ListDecisionsForGoal(ctx, goalID)` and `domain.Decision`.
- Produces: the optional top-level `goal_review` member of the `atct_goal_get` data response, with `decision_id`, `status`, answer metadata, and optional `next_commander_action`.

- [ ] **Step 1: Write failing daemon/MCP response tests**

Create daemon `goal.get` fixtures for the latest taskless `KindGoalReview` in
`open`, rejected `answered`, and approved `applied` states. Assert all three
statuses are returned in top-level `goal_review` and only the active approved
fixture returns:

```go
if got := response.GoalReview.NextCommanderAction; got != "goal.review.complete" {
    t.Fatalf("next commander action = %q, want goal.review.complete", got)
}
```

Also assert a done goal and a non-`approve` applied review expose no action.

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `go test ./internal/daemon -run 'Test.*Goal(Get|Review).*Lifecycle' -count=1`

Expected: FAIL because daemon `goal.get` has no `goal_review` lifecycle
projection.

- [ ] **Step 3: Add the minimal projection**

In daemon `goal.get`, obtain the goal's decisions once, choose the newest
taskless `KindGoalReview`, and map only its persisted fields into an optional
top-level response value. Set `NextCommanderAction` only when:

```go
goal.Status == domain.GoalActive &&
decision.Status == domain.DecisionApplied &&
decision.AnswerLabel == "approve"
```

Do not call finalization and do not add any authorization branch. Do not modify
`internal/httpapi/server.go`; it has no shared goal-get response type.

- [ ] **Step 4: Run focused and package tests**

Run: `go test ./internal/daemon -count=1`

Expected: PASS.

### Task 2: Reconcile a commander-only finalization notification

**Files:**
- Modify: `cmd/atct/watch.go`
- Modify: `cmd/atct/watch_action.go`
- Modify: `cmd/atct/watch_scope.go`
- Test: `cmd/atct/watch_test.go`
- Test: `cmd/atct/watch_action_test.go`
- Test: `cmd/atct/watch_scope_test.go`

**Interfaces:**
- Consumes: reconciled `watchReconciliation.Goals` and `watchDecision` values, including the persisted `answer_label`.
- Produces: a project-scoped monitor action stating `goal.review.complete` is the next commander operation.

- [ ] **Step 1: Write failing reconciliation/scope tests**

Build a reconciliation with an active goal and a taskless `goal_review` whose
status is `applied` and answer label is `approve`. Assert project scope emits
one dedicated lifecycle line containing `goal.review.complete`; goal and task
scopes emit none. Repeat reconciliation against the same state to establish
the normal delivery deduplication behavior.

- [ ] **Step 2: Run the focused watch tests and verify they fail**

Run: `go test ./cmd/atct -run 'Test.*GoalReview.*(Finalization|Lifecycle)' -count=1`

Expected: FAIL because reconciliation only promotes applied `goal_approval`.

- [ ] **Step 3: Add the smallest dedicated projection**

First add `AnswerLabel string \`json:"answer_label"\`` to `watchDecision` so
reconciliation can distinguish an approval from another applied decision.
Extend the existing project-only applied-goal approval reconciliation predicate
to recognize an active, taskless `goal_review` with `status == "applied"` and
`answer_label == "approve"`. Format and select a distinct lifecycle action
whose text names the goal and says the commander must call
`goal.review.complete`. Keep its scope project-only and leave generic decision
events unchanged.

- [ ] **Step 4: Run watch package tests**

Run: `go test ./cmd/atct -count=1`

Expected: PASS.

### Task 3: Guard the authority and full lifecycle regression

**Files:**
- Modify: `internal/daemon/goal_handoff_test.go`
- Test: `internal/daemon/goal_handoff_test.go`

**Interfaces:**
- Consumes: `goal.review.complete` RPC with commander and subcommander session identities.
- Produces: regression evidence that visibility did not create a finalization capability.

- [ ] **Step 1: Strengthen the existing named goal-review lifecycle test**

Keep the existing approved-review setup and assert the subcommander call fails
with a commander authorization error before asserting the commander call makes
the goal done:

```go
if err == nil || !strings.Contains(err.Error(), "commander") {
    t.Fatalf("subcommander finalization error = %v, want commander denial", err)
}
```

- [ ] **Step 2: Run the focused daemon test**

Run: `go test ./internal/daemon -run TestNamedGoalReviewRequiresCommanderAndHumanApprovalOrdering -count=1`

Expected: PASS.

- [ ] **Step 3: Run the full relevant verification set**

Run: `go test ./internal/httpapi ./internal/daemon ./cmd/atct -count=1 && git diff --check`

Expected: PASS with no whitespace errors.

- [ ] **Step 4: Submit the task reviews**

Each executor submits its completed task through the ATCT task-handoff review;
the subcommander reviews and accepts before committing the goal work.

## Plan self-review

- Spec coverage: Tasks 1–3 cover the three statuses, the applied finalization
  cue, project-only monitor recovery, and retained commander authorization.
- Placeholder scan: no incomplete or undefined implementation step remains.
- Type consistency: all tasks use existing `domain.Decision`,
  `domain.DecisionStatus`, `KindGoalReview`, and `goal.review.complete` names.
