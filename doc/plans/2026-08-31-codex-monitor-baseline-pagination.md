# Codex monitor baseline pagination implementation plan

**Goal:** Start the monitored Codex TUI after a bounded first-page baseline capture,
without misclassifying existing paginated threads as new.

**Architecture:** Add a baseline pagination primitive that obtains the first page
under setup context and resumes the returned cursor asynchronously.  Gate existing
thread discovery on its full result, so discovery retains its complete-ID comparison.

## Constraints

- Do not lengthen `codexMonitorSetupTimeout`.
- Retain full 128 MiB App Server read limit, strict status decoding, and remoteControl no-op behavior.
- Preserve fast automatic fallback when App Server setup cannot obtain the first page.
- No launcher, shim, user configuration, or unrelated refactoring changes.

### Task 1: Add deterministic lifecycle regression coverage

**Files:** modify `cmd/atct/codex_monitor_lifecycle_test.go`.

1. Extend the fake App Server with a cursor-aware baseline response and a channel
   which blocks page two until the test releases it.
2. Write a failing test which starts `runCodexMonitorWithDeps`, waits for fake TUI
   start, and asserts it occurs before releasing page two.
3. Assert discovery has not been called while page two is blocked.
4. Release page two; assert discovery receives IDs from both old pages and then
   returns the TUI-created thread only.
5. Run `go test ./cmd/atct -run TestCodexMonitor -count=1` and observe the test fail
   before production changes.

### Task 2: Make baseline continuation asynchronous

**Files:** modify `cmd/atct/codex_monitor.go`, `cmd/atct/codex_monitor_supervisor.go`.

1. Extract cursor-aware list pagination so the caller can retain the first result's
   next cursor and finish it later without re-listing page one.
2. In supervisor setup, use the existing setup context for connect, initialize, and
   first baseline page only.
3. Start lifecycle/TUI after that page; run remaining cursor pages under monitor
   context in a goroutine.
4. Start `DiscoverThread` only after the continuation succeeds, passing the complete
   old-ID map. Route continuation errors through the existing post-launch
   `disableMonitor` path.
5. Run the focused lifecycle test, then `go test ./cmd/atct -count=1`.

### Task 3: Review boundary conditions

**Files:** inspect the two implementation files and lifecycle test only.

1. Confirm no path uses a partial ID map for discovery.
2. Confirm first-page failure still uses `codexMonitorSetupFailure`, preserving
   automatic fallback and explicit monitor failure semantics.
3. Confirm only Goal 216 implementation, tests, spec, and plan changed.
