# Wakeup Content Deduplication Implementation Plan

> **For agentic workers:** Execute this plan inline in the existing executor worktree. Do not create a pane, delegate the work, or commit changes.

**Goal:** Make each `atct watch` emit a wakeup only when its rendered content differs from the previous wakeup it emitted.

**Architecture:** Keep only the last rendered normal wakeup content as one string in the per-watch loop state in `cmd/atct/watch.go`. The daemon may continue publishing periodic wakeup events with fresh IDs; the watch compares the rendered line, so unchanged counts are silent, changed counts are emitted immediately when received, and A -> B -> A emits the final A. The separate discrepancy event keeps its existing ID-based delivery set. State is per watch loop rather than daemon-global, ensuring every separately connected watch receives its first current wakeup.

**Tech Stack:** Go, existing SSE watch loop, Go standard-library tests.

## Global Constraints

- Use the approved decision: suppress only when the previously sent content is identical.
- A changed wakeup content must be emitted even if the event ID is new or the previous content was suppressed.
- Daemon restart does not share or persist delivery state; an existing watch keeps its local last content across reconnects and suppresses an unchanged post-restart wakeup, while a newly started watch emits its first wakeup.
- Delivery state is per watch loop, not daemon-global, so a later watch is not starved by an earlier watch's delivery.
- Do not modify `internal/store/`, `web/`, `plugin/`, or `cmd/atct/context.go`.
- Do not commit.

### Task 1: Add failing wakeup content-delivery tests

**Files:**
- Create: `cmd/atct/wakeup_delivery_test.go`

**Interfaces:**
- Consume the existing `watchDecision`, `watchWakeupDeliveryKey`, `emitWatchDecision`, and `formatWatchDecision` interfaces from `cmd/atct/watch.go`.
- Prove that identical rendered wakeup content is emitted once, changed content is emitted, and independent delivery maps each receive their first wakeup.

- [x] Write tests that feed two wakeup events with different IDs but identical counts, then a third event with changed counts; expect exactly the first and third rendered lines.
- [x] Write a test that sends the same content through two independent delivery maps; expect one line in each output.
- [x] Write a regression test that sends A -> B -> A; expect all three rendered lines.
- [x] Run the focused tests and capture the A -> B -> A failure from the historical map implementation.

### Task 2: Implement per-watch rendered-content deduplication

**Files:**
- Modify: `cmd/atct/watch.go` near `watchWakeupDeliveryKey` and `emitWatchDecision`

**Interfaces:**
- Preserve `emitWatchDecision`'s existing parameters and error behavior.
- Keep wakeup/discrepancy event validation and all non-wakeup delivery deduplication unchanged.

- [x] Replace the normal wakeup history map with one last-rendered-content string; retain ID-based state only for `wakeup.discrepancy`.
- [x] Add code comments documenting the daemon-restart and multiple-watch decisions and their reasons.
- [x] Run the focused tests and confirm the identical event is suppressed, changed content is emitted, and independent watches are not coupled.

### Task 3: Verify, mutate, and restore

**Files:**
- Modify: none beyond Tasks 1–2

- [x] Temporarily replace the changed-content key behavior with an always-suppressed key, run the focused test, and capture its failing output as proof the regression test is live.
- [x] Restore the implementation and run `gofmt` on changed Go files.
- [x] Run `go build ./... && go vet ./internal/daemon/ ./cmd/atct/`.
- [ ] Run `go test -race ./internal/daemon/ ./cmd/atct/`.
- [ ] Confirm the daemon was not left running; if it was started during verification, stop it before reporting.
