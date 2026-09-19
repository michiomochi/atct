# Shared Monitor Notification Policy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver every actionable formatted watch event, including a lost delegated monitor, identically to Claude and Codex monitors.

**Architecture:** Keep `selectWatchAgentAction` as the sole notification-policy boundary. Select formatted events by default and retain only the existing non-actionable observations as exclusions; Claude's writer and the Codex bridge continue to receive the same selected typed action.

**Tech Stack:** Go; focused `cmd/atct` unit tests.

## Global Constraints

- Do not add a transport-specific selector or raw-line classifier.
- Preserve scope filtering and delivery deduplication before action selection.
- Preserve the existing explicit non-actionable events listed in the approved spec.
- Do not alter which scopes each CLI supports.

---

### Task 1: Make the shared selector future-safe

**Files:**

- Modify: `cmd/atct/watch_action.go:39-79`
- Test: `cmd/atct/watch_action_test.go:16-107`

**Interfaces:**

- Consumes: a non-empty formatted `line`, `eventName`, and `watchDecision` after scope and deduplication.
- Produces: a `watchAgentAction` for all actionable events, or no action for the explicit non-actionable observations.

- [x] **Step 1: Add the regression to the frozen action matrix.**

  In `frozenWatchAgentActionCases`, add the literal case:

  ```go
  {
      name: "lost monitor detection",
      eventName: "detection.monitor_lost",
      line: "atct detection: monitor for handoff h1 is lost; recover or replace its worker",
      decision: watchDecision{GoalID: "1", TaskID: "2", HandoffID: "h1"},
      want: true,
  }
  ```

  This test catches a missing selector entry: deleting the default action path or adding this event to an exclusion makes the shared selector return `ok == false`.

- [x] **Step 2: Run the regression before implementation.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run '^TestWatchAgentActionSelectorMembership$' -count=1 -v
  ```

  Expected: FAIL for `lost_monitor_detection`, because the current whitelist omits `detection.monitor_lost`.

- [x] **Step 3: Replace the positive whitelist with explicit exclusions.**

  In `selectWatchAgentAction`, return no action for an empty line, `decision.pending`, `decision.opened`, default-applied `decision.answered`, ordinary `plan.handoff.request` / `.receive` / `.complete`, `detection.handoff_unreceived`, `detection.handoff_unreported`, `keepalive`, and an empty event name. Return the existing `watchAgentAction` for every other formatted event.

  The branch shape is:

  ```go
  if strings.TrimSpace(line) == "" || eventName == "" {
      return watchAgentAction{}, false
  }
  if eventName == "decision.answered" && decision.defaultApplied() {
      return watchAgentAction{}, false
  }
  switch eventName {
  case "decision.pending", "decision.opened", "plan.handoff.request",
      "plan.handoff.receive", "plan.handoff.complete",
      "detection.handoff_unreceived", "detection.handoff_unreported", "keepalive":
      return watchAgentAction{}, false
  }
  ```

  Keep `watchActionDeliveryIdentity` and the returned action fields unchanged.

- [x] **Step 4: Run the focused selector and transport-parity tests.**

  Run:

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run '^(TestWatchAgentActionSelectorMembership|TestClaudeAndCodexAgentActionParity)$' -count=1 -v
  ```

  Expected: PASS. The existing parity test feeds every selected frozen action to both adapters and proves equal event, line, and order; the new case proves `detection.monitor_lost` reaches both.

- [x] **Step 5: Commit the implementation.**

  ```sh
  git add cmd/atct/watch_action.go cmd/atct/watch_action_test.go
  git commit -m "fix: share monitor notification policy"
  ```

### Task 2: Verify the repository contract

**Files:**

- Modify: none unless verification reveals a directly related defect.

**Interfaces:**

- Consumes: the shared selection contract from Task 1.
- Produces: evidence that the `cmd/atct` package and all repository packages retain their contracts.

- [x] **Step 1: Run the package test suite.**

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -count=1
  ```

  Expected: PASS.

- [x] **Step 2: Run all tests.**

  ```sh
  GOCACHE=/private/tmp/atct-go-cache go test ./... -count=1
  ```

  Expected: PASS.

- [x] **Step 3: Check the final diff.**

  ```sh
  git diff --check HEAD~1..HEAD
  git status --short
  ```

  Expected: no whitespace errors and no uncommitted implementation changes.
