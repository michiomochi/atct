# Review Receive Transition Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject delayed or duplicate review-receive mutations after a review rejection, while preserving rejection receipt and reissue cycles.

**Architecture:** The three lifecycle mutations share SQL query sources in `internal/store/queries/task.sql`. Restrict each mutation to an unrejected review cycle; the existing Store zero-row handling returns the existing required-state error without a new API path. Existing lifecycle tests are extended at the rejected state and retain their valid reissue tail.

**Tech Stack:** Go, SQLite, sqlc, Go standard testing.

## Global Constraints

- Change only Goal 268's store mutation transition guard and its lifecycle tests.
- Do not alter monitor delivery/reconciliation behavior owned by Goal 265.
- Do not add dependencies or new error types.

---

### Task 1: Guard rejected review cycles across task, goal, and plan

**Files:**
- Modify: `internal/store/queries/task.sql:286-294,423-431,535-542`
- Modify: `internal/store/sqlcgen/task.sql.go` (regenerated)
- Modify: `internal/store/task_handoff_test.go:232-330`
- Modify: `internal/store/goal_handoff_test.go:170-260`
- Modify: `internal/store/plan_handoff_test.go:43-120`

**Interfaces:**
- Consumes: `ReceiveTaskHandoffReview`, `ReceiveGoalHandoffReview`, and `ReceivePlanHandoffReview`, each of which maps a zero-row mutation to its existing required-state error.
- Produces: review receive only advances a currently open, unrejected review cycle; reissue opens the following legal cycle.

- [ ] **Step 1: Add failing lifecycle assertions after rejection**

In each existing lifecycle test, immediately after `Reject*HandoffReview` returns,
save the rejected handoff and call the matching `Receive*HandoffReview` using the
previously valid reviewer. Assert `errors.Is(err, Err*HandoffReviewState)`, reload
the handoff, and assert `ReviewReceivedBy`, `ReviewReceivedAt`, `ReviewRejectedAt`,
and `ReviewRejectReport` are unchanged. Leave the existing rejection-receive,
reissue, valid receive, and completion assertions after this new check.

- [ ] **Step 2: Run the focused store tests and observe the regression**

Run: `go test ./internal/store -run 'Test(PlanHandoffReviewRejectReceiveLifecycle|GoalHandoffReviewRejectReceiveLifecyclePreservesGoalClaim|TaskHandoffReviewRejectReceiveLifecycleUpdatesTaskStatusAndPreservesClaim)$' -count=1`

Expected: FAIL because the delayed receives currently succeed and write a fresh
review receipt.

- [ ] **Step 3: Add the shared mutation guard and regenerate sqlc**

Add `AND review_rejected_at IS NULL` to each of `ReceiveTaskHandoffReview`,
`ReceiveGoalHandoffReview`, and `ReceivePlanHandoffReview` in
`internal/store/queries/task.sql`. Run `go tool sqlc generate` so the committed
generated query constants match the source.

- [ ] **Step 4: Run focused and generated-schema checks**

Run: `go test ./internal/store -run 'Test(PlanHandoffReviewRejectReceiveLifecycle|GoalHandoffReviewRejectReceiveLifecyclePreservesGoalClaim|TaskHandoffReviewRejectReceiveLifecycleUpdatesTaskStatusAndPreservesClaim)$' -count=1`

Expected: PASS; each rejected-cycle receive fails without changing persisted
review fields, and each existing reissue cycle reaches completion.

Run: `script/schema-check.sh`

Expected: PASS; sqlc generation leaves no uncommitted generated-code difference.

- [ ] **Step 5: Run the package regression suite**

Run: `go test ./internal/store -count=1`

Expected: PASS.
