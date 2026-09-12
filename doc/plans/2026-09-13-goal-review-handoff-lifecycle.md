# Goal Review Handoff Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep a goal handoff open through human review, reusing it on rejection and completing it atomically with approved goal finalization.

**Architecture:** The store distinguishes a commander review receipt from a completed handoff. Human review opens after the receipt; finalization uses the existing SQL handoff-completion and goal-finalization updates inside one transaction. A human rejection remains a commander action that drives the existing goal-handoff rejection lifecycle.

**Tech Stack:** Go, SQLite, sqlc-generated queries, MCP shim, `atct watch`.

## Global Constraints

- Preserve existing MCP tool names and input schemas.
- Reuse a rejected goal handoff's `handoff_id`; do not add a replacement-handoff API.
- `atct_goal_review_complete` must not finalize the goal unless it can also complete the currently reviewed handoff.
- Keep the spec and this plan committed under `doc/specs/` and `doc/plans/`.

---

### Task 1: Gate human review on commander receipt and finalize both records atomically

**Files:**
- Modify: `internal/store/goal.go:543-844`
- Modify: `internal/store/goal_handoff_test.go:1358-1646`
- Modify: `internal/store/goal_complete_test.go:253-450`

**Interfaces:**
- Consumes: `GoalHandoff` review fields and `sqlcgen.CompleteGoalHandoffByReviewer`.
- Produces: `RequestGoalReview` accepting a currently received goal-handoff review and `FinalizeGoalReview(ctx, goalID, commanderID)` atomically completing the handoff and goal.

- [ ] **Step 1: Write failing store tests for the new lifecycle**

Replace the “requires completed handoff review” expectation with a successful
`RequestGoalReview` immediately after `ReceiveGoalHandoffReview`. Add assertions
that the handoff remains incomplete while the human decision is open. Extend the
approved-finalization test to assert:

```go
completed, err := s.GetGoalHandoff(ctx, handoff.ID)
if err != nil {
	t.Fatalf("GetGoalHandoff after finalization: %v", err)
}
if completed.CompletedReportAt == nil || completed.CompletedBy != commanderID {
	t.Fatalf("completed handoff = %+v, want commander completion", completed)
}
```

Add a rejection cycle test that rejects the human review, verifies a direct
second `RequestGoalReview` fails, then calls `RejectGoalHandoffReview`,
`ReceiveGoalHandoffReviewRejection`, `RequestGoalHandoffReview`, and
`ReceiveGoalHandoffReview` with the original ID before a second human review
succeeds.

- [ ] **Step 2: Run the focused tests to verify failure**

Run:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./internal/store -run 'TestRequestGoalReviewRequiresCompletedGoalHandoffReview|TestRejectedGoalReviewRequiresExplicitReplacementHandoff' -count=1
```

Expected: failure because human review still requires `CompletedReportAt` and a
human rejection still requires a replacement handoff.

- [ ] **Step 3: Implement the minimal state transition changes**

In `internal/store/goal.go`:

```go
func goalHandoffHasCommanderReviewReceipt(h *GoalHandoff, commanderID int64) bool {
	return h.RequestedAt != nil && h.ReceivedAt != nil &&
		h.ReviewRequestedAt != nil && h.ReviewReceivedAt != nil &&
		h.ReviewRequestedBy == h.ReceivedBy &&
		h.ReviewReceivedBy == commanderID
}

func goalHandoffHasCommanderReviewCompletion(h *GoalHandoff, commanderID int64) bool {
	return goalHandoffHasCommanderReviewReceipt(h, commanderID) &&
		h.CompletedReportAt != nil &&
		h.CompleteReport != goalHandoffReclaimedReport &&
		h.CompleteReport != goalHandoffReleasedReport
}
```

Use the receipt predicate when opening human review. For a previously rejected
human decision, require `latest.ReviewRequestedAt.After(*previous.AnsweredAt)`
so only a fresh review round of the same handoff can reopen human review.

In `FinalizeGoalReview`, use the commander ID, select the latest delegated
handoff, require the receipt predicate, begin one transaction, call
`CompleteGoalHandoffByReviewer` with that handoff ID and its non-empty
`ReviewRequestReport`, then call `FinalizeGoalReview` on the same transaction.
Return an error and roll back if either update affects anything other than one
row.

- [ ] **Step 4: Run focused store tests to verify success**

Run:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./internal/store -run 'TestRequestGoalReview|TestRejectedGoalReview|TestFinalizeGoalReview' -count=1
```

Expected: PASS; approved finalization closes both records and rejected review
requires the same handoff's reject/receive/resubmit cycle.

- [ ] **Step 5: Commit the store lifecycle change**

