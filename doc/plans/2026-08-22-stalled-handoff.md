# Stalled Handoff Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

## Goal

Detect handoffs that have not advanced and claims that have no corresponding delegation, using the existing wakeup/detection path. “No progress” is defined solely by the next handoff timestamp being empty; status, commits, and pane state are not inputs.

## Architecture

- `internal/store/wakeup.go` will collect three candidate sets while scanning active goals: requested handoffs without `received_at`, received handoffs without `completed_report_at`, and claimed tasks without a task handoff whose `requested_at` is set.
- `internal/daemon/wakeup.go` will feed those candidates through the existing detection publisher with three separate 30-minute durations. The existing event-name-plus-target deduplication remains in place; handoff detections target `HandoffID`, and undelegated claims target `TaskID`.
- `cmd/atct/watch.go` will carry `handoff_id` in detection payloads and render the three new detection event names through the existing one-line watch output and delivery deduplication.
- `internal/daemon/wakeup.go` active-task and resend behavior will remain unchanged. No Herdr dependency or pane inspection will be added.

## Tech Stack

- Go
- Existing SQLite store and generated sqlc accessors
- Existing wakeup detection and watch event plumbing
- Injected `now` values in daemon tests

## Global Constraints

- Do not modify `internal/store/task_handoff.go`; another executor is editing it concurrently.
- Do not add a migration or change schema files.
- Do not inspect status, commits, pane state, or work-in-progress state to decide whether a detection is emitted.
- Do not use a new detection path.
- Do not add or commit unrelated changes; leave commit creation to the commander.

## Tasks

### 1. Add failing tests first

- Add a store test covering all three candidate classes and the filled-next-timestamp exclusions.
- Add a daemon test that evaluates one fixture containing all three candidates just before and just after their independent thresholds, verifies distinct event names and payload targets, and verifies no repeat for the same targets.
- Extend watch tests for the three event formats, handoff-target delivery deduplication, and payload handling.
- Run the targeted tests before production changes and confirm they fail for the missing behavior.

### 2. Implement store candidate collection

- Extend `WakeupState` with explicit candidate slices and extend `DetectionEvent` with optional `HandoffID`.
- During active-goal task scanning, list each task’s handoffs and select candidates only when the specified next timestamp is nil.
- Mark a task as delegated only when a handoff row has a nonnil `requested_at`; a claim with no such row becomes an undelegated-claim candidate.
- Keep all candidate slices nonnil in returned state and avoid status/commit checks.

### 3. Wire daemon thresholds and deduplication

- Define three separately named 30-minute duration constants.
- Reuse `publishDetection` and its existing tracker maps, allowing the candidate’s source timestamp to determine age while preserving the existing grace behavior for other detections.
- Emit handoff IDs for the first two event types and task IDs for undelegated claims.
- Keep active detection collection and resend logic otherwise unchanged.

### 4. Render watch events

- Add `handoff_id` to the watch decision payload.
- Select handoff ID as the detection target when present, then fall back to task ID for task detections.
- Add stable one-line messages for missing receipt, missing completion report, and claim without a handoff request.

### 5. Run the required mutation check

- Temporarily remove the next-timestamp nil guard in the store detector and run the focused store test.
- Confirm the test fails by including a received/completed handoff that should be excluded.
- Restore the guard immediately and rerun the focused test successfully.

### 6. Verify and hand off

- Format changed Go files.
- Run focused store, daemon, and watch tests.
- Run `go build ./...` and `go test ./internal/... ./cmd/...`.
- Inspect the diff and confirm `internal/store/task_handoff.go` is unchanged.
- Report the implementation, mutation result, verification results, and no-commit status to `atct-commander` in at most 10 lines.
