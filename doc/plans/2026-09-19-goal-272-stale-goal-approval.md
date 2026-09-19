# Stale Goal Approval Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use ATCT executor handoffs task-by-task with the superpowers test/review workflow. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically retire old, untouched proposed goals while preserving every proposed goal with recorded work or recent activity.

**Architecture:** Reuse the existing commander-only goal withdrawal transaction for both active goals and provably unstarted proposed goals. Add a store reconciliation that scans open proposed approvals older than the fixed 14-day window, then call it from the existing 30-second daemon maintenance loop. A proposed goal with any task, handoff, additional decision, or recent update keeps its existing goal_approval as the human decision.

**Tech Stack:** Go, SQLite/sqlc, existing daemon maintenance and workflow events.

## Global Constraints

- Withdraw automatically only when the approval and goal have both been inactive for 14 days and no task or handoff record exists.
- Treat every task and handoff row, including terminal rows, as evidence that work started; do not withdraw those goals automatically.
- Preserve the original goal_approval as the only human decision for ambiguous cases; do not add a second decision kind.
- Keep active-goal withdrawal behavior and commander authorization unchanged.
- Do not add a migration, configuration, MCP tool, UI, worktree scanner, or daemon worker.
- Executor verification is limited to the focused internal/store and internal/daemon tests named below; the subcommander runs broader verification after review.

---

### Task 1: Make withdrawal safe for unstarted proposed goals and add store reconciliation

**Files:**
- Modify: internal/store/queries/goal.sql
- Regenerate: internal/store/sqlcgen/goal.sql.go with go tool sqlc generate
- Modify: internal/store/goal.go
- Modify: internal/store/goal_withdraw_test.go
- Create: internal/store/goal_stale_approval_test.go

**Interfaces:**
- Consumes: existing goals, decisions, tasks, goal/task/plan/task-create handoff tables and domain.Goal/domain.Decision timestamps.
- Produces: Store.ReconcileStaleGoalApprovals(ctx, now) (int, error) and a withdrawal path that accepts only an untouched proposed goal or preserves the existing active behavior.

- [ ] **Step 1: Write failing store tests**

Add table-driven tests that create an agent goal and move the proposal timestamps
back with SQL so the clock is deterministic. Cover:

Use these cases: `TestReconcileStaleGoalApprovalsWithdrawsUntouchedOldProposal`,
`TestReconcileStaleGoalApprovalsKeepsRecentProposal`,
`TestReconcileStaleGoalApprovalsKeepsProposalWithTaskInAnyStatus`,
`TestReconcileStaleGoalApprovalsKeepsProposalWithCompletedHandoff`,
`TestReconcileStaleGoalApprovalsKeepsProposalWithAdditionalDecision`, and
`TestReconcileStaleGoalApprovalsKeepsRecentlyUpdatedProposal`.

Assert the dropped case stores a non-empty reason in both result_summary and
answer_text, changes statuses to dropped/withdrawn, publishes exactly one
goal-withdrawn event carrying the approval ID, and returns count 1. Assert the
protected cases return count 0 and create no extra decision. Add a direct
withdrawal test for an untouched proposed goal and a refusal test for a
proposed goal with records. Run:

~~~
count, err := s.ReconcileStaleGoalApprovals(ctx, now)
if err != nil || count != 1 {
    t.Fatalf("ReconcileStaleGoalApprovals() = (%d, %v), want (1, nil)\n", count, err)
}
gotGoal, _ := s.GetGoal(ctx, goal.ID)
gotApproval, _ := s.GetDecision(ctx, approval.ID)
if gotGoal.Status != domain.GoalDropped || gotApproval.Status != domain.DecisionWithdrawn {
    t.Fatalf("statuses = (%q, %q), want (dropped, withdrawn)", gotGoal.Status, gotApproval.Status)
}
~~~

Then run the focused test command:

~~~
go test ./internal/store -run 'Test(ReconcileStaleGoalApprovals|Withdraw.*Proposed)' -count=1
~~~

Expected: FAIL because the reconciliation method and proposed-goal withdrawal
support do not exist.

- [ ] **Step 2: Add the smallest sqlc queries**

In internal/store/queries/goal.sql, add:

~~~
-- name: HasGoalWork :one
SELECT EXISTS(
  SELECT 1 FROM tasks WHERE goal_id = ?
  UNION ALL SELECT 1 FROM goal_handoffs WHERE goal_id = ?
  UNION ALL SELECT 1 FROM plan_handoffs WHERE goal_id = ?
  UNION ALL SELECT 1 FROM task_create_handoffs WHERE goal_id = ?
  UNION ALL SELECT 1 FROM decisions
    WHERE goal_id = ? AND kind <> 'goal_approval'
);

-- name: WithdrawProposedGoal :execresult
UPDATE goals SET status = 'dropped', result_summary = ?, updated_at = ?
WHERE id = ? AND status = 'proposed'
  AND NOT EXISTS (SELECT 1 FROM tasks WHERE goal_id = ?)
  AND NOT EXISTS (SELECT 1 FROM goal_handoffs WHERE goal_id = ?)
  AND NOT EXISTS (SELECT 1 FROM plan_handoffs WHERE goal_id = ?)
  AND NOT EXISTS (SELECT 1 FROM task_create_handoffs WHERE goal_id = ?)
  AND EXISTS (
    SELECT 1 FROM decisions
    WHERE goal_id = ? AND kind = 'goal_approval' AND status = 'open'
  )
  AND NOT EXISTS (
    SELECT 1 FROM decisions
    WHERE goal_id = ? AND kind <> 'goal_approval'
  );
