# Goal 274 Direct-Close State Guard Implementation Plan

**Goal:** Reject normal delegated goal-handoff direct completion before and after review request, while retaining reviewer completion and system recovery paths.

**Architecture:** Centralize the policy in `CompleteGoalHandoff`, because `CompleteGoalHandoffForGoal` resolves then delegates to it. The guard uses the existing delegation predicate and existing system-report constants; no new state or API is needed.

**Tech Stack:** Go, SQLite store, Go standard testing package.

## Global Constraints

- Modify only Goal 274 behavior; do not change Goal 268's review-receive mutation guard.
- Preserve `CompleteGoalHandoffByReviewer`, self-claim completion, and reclaim/release system reports.
- Do not add dependencies or migrations.

### Task 1: Guard direct delegated completion and prove the state transitions

**Files:**

- Modify: `internal/store/goal_handoff.go:757-802`
- Modify: `internal/store/goal_handoff_test.go` near direct-close and self-claim coverage

**Interfaces:**

- Consumes: `handoffIsDelegation(requestedBy, receivedBy int64) bool`, `goalHandoffReclaimedReport`, `goalHandoffReleasedReport`
- Produces: `CompleteGoalHandoff` and `CompleteGoalHandoffForGoal` return `ErrGoalHandoffReviewState` for ordinary delegated direct closes.

- [ ] **Step 1: Add failing focused tests**

Create delegated, received handoffs in two states: before review request and after `RequestGoalHandoffReview`. For each state, call both `CompleteGoalHandoff` and `CompleteGoalHandoffForGoal`; assert `errors.Is(err, ErrGoalHandoffReviewState)` and assert the handoff remains incomplete. Keep an assertion that `CompleteGoalHandoffByReviewer` completes the reviewed handoff.

- [ ] **Step 2: Run the focused test before implementation**

Run: `go test ./internal/store -run 'Test.*GoalHandoff.*(Direct|Complete).*' -count=1`

Expected: FAIL because pre-review direct completion is currently accepted.

- [ ] **Step 3: Add the minimal shared guard**

In `CompleteGoalHandoff`, after loading the handoff, reject a delegated handoff whose report is neither `goalHandoffReclaimedReport` nor `goalHandoffReleasedReport` with `ErrGoalHandoffReviewState`. Leave `CompleteGoalHandoffForGoal` as a resolver that calls this shared method, and do not alter `CompleteGoalHandoffByReviewer`.

- [ ] **Step 4: Preserve explicit exceptions**

Keep or add focused assertions that a self-claim still completes and that reclaim/release use their existing system reports successfully. Do not broaden the exception set.

- [ ] **Step 5: Verify**

Run: `go test ./internal/store -run 'Test.*GoalHandoff' -count=1`

Expected: PASS, including direct-close rejection, reviewer completion, self-claim, reclaim, and release coverage.

- [ ] **Step 6: Report for review**

Include the exact test output, changed files, and confirmation that Goal 268's review-receive mutation guard was not edited. Do not commit, start agents, alter unrelated files, or request escalation.