```sh
git add internal/store/goal.go internal/store/goal_handoff_test.go internal/store/goal_complete_test.go
git commit -m "feat: keep goal handoff open through human review"
```

### Task 2: Route a human rejection to the commander as a handoff-rejection action

**Files:**
- Modify: `cmd/atct/watch.go:1301-1318,1661-1671`
- Modify: `cmd/atct/watch_scope.go:186-214`
- Modify: `cmd/atct/watch_test.go:400-452`
- Modify: `cmd/atct/watch_scope_test.go:36-46`
- Modify: `cmd/atct/codex_monitor_test.go:311-334`

**Interfaces:**
- Consumes: answered goal-review decisions with `answer_label == "reject"`.
- Produces: a commander-only `goal.review.reject` watch action whose message
  directs the commander to call `atct_goal_handoff_review_reject`.

- [ ] **Step 1: Write failing monitor tests**

Add a rejected goal-review decision to the existing applied goal-review watch
table. Assert its project watch output contains `goal.handoff.review.reject`,
its action event is `goal.review.reject`, and a goal-scoped watch does not
receive it. Add the formatted line to the canonical monitor-action test so a
Codex monitor submits the rejection action to the commander.

- [ ] **Step 2: Run the focused monitor tests to verify failure**

Run:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run 'TestWatchScopeGoalReviewCompletionIsProjectOnly|Test.*GoalReview.*Watch|TestCodexMonitorActionLineAdmitsCanonicalHandoffLifecycle' -count=1
```

Expected: failure because rejected human goal reviews currently project only a
generic `decision.rejected` event.

- [ ] **Step 3: Add the projected rejection action**

Mirror the approved-goal-review projection in `reconcileWatchScope` for a
rejected `KindGoalReview` decision. Emit `goal.review.reject` to the project
commander. Add its user-facing message:

```go
return fmt.Sprintf(
	"atct goal review rejected (goal_id: %s, decision_id: %s): commander should call goal.handoff.review.reject",
	decision.GoalID, decision.decisionID(),
), true
```

Treat `goal.review.reject` as project-only in `watchScopeFilter.delivers`.

- [ ] **Step 4: Run focused monitor tests to verify success**

Run:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run 'TestWatchScopeGoalReviewCompletionIsProjectOnly|Test.*GoalReview.*Watch|TestCodexMonitorActionLineAdmitsCanonicalHandoffLifecycle' -count=1
```

Expected: PASS; only the commander receives the specific reject action.

- [ ] **Step 5: Commit the monitor routing change**

```sh
git add cmd/atct/watch.go cmd/atct/watch_scope.go cmd/atct/watch_test.go cmd/atct/watch_scope_test.go cmd/atct/codex_monitor_test.go
git commit -m "feat: route human goal review rejection to commander"
```

### Task 3: Align the execution flow with the new lifecycle

**Files:**
- Modify: `doc/execution-flow.md:44-191`

**Interfaces:**
- Consumes: the final store and watch lifecycle from Tasks 1–2.
- Produces: a flow diagram that distinguishes commander review, human review,
  and the two reject paths.

- [ ] **Step 1: Update the diagram and role instructions**

Show the accepted commander review flowing to `atct_goal_review_request` while
the handoff remains open. Show human approval flowing to
`atct_goal_review_complete`, which completes the handoff and goal. Show human
rejection flowing to `atct_goal_handoff_review_reject` then
`atct_goal_handoff_review_reject_receive` and a same-ID review resubmission.
Remove text saying a human rejection creates a new goal handoff.

- [ ] **Step 2: Verify documentation consistency**

Run:

```sh
git diff --check
rg -n 'new handoff|新しい.*handoff|reissue.*handoff' doc/execution-flow.md
bash tests/wrapper_test.bash
```

Expected: no stale replacement-handoff instruction and passing wrapper tests.

- [ ] **Step 3: Commit the documentation alignment**

```sh
git add doc/execution-flow.md
git commit -m "docs: align goal handoff with human review"
```

### Task 4: Run the complete verification suite

**Files:**
- Modify: none

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces: evidence that store, daemon, monitor, MCP, and wrapper behavior
  remain compatible.

- [ ] **Step 1: Run the complete Go suite**

Run:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run formatting and wrapper checks**

Run:

```sh
gofmt -w internal/store/goal.go internal/store/goal_handoff_test.go internal/store/goal_complete_test.go cmd/atct/watch.go cmd/atct/watch_scope.go cmd/atct/watch_test.go cmd/atct/watch_scope_test.go
git diff --check
bash tests/wrapper_test.bash
```

Expected: no formatting diff, no whitespace errors, and passing wrapper tests.

- [ ] **Step 3: Confirm formatting did not change files**

```sh
git diff --exit-code
```
