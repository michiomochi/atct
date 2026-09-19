# Goal 230 Reviewer Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Safely recover a pending goal-handoff review after project-command claim turnover.

**Architecture:** Keep recovery authorization in the Goal 225 Store state machine, where every direct caller is protected. The daemon/MCP route stays a thin adapter and keeps its ordinary commander role/evidence response.

**Tech Stack:** Go, SQLite-backed Store, daemon RPC, MCP shim tests.

## Global Constraints

- Begin only after the commander accepts this plan through plan-handoff review.
- Rebase or integrate first onto Goal 225 commit
  `19937307be4794d62e7d8226856dad89f0123c76` (`feat: add GoalDetail goal
  review actions`) or a commander-verified descendant. Goal 230 currently
  starts from `16c251b06d2caacd4d39a9f775877dcc9fca85ed` and cannot implement
  this plan before that integration gate passes.
- If integration conflicts in Goal 225-owned state-machine/outbox paths, stop
  and return the conflict to the commander; do not resolve it in Goal 230.
  Those paths are `internal/store/goal_handoff.go`,
  `internal/store/task_handoff.go`, `internal/store/queries/task.sql`,
  `internal/store/migrations/`, `internal/store/workflow_events.go`,
  `internal/store/notify.go`, and generated SQLC output/tests.
- Before RED, verify the integrated base contains
  `ReceiveGoalHandoffReview`; if absent, stop and report the missing Goal 225
  prerequisite rather than writing recovery tests against this older base.
- Do not read, change, or use Goal 225's live handoff or session 5483.
- Preserve requester-only authorization while the requester retains the project claim.
- Do not alter migrations, SQL state predicates, task/plan review paths, or workflow-event format.

---

## File / responsibility map

- `internal/store/goal_handoff.go`: authorize normal and recovery reviewer receipt.
- `internal/store/goal_handoff_test.go`: direct Store authorization/state tests.
- `internal/daemon/goal_handoff_test.go`: named RPC authorization and commander-evidence tests.

### Task 1: Authorize recovery in the Store transition

**Files:**

- Modify: `internal/store/goal_handoff.go`
- Test: `internal/store/goal_handoff_test.go`

**Interfaces:**

- Consumes: `GoalHandoff{RequestedBy, GoalID, ReviewRequestedAt, ReviewReceivedAt}` and the target project's `ClaimedBy`.
- Produces: `ReceiveGoalHandoffReview(ctx, handoffID, goalID, callerID)`, preserving its signature and either recording `callerID` as `ReviewReceivedBy` or returning without mutation.

- [ ] **Step 1: Write failing Store tests for the five-case matrix**

  First execute the pre-RED integration check:

  ```bash
  git merge-base --is-ancestor 19937307be4794d62e7d8226856dad89f0123c76 HEAD
  rg -n 'func \(s \*Store\) ReceiveGoalHandoffReview' internal/store/goal_handoff.go
  ```

  Expected: both commands exit 0. If either fails, stop and return the missing
  integration prerequisite to the commander; do not start RED or resolve a
  Goal 225-owned integration conflict.

  Create fixtures with an original requester, receiver, replacement commander,
  noncommander, and separate-project commander. Request/receive the handoff,
  then request review. Assert:

  ```go
  if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, requesterID); err != nil { t.Fatal(err) }
  got, _ := s.GetGoalHandoff(ctx, handoff.ID)
  if got.ReviewReceivedAt != nil || got.ReviewReceivedBy != 0 { t.Fatalf("review receipt mutated: %+v", got) }
  ```

  Cover a live original requester, claim turnover plus replacement commander,
  claim turnover plus noncommander, and a commander whose claim is another
  project.

- [ ] **Step 2: Run focused tests to verify recovery fails**

  Run: `go test ./internal/store -run 'TestGoalHandoffReview.*(Recovery|Requester|Commander|Foreign)' -count=1`

  Expected: the replacement commander case fails with the current requester
  mismatch; existing requester tests pass.

- [ ] **Step 3: Add the narrow Store authorization branch**

  After existing handoff identity/state checks, accept `callerID == RequestedBy`.
  Otherwise load the goal and target project, then require:

  ```go
  callerID != 0 &&
  project.ClaimedBy == callerID &&
  project.ClaimedBy != handoff.RequestedBy
  ```

  Return the existing reviewer mismatch error (or a specifically named wrapped
  recovery-denied error) before beginning the transaction when false. Leave
  `ReceiveGoalHandoffReview` SQL unchanged; its state predicate remains the
  concurrency backstop.

- [ ] **Step 4: Re-run focused Store coverage**

  Run: `go test ./internal/store -run 'TestGoalHandoffReview.*(Recovery|Requester|Commander|Foreign|Lifecycle)' -count=1`

  Expected: PASS, and every rejection leaves review receipt fields unset.

- [ ] **Step 5: Commit the Store change**

  ```bash
  git add internal/store/goal_handoff.go internal/store/goal_handoff_test.go
  git commit -m "fix: recover goal handoff reviewer after claim turnover"
  ```

### Task 2: Prove named RPC behavior and scope isolation

**Files:**

- Modify: `internal/daemon/goal_handoff_test.go`
- Test: `internal/daemon/goal_handoff_test.go`

**Interfaces:**

- Consumes: Task 1 Store authorization through `goal.handoff.review.receive`.
- Produces: unchanged receive response `{data, role: "commander", claim_evidence}` for a valid recovery caller.

- [ ] **Step 1: Add failing RPC tests for turnover and rejection paths**

  Exercise `goal.handoff.review.receive`. After replacing the original
  requester's project claim, assert the replacement commander gets:

  ```go
  if got.Role != "commander" || got.ClaimEvidence.Scope != "project" ||
      got.ClaimEvidence.ProjectID != fixture.projectID {
      t.Fatalf("recovery response = %+v", got)
  }
  ```

  Add independent subtests where the original requester remains live, the
  caller has no target-project claim, and the caller claims another project.
  Each returns an error and leaves the handoff review-requested.

- [ ] **Step 2: Run focused daemon tests to verify new cases fail first**

  Run: `go test ./internal/daemon -run 'Test.*GoalHandoff.*Review.*(Recovery|Requester|Foreign|Commander)' -count=1`

  Expected: recovery fails until Task 1 is present; denial cases never become
  successful review receipt.

- [ ] **Step 3: Use the existing adapter; add no daemon authorization fork**

  Do not change `goal.handoff.review.receive`, `receiveGoalReviewResponse`,
  or MCP schemas unless a test proves an adapter defect. The daemon continues to
  call Store and derives evidence from the target goal's project claim.

- [ ] **Step 4: Run verification**

  Run:

  ```bash
  go test ./internal/store ./internal/daemon -run 'Test.*GoalHandoff.*Review' -count=1
  git diff --check
  ```

  Expected: PASS with no whitespace errors.

- [ ] **Step 5: Commit RPC regression coverage**

  ```bash
  git add internal/daemon/goal_handoff_test.go
  git commit -m "test: cover goal handoff reviewer recovery"
  ```