~~~

The positional arguments may be named differently by sqlc; preserve the same
conditions. Regenerate and run script/schema-check.sh.

- [ ] **Step 3: Extend the existing withdrawal transaction**

Keep the public WithdrawActiveGoal name for compatibility, but branch on the
goal status inside its transaction:

1. For active, retain the current update, open-decision withdrawal, open-task
   dropping, and task-handoff completion behavior unchanged.
2. For proposed, call the conditional WithdrawProposedGoal. If it cannot
   update and HasGoalWork is true, return a new ErrGoalHasWork; otherwise
   return the existing non-active error.
3. For a successful proposed withdrawal, withdraw its open approval through the
   existing decision helper, build the same GoalWithdrawnEvent and
   decision.withdrawn event, commit once, then publish notifications.

Use the existing ListAllGoals, ListOpenDecisions, and ListTasks patterns; do not
create a second withdrawal implementation. The reason must be the same string
in the goal result and decision answer text.

- [ ] **Step 4: Implement the stale approval scan**

Add const staleGoalApprovalAfter = 14 * 24 * time.Hour and
ReconcileStaleGoalApprovals(ctx, now). Iterate proposed goals and their open
decisions using existing store readers. For each open KindGoalApproval, use
the later of decision.CreatedAt and goal.UpdatedAt as the last activity; skip
it when now is before that time plus the threshold. Call the shared withdrawal
method with this exact reason:

~~~
automatic withdrawal: goal approval was stale for 14 days with no recorded task or handoff activity
~~~

Treat ErrGoalHasWork and a concurrent ErrGoalNotActive as a protected or
already-settled candidate and continue. Return other database errors. The
method must be safe to call repeatedly: a withdrawn goal no longer has an open
approval candidate.

- [ ] **Step 5: Run store verification**

Run:

~~~
script/schema-check.sh
go test ./internal/store -run 'Test(ReconcileStaleGoalApprovals|Withdraw.*Proposed|WithdrawActiveGoal)' -count=1
~~~

Expected: PASS.

### Task 2: Run reconciliation from daemon maintenance and report failures

**Files:**
- Modify: internal/daemon/wakeup.go
- Modify: internal/daemon/wakeup_test.go

**Interfaces:**
- Consumes: Store.ReconcileStaleGoalApprovals(ctx, now).
- Produces: automatic cleanup on every existing 30-second maintenance tick and
  the existing wakeup.evaluate_failed signal if reconciliation returns an error.

- [ ] **Step 1: Write a failing maintenance integration test**

Create an old untouched agent proposal, call runMaintenanceWith with an
injected now and a successful wakeup evaluator, then assert the goal is dropped
before the tick returns and its approval is withdrawn. Add a second case with a
task or handoff record and assert it remains proposed. Re-run the same
maintenance tick and assert no duplicate withdrawal event or decision appears.

Use the existing daemon test helpers and assertions in this shape:

~~~
callRunMaintenanceWith(t, d, ctx, newWakeupTracker(time.Time{}), now,
    func(context.Context, int64) (store.WakeupState, error) {
        return store.WakeupState{}, nil
    })
got, err := s.GetGoal(ctx, goal.ID)
if err != nil || got.Status != domain.GoalDropped {
    t.Fatalf("goal after maintenance = (%q, %v), want dropped", got.Status, err)
}
~~~

Run:

~~~
go test ./internal/daemon -run 'TestRunMaintenance.*StaleGoalApproval' -count=1
~~~

Expected: FAIL because maintenance does not call the reconciliation method.

- [ ] **Step 2: Call reconciliation in the existing maintenance path**

In runMaintenanceWith, call reconciliation with the same now used by
ApplyExpiredDefaults and before publishing keepalive. Preserve the existing
failure-event contract by combining both errors before updating
evaluateFailedID:

~~~
reconcileErr := d.store.ReconcileStaleGoalApprovals(ctx, now)
// evaluateErr is the existing wakeup evaluation error.
maintenanceErr := errors.Join(reconcileErr, evaluateErr)
~~~

Use the existing failure event for a non-nil maintenanceErr; a nil error clears
the failure ID. Do not add another goroutine, ticker, public RPC, or event type.

- [ ] **Step 3: Verify maintenance behavior and existing wakeups**

Run:

~~~
go test ./internal/daemon -run 'TestRunMaintenance|TestWakeupTracker' -count=1
~~~

Expected: PASS, including the existing keepalive ordering and evaluate-failure
ID behavior.

- [ ] **Step 4: Commit the implementation**

After the focused tests pass, stage only the files in Tasks 1 and 2 and commit:

~~~
git add internal/store/queries/goal.sql internal/store/sqlcgen/goal.sql.go internal/store/goal.go internal/store/goal_withdraw_test.go internal/store/goal_stale_approval_test.go internal/daemon/wakeup.go internal/daemon/wakeup_test.go
git commit -m "fix: prune stale untouched goal approvals"
~~~
