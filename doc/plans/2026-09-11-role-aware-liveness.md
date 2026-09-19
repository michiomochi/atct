# Role-aware one-minute monitor liveness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Send a liveness turn every minute only when the monitor's role has an immediately actionable workflow state.

**Architecture:** Keep liveness entirely in the watch process. Derive actionability from the existing scoped reconciliation payload, then retain the existing action sink and bridge queue. No persistence or process lifecycle behavior changes.

**Tech Stack:** Go, existing `cmd/atct` watch tests.

## Global Constraints

- Use `doc/specs/2026-09-11-role-aware-liveness.md` as the behavioral source.
- Do not add a database migration, delivery cursor, or monitor restart path.
- Suppress every liveness prompt while a scoped human decision is open.

---

### Task 1: Specify role-aware liveness with failing tests

**Files:**
- Modify: `cmd/atct/watch_test.go:879-914`

**Interfaces:**
- Consumes: `watchLivenessState.PromptDue(time.Time, watchScope, watchReconciliation) bool`.
- Produces: regression coverage for one-minute, actionable-only behavior.

- [x] **Step 1: Write failing tests**

```go
func TestWatchLivenessPromptsAfterOneMinuteForActionableExecutor(t *testing.T) {
	state := newWatchLivenessState(time.Unix(0, 0))
	scope := watchScope{Role: "executor", ProjectID: "1", GoalID: "249", TaskID: "812"}
	received := "received"
	reconciliation := watchReconciliation{TaskHandoffs: []watchReconciliationHandoff{{TaskID: 812, ReceivedAt: &received}}}
	if !state.PromptDue(time.Unix(60, 0), scope, reconciliation) {
		t.Fatal("PromptDue() = false, want actionable executor prompt at one minute")
	}
}
```

Add table cases proving: an executor awaiting review is silent; a subcommander with a pending task review prompts; a subcommander while an executor is working is silent; a commander with a pending goal review prompts; and an open decision suppresses each otherwise actionable case.

- [x] **Step 2: Run the focused test to verify it fails**

Run: `go test ./cmd/atct -run '^TestWatchLivenessPromptsAfterOneMinuteForActionableExecutor$' -count=1`

Expected: FAIL because the current interval is ten minutes.

- [x] **Step 3: Commit the red test only if it is independently reviewable**

Do not commit an intentionally failing test by itself in the normal feature history; continue directly to Task 2.

### Task 2: Derive immediate actionability from reconciliation

**Files:**
- Modify: `cmd/atct/watch.go:26-34,430-440`
- Modify: `cmd/atct/watch_scope.go:16-49`
- Test: `cmd/atct/watch_test.go:879-914`

**Interfaces:**
- Consumes: `watchScope`, `watchReconciliation`, and existing handoff timestamps.
- Produces: `watchLivenessActionable(watchScope, watchReconciliation) bool`, used by `PromptDue`.

- [x] **Step 1: Implement the smallest predicate**

```go
const watchLivenessPromptInterval = time.Minute

func (s *watchLivenessState) PromptDue(now time.Time, scope watchScope, snapshot watchReconciliation) bool {
	if !watchLivenessActionable(scope, snapshot) || scopedOpenDecision(scope, snapshot) {
		s.lastPromptAt = now
		return false
	}
	if now.Sub(s.lastPromptAt) < watchLivenessPromptInterval {
		return false
	}
	s.lastPromptAt = now
	return true
}
```

Implement `watchLivenessActionable` in `watch_scope.go` with role-specific helpers. Treat a submitted task review as waiting; treat a review rejection only after the original worker receives it as actionable; and treat any received, unfinished executor handoff without a review request as active executor work.

- [x] **Step 2: Run the focused liveness tests**

Run: `go test ./cmd/atct -run '^TestWatchLiveness' -count=1`

Expected: PASS.

- [x] **Step 3: Run package verification**

Run: `go test ./cmd/atct -count=1`

Expected: PASS.

### Task 3: Record the behavior and verify the repository

**Files:**
- Modify: `doc/continuous-execution.md`
- Modify: `doc/specs/2026-09-11-role-aware-liveness.md`
- Modify: `doc/plans/2026-09-11-role-aware-liveness.md`

**Interfaces:**
- Consumes: the verified implementation behavior.
- Produces: durable operational documentation with one-minute role-aware liveness.

- [x] **Step 1: Update the operational document**

Replace the old generic ten-minute liveness description with the one-minute role-aware conditions and explicit human-decision suppression.

- [x] **Step 2: Run full verification**

Run: `go test ./... -count=1 && git diff --check`

Expected: every Go package passes and the diff has no whitespace errors.

- [x] **Step 3: Commit the completed change**

```bash
git add cmd/atct/watch.go cmd/atct/watch_scope.go cmd/atct/watch_test.go \
  doc/continuous-execution.md doc/specs/2026-09-11-role-aware-liveness.md \
  doc/plans/2026-09-11-role-aware-liveness.md
git commit -m "feat: prompt actionable roles every minute"
```

## Self-review

- The plan changes only the timer and role-aware predicate, satisfying the no-persistence constraint.
- Every role action and suppression in the spec has a focused test case in Task 1.
- The plan contains no timeout/default behavior and does not restart a process.

## Execution Handoff

The user approved inline execution for this scoped change. Execute Tasks 1–3 in this session with the TDD red-green cycle.
