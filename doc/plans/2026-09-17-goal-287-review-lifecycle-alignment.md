# Goal 287: Human goal review lifecycle alignment — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent direct goal-handoff completion from bypassing human goal review, and make the terminal-versus-blocked reporting channels explicit.

**Architecture:** Keep the existing open-handoff → human review request → approval → atomic finalizer flow. First reject re-receipt of a rejected handoff, then (after the required main-integration gate) add a separate guard at the shared public reviewer-completion method, retain the finalizer's transaction-local SQL completion, and only then apply the gated guidance wording.

**Tech Stack:** Go, SQLite, existing ATCT MCP/daemon/store tests, Markdown skills.

## Global constraints

- Read and align with current `main` (`v0.63.11`, `9f519a8`) without merging or rebasing this worktree.
- Before creating or delegating any store/test task, verify that the Goal 274 direct-close integration is present on `main`; if it is not, stop task creation and escalate through `atct_decision_ask`.
- Do not start the skill/lifecycle-document follow-up until the Goal 284/283 integrations are present on `main`. The current owner of `doc/execution-flow.md` remains responsible for that file.
- Do not edit `doc/execution-flow.md` or `skills/subcommander/SKILL.md`; Goal 286 owns those lifecycle/bootstrap documents.
- Do not edit `internal/mcpshim/instructions.go`; Goal 290 owns its compression.
- Do not change Goal 272 stale-approval withdrawal or any recovery behavior.
- Reuse `ErrGoalHandoffReviewState`; add no tool, schema, configuration flag, or new abstraction.
- Keep the `skills/atct/SKILL.md` edit minimal and self-contained; record its Goal 290 overlap in the final `needs_review` report.
- Task 1 and Task 2 remain behind the Goal 274 main-integration gate; do not declare either task until that gate is verified. Task 2 is intentionally separate and starts only after the rejected-review receive guard is accepted.

---

### Task 1: Reject receipt of a rejected goal-handoff review (Goal 221 regression)

**Files:**

- Modify: `internal/store/goal_handoff.go`
- Modify: `internal/store/goal_handoff_test.go`
- Modify: `internal/daemon/goal_handoff_test.go`

**Interfaces:**

- A rejected or rejection-received review cannot be received again.
- The ordinary requested-but-not-yet-received review can still be received once.
- The Goal 221 observation (`goal.handoff.review.receive` re-receiving a rejected review) is covered at the store layer and through the named route.

- [ ] **Step 1: Add failing store and RPC regressions**

Extend the existing reject/receive lifecycle coverage with the Goal 221 regression:

1. After `RejectGoalHandoffReview`, `ReceiveGoalHandoffReview` returns `ErrGoalHandoffReviewState` and leaves `ReviewRejectReport`/`ReviewRejectedAt` unchanged.
2. After `ReceiveGoalHandoffReviewRejection`, another `ReceiveGoalHandoffReview` returns the same error and leaves `ReviewRejectionReceivedBy`/`ReviewRejectionReceivedAt` unchanged.
3. A requested-but-not-yet-received review can be received once; a second receive is rejected without changing the received evidence.
4. The named daemon/MCP route has the same rejected-state refusal and unchanged rejection evidence.

Run the focused tests before implementation and confirm the new rejected-state assertions fail against the current code.

- [ ] **Step 2: Add the minimal store guard**

In `ReceiveGoalHandoffReview`, reject the handoff when `ReviewRejectedAt` or `ReviewRejectionReceivedAt` is already set, before opening the transaction. Preserve the existing requester/current-claim authorization and normal requested-state checks. Keep the rejection report and timestamps untouched. If the executor strengthens the generated SQL predicate for atomicity, change the query source and regenerate committed output rather than editing generated code by hand; no schema change is allowed.

Run the focused store and daemon tests for Task 1.

---

### Task 2: Enforce the terminal direct-close lifecycle (after Goal 274 gate)

**Files:**

- Modify: `internal/store/goal_handoff.go`
- Modify: `internal/store/goal_handoff_test.go`
- Modify: `internal/store/workflow_event_test.go` only if its historical completed-handoff fixture uses the guarded public method
- Modify: `internal/daemon/goal_handoff_test.go`

**Interfaces:**

- Direct public completion after commander review receipt returns `ErrGoalHandoffReviewState` and leaves the handoff open.
- `RequestGoalReview` still accepts that open handoff.
- `FinalizeGoalReview` remains the only normal completion route after human approval.

