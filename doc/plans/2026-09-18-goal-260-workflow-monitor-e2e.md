# Goal 260 Workflow and Monitor E2E Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use ATCT's executor handoff lifecycle task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one deterministic integration test that proves the workflow-contract lifecycle reaches only the intended monitor and suppresses non-actionable executor liveness.

**Architecture:** Reuse the real daemon, HTTP/SSE watch transport, workflow definition, and Codex monitor typed bridge.  The test drives persisted lifecycle RPCs instead of fabricating events, then observes scoped monitor actions and their delivery keys.  It records human approval before the test's merge-continuation marker; it does not run Git.

**Tech Stack:** Go standard library, existing daemon/store RPC, HTTP/SSE watch, Codex monitor bridge test helpers.

## Global Constraints

- `doc/execution-flow.md` and `internal/workflow` remain the lifecycle authority; this test adds no parallel transition table.
- A notification has one recipient role.  Assert both its intended recipient and all nonrecipients.
- Task-create follows accepted plan review; executor task delegation follows task creation.
- Goal review completes before human review; an ATCT-controlled merge continuation follows only `atct_goal_review_complete`.
- Refresh executor scope before every reconciliation/liveness check; an empty current task emits neither stale task delivery nor liveness.
- Do not add a daemon route, production hook, monitor restart, new dependency, or executable pane setup.

## File Structure

- Create: `cmd/atct/workflow_monitor_e2e_test.go` — complete daemon-to-monitor contract, using existing scoped-watch and bridge helpers.
- Modify only an existing test helper if package visibility prevents the new test from starting the same real daemon/HTTP stack; keep it test-only and shared rather than copying lifecycle setup.

### Task 1: Drive the complete lifecycle through scoped monitor delivery

**Files:**
- Create: `cmd/atct/workflow_monitor_e2e_test.go`
- Modify: only the smallest existing `cmd/atct` test helper required to expose the real daemon/HTTP stack

**Interfaces:**
- Consumes: `workflow.Actions`, daemon handoff RPCs, `runWatchScoped`, `watchScope`, and `codexMonitorBridge` typed actions.
- Produces: `TestWorkflowMonitorEndToEndContract(t *testing.T)`.

- [ ] **Step 1: Write the failing end-to-end test**

Create commander, subcommander, executor A, and executor B sessions.  Start
one scoped monitor bridge for each role.  Drive goal request/receipt, plan
review request/receipt/completion, task-create receipt/creation, task request/
receipt/review/receipt/completion, goal review request/receipt/completion, and
human approval.  For each table row in the spec, assert one matching typed
action reaches the named bridge and no action reaches the other bridges.

Include this executable liveness assertion after executor A's task review is
accepted:

```go
effectiveA := refreshExecutorScope(ctx, baseURL, executorAScope)
if effectiveA.TaskID != "" {
    t.Fatalf("executor A effective task = %q, want empty", effectiveA.TaskID)
}
if watchLivenessEligible(effectiveA) {
    t.Fatal("non-actionable executor A remained liveness-eligible")
}
assertNoMonitorAction(t, executorABridge, "monitor.liveness")
```

Use executor B's received task as the isolation control: its bridge still
receives its own task request while A is empty.

- [ ] **Step 2: Run the focused test to verify it fails**

Run:

```sh
GOCACHE=/private/tmp/goal260-go-cache go test ./cmd/atct -run TestWorkflowMonitorEndToEndContract -count=1
```

Expected: FAIL because the composition test does not yet exist.

- [ ] **Step 3: Add the smallest test-only stack/helper seam**

Use the existing daemon-backed test setup and typed `watchAgentAction` sink;
do not call a store event publisher directly.  Await each unique lifecycle
delivery key with a bounded context, then assert the other bridge queues stay
empty.  After `atct_goal_review_complete`, append a local test-only
`merge-continuation` marker and assert its index is after the approval marker;
do not invoke Git from the test.

- [ ] **Step 4: Run focused verification**

Run the command from Step 2.

Expected: PASS.  It proves committed lifecycle actions travel through daemon,
SSE scope/routing, and monitor queue once per intended recipient.

- [ ] **Step 5: Run regression verification**

```sh
GOCACHE=/private/tmp/goal260-go-cache go test ./internal/workflow ./internal/store ./internal/daemon ./cmd/atct -count=1
git diff --check
```

Expected: PASS with no whitespace errors.  Confirm the diff is limited to the
new E2E test and any unavoidable test-only helper.

- [ ] **Step 6: Submit for subcommander review**

Request the task-handoff review with the exact focused and regression command
outputs, changed paths, lifecycle-to-recipient matrix, and the explicit note
that the merge assertion verifies ATCT ordering rather than arbitrary external
Git commands.  Do not commit or request goal review in this task.

## Plan Self-Review

- Coverage: all role handoff boundaries, plan/task-create ordering, scoped
  notifications, executor rebinding, empty-selector liveness suppression,
  executor isolation, and human-approval-before-merge ordering are covered.
- Boundaries: the test observes the real daemon and monitor delivery path but
  changes neither; it does not claim to police Git outside ATCT.
- Placeholder scan: no unspecified lifecycle transition, target role, or test
  command remains.
