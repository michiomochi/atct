# Goal-review guidance lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:test-driven-development. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align shared ATCT guidance with the existing open-handoff human goal-review lifecycle and pin that order with the shell contract.

**Architecture:** This is a documentation-only correction. `skills/atct/SKILL.md` remains the shared source; `tests/wrapper_test.bash` asserts the normal sequence. No daemon, schema, or MCP behavior changes.

**Tech Stack:** Markdown skill guidance; Bash static-contract test; existing Go store lifecycle tests.

## Global Constraints

- Do not start implementation until commander accepts this plan through the plan handoff review.
- Do not alter Goal 246 receipt-schema work, goal creation activation, or existing-goal cleanup.
- Keep `atct_goal_handoff_complete` only as legacy/out-of-order recovery, never as a normal pre-human-review step.
- Preserve the test harness; add no dependency or framework.

---

### Task 1: Correct the shared lifecycle and its contract

**Files:**

- Modify: `skills/atct/SKILL.md:461-489, 475-481, 534-540, 579-604`
- Modify: `tests/wrapper_test.bash` goal-lifecycle assertions
- Test: `tests/wrapper_test.bash`
- Verify implementation boundary: `internal/store/goal_handoff_test.go`

**Interfaces:**

- Consumes: `RequestGoalReview` and `FinalizeGoalReview`.
- Produces: `atct_goal_handoff_review_request` → `atct_goal_handoff_review_receive` → `atct_goal_review_request` → merge → `atct_goal_review_complete`.

- [ ] **Step 1: Write the failing shell-contract assertions.**

Replace the assertions requiring `atct_goal_handoff_complete` before human review with:

```bash
[[ "$flow" == *'atct_goal_handoff_review_receive`'*'atct_goal_review_request`'* ]] ||
  fail 'commander review receipt must precede human goal review'
[[ "$flow" == *'atct_goal_review_request`'*'atct_goal_review_complete`'* ]] ||
  fail 'human goal review must precede atomic finalization'
! grep -Fq -- 'atct_goal_handoff_complete` → `atct_goal_review_request`' <<<"$flow" ||
  fail 'normal lifecycle must not close the handoff before human review'
```

Update related static assertions so the subcommander's upward message and commander wakeup are the goal-handoff review request, not legacy handoff completion.

- [ ] **Step 2: Run the contract to verify RED.**

Run: `bash tests/wrapper_test.bash`

Expected: FAIL because the current skill contains the obsolete completion-before-human-review order.

- [ ] **Step 3: Write the minimal guidance correction.**

In every normal-path mention, state that the commander receives review, requests human review while the handoff remains open, and calls `atct_goal_review_complete` after approval and merge. Retain legacy premature `atct_goal_handoff_complete` only as explicitly out-of-order recovery. Do not edit implementation files.

- [ ] **Step 4: Run focused verification.**

Run `bash tests/wrapper_test.bash` and `go test ./internal/store -run 'TestRequestGoalReviewAllowsReceivedGoalHandoffReview|TestGoalReviewRejectRequiresSameHandoffResubmission|TestFinalizeGoalReview'`.

Expected: both pass; the shell contract rejects the old order and the store tests retain the open-handoff invariant.

- [ ] **Step 5: Commit after accepted executor review.**

Stage only `skills/atct/SKILL.md`, `tests/wrapper_test.bash`, and these Goal 269 spec/plan records; commit a focused guidance-lifecycle change after task acceptance.