**Gate:** Start this task only after the Goal 274 direct-close integration is verified on `main` and Task 1's rejected-review receive guard has been accepted. Do not create or delegate this task before both conditions hold.

- [ ] **Step 1: Add failing store and RPC regressions**

Cover both public entry paths after a delegated handoff has been received for review:

1. `Store.CompleteGoalHandoffByReviewer` returns `ErrGoalHandoffReviewState`, leaves `CompletedReportAt` and the completion report unset, and emits no completion event.
2. The named daemon/MCP route returns the same error and leaves the persisted handoff unchanged.

Also retain or add the positive state assertions that `RequestGoalReview` succeeds while the handoff is open and that approved `FinalizeGoalReview` closes it atomically. Add the proposed-goal refusal assertion only if no existing focused test already covers the existing not-active error; do not change that behavior.

Run the focused tests before implementation and confirm the new direct-close assertions fail against the current code.

- [ ] **Step 2: Add the smallest shared store guard**

In `Store.CompleteGoalHandoffByReviewer`, after loading and validating the handoff identity and before the completion transaction, return `ErrGoalHandoffReviewState` for a commander-received delegated handoff in the review lineage. Preserve existing report, goal-ID, reviewer-identity, recovered-handoff, and missing-state checks where they remain meaningful.

Do not route `FinalizeGoalReview` through this public method. Its existing transaction-local generated SQL must remain the close path after the approved human decision, so finalization stays atomic.

- [ ] **Step 3: Repair test fixtures without weakening production state rules**

Update tests that only need a historical completed handoff to use an existing non-review/reclaimed fixture or a narrowly scoped test setup, rather than calling the now-guarded public reviewer method. Keep lifecycle tests on the real `RequestGoalReview`/approval/finalizer path. Do not add a production bypass for tests.

Run the focused store and daemon tests for Task 2.

---

### Task 3: Apply the gated report-channel wording

**Files:**

- Modify: `skills/atct/SKILL.md` only after the Goal 284/283 integration gate is verified

- [ ] **Step 1: Verify the integration gate**

Confirm the required Goal 284/283 changes are present on `main`. If the gate is not satisfied, do not edit this file; report the blocker through `atct_decision_ask` and leave this task unstarted.

- [ ] **Step 2: Make the minimal guidance edit**

Make the minimal edit to `skills/atct/SKILL.md` near its unsent-report routing:

- `atct_goal_handoff_review_request` is terminal and is called only after all declared work is accepted, committed, and ready for commander review.
- Unfinished or blocked work stays open and uses `atct_decision_ask` with concrete options/consequences; it is not a goal-handoff review request.
- Initial approval activates a proposed goal; `atct_goal_review_request` applies only to an active goal after the commander has received the goal-handoff review.

Do not duplicate the full lifecycle owned by Goal 286 or alter tool instructions owned by Goal 290.

- [ ] **Step 3: Verify and commit the task**

Run the smallest useful checks, then package the task in one commit:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./internal/store -run 'TestGoalHandoff|TestRequestGoalReview|TestFinalizeGoalReview' -count=1
GOCACHE=/private/tmp/atct-go-cache go test ./internal/daemon -run 'TestNamedGoalAndPlanHandoffReviewRoutesReturnRoleEvidence|TestNamedGoalAndPlanHandoffReviewRejectReceiveRetriesOverRPC|Test.*GoalHandoff' -count=1
git diff --check
```

Review each task's diff for unrelated ownership changes, stage only its files, and commit with a lifecycle-focused message. Report each commit, tests, `surprises`, `needs_review` (including Goal 290 skill overlap), and `next_steps` to the subcommander; do not send a goal review request from the executor.

## Plan self-review

- Rejected review re-receipt is a separate state task and is sequenced before the direct-close task, as required by the rejection report.
- Goal 221's observed rejected-review re-receipt is covered by store and named-route regressions, including unchanged rejection evidence.
- The Goal 274 main-integration gate precedes all store/test task creation; the Goal 284/283 integration gate precedes guidance changes.
- The direct reviewer close guard is a separate post-Goal-274 task and does not weaken the finalizer path.
- Root cause is fixed at shared store methods rather than at one RPC caller.
- The finalizer's intentional SQL bypass is preserved and tested through the approved lifecycle.
- The plan names the blocked-work channel and makes the terminal review request unambiguous.
- Adjacent ownership is explicit; no schema, tool, recovery, or lifecycle-document expansion is planned.
- No placeholder APIs, speculative abstractions, or broad test commands are included.
