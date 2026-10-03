# Commander Monitor Local Mutation Suppression Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Suppress a commander monitor's one redundant notification only after
that same Codex session has received the matching successful ATCT MCP mutation
response, plus one exact `goal handoff received` notification only after that
commander recorded and launched the matching subcommander handoff.

**Architecture:** A commander monitor owns an in-memory, capability-protected
Unix acknowledgement listener. A transport wrapper in its inherited
`atct-mcp` process observes a successful `tools/call` response write and sends
a canonical mutation key; the watch sink consumes a matching key immediately
before it would enqueue the action line. The same listener keeps a separate
five-minute goal-handoff candidate/armed state machine. Only an acknowledged
`atct_goal_handoff_request` followed by an authenticated child monitor launch
with the identical `(goal_id, handoff_id)` can arm one receipt key.

**Tech Stack:** Go, Unix-domain sockets, the existing MCP Go SDK stdio
transport, Codex monitor supervisor, and ATCT canonical reconciliation watch.

## Global Constraints

- Only commander monitors receive an acknowledgement capability; all other
  monitor modes and ordinary MCP launches must retain current behavior.
- Suppress only an already-acknowledged, exact local mutation key, once; never
  wait, retry, persist, or infer origin from an agent/session ID.
- The delegated lifecycle extension is the sole cross-session case: a child
  launch signal can arm one `goal.handoff.receive` key only after a fresh,
  parent-local request candidate with the same `(goal_id, handoff_id)`. It
  cannot create or refresh a candidate. Candidates and armed keys expire after
  five minutes and are one-shot.
- Require both `--goal` and `--handoff` for a subcommander child to send a
  launch signal. Do not infer a handoff ID from scope, process environment, or
  daemon state. A direct, independently started, failed, stale, duplicate,
  pre-launch, mismatched, or remote receipt must be delivered.
- A failed mutation, failed stdout write, unavailable/invalid channel, remote
  mutation, or notification-before-acknowledgement must deliver normally.
- Do not alter daemon/store event semantics, reconciliation, action-line text,
  handoff/plan semantics, or unrelated notification classes.
- Keep capability values out of logs, registry records, CLI output, and MCP
  results. Clean up listener files with the monitor's existing lifecycle.
- Implement exactly the tool/action/ID matrix in the accepted spec. Every
  unlisted mutation and every read-only tool must produce no acknowledgement;
  adding a new supported action requires a matrix and test update first.

---

### Task 1: Define and test monitor-local acknowledgement primitives

**Files:**

- Create: `cmd/atct/codex_monitor_ack.go`
- Test: `cmd/atct/codex_monitor_ack_test.go`

**Consumes:** Existing `codexMonitorBridge` lifecycle and `watch` action-line
formatters.

**Produces:** A concurrency-safe acknowledgement set plus a distinct,
time-bounded delegated-handoff state machine, both behind a
capability-authenticated Unix listener/client protocol using a canonical
`monitorMutationKey`.

- [ ] **Step 1: Write focused failing primitive tests**

  Add table-driven tests that prove: a valid capability stores a key; a wrong
  capability and malformed payload do not store it; `Consume(key)` returns
  true only once; and two independently created stores cannot consume each
  other's acknowledgement. Add lifecycle rows proving that only
  `request-ack(goal,handoff)` then `child-launch(goal,handoff)` arms one
  `goal.handoff.receive` key; launch-first, mismatched IDs, duplicate launch,
  duplicate receipt, expired candidate, expired armed key, and invalid
  capability do not suppress. Inject a clock rather than sleeping.

- [ ] **Step 2: Run the primitive test to verify it fails**

  Run: `go test ./cmd/atct -run '^TestCodexMonitorAck' -count=1`

  Expected: FAIL because the acknowledgement listener and key type do not yet
  exist.

- [ ] **Step 3: Implement the bounded in-memory protocol**

  Define a key with an action class, handoff ID, and canonical numeric target
  ID. Implement `newCodexMonitorAcknowledgements`, `Serve`, `Acknowledge`,
  and `Consume`; validate a constant-time opaque capability comparison and
  reject blank/unknown key components. On a successful
  `goal.handoff.request` acknowledgement, register the five-minute candidate.
  Accept a distinct `subcommander-launched` wire record only for an existing
  exact candidate and arm one five-minute `goal.handoff.receive` key. Use a
  short per-connection deadline and close every connection. Keep state
  process-local and do not write it to disk.

- [ ] **Step 4: Run the primitive tests**

  Run: `go test ./cmd/atct -run '^TestCodexMonitorAck' -count=1`

  Expected: PASS.

### Task 2: Bind a commander monitor's listener to its TUI lifecycle

**Files:**

- Modify: `cmd/atct/codex_monitor_supervisor.go`
- Modify: `cmd/atct/codex_monitor_lifecycle_test.go`
- Modify: `cmd/atct/codex_monitor.go`
- Modify: `cmd/atct/main.go`
- Test: `cmd/atct/codex_monitor_test.go`
- Test: `cmd/atct/main_test.go`

**Consumes:** Task 1's listener and `codexMonitorBridge.LineSinkWithContext`.

**Produces:** An acknowledgement set that is passed to the commander watch
sink, plus a listener path/capability inherited only by the commander TUI and
its explicit, exact subcommander monitor child.

- [ ] **Step 1: Write failing lifecycle and watch tests**

  Add a supervisor test that captures the TUI launch environment and asserts
  it has nonempty acknowledgement socket/capability values only for explicit
  or automatic commander scope; assert the App Server launch and noncommander
  TUI do not receive them. Add CLI parsing tests that reject `--handoff`
  without `--role subcommander` and reject a subcommander lifecycle launch
  missing either exact ID. Add child-start tests proving a child sends its
  authenticated launch record only after its monitor setup succeeds and only
  when both explicit IDs are supplied. Add watch-sink tests for: acknowledged
  matching action line is not enqueued; unmatched and second matching lines
  are enqueued; a line before `Acknowledge` is enqueued.

- [ ] **Step 2: Run the focused tests to verify failure**

  Run: `go test ./cmd/atct -run '^(TestCodexMonitor.*(Acknowledg|Environment|LocalMutation)|TestRunCodexMonitor.*Acknowledg)' -count=1`

  Expected: FAIL because no launch environment or sink suppression exists.

- [ ] **Step 3: Implement commander-only setup and fail-open cleanup**

  Extend process launch dependencies so only the TUI receives extra
  environment entries. For commander scope, create the listener below the
  existing protected monitor directory, start its serve goroutine with the
  monitor context, pass its set into the bridge/watch sink, and remove the
  socket in every setup-failure and normal cleanup branch. If setup fails,
  continue with no suppression rather than disabling Codex monitoring. Extend
  `codex monitor` parsing with `--handoff`; permit it only for a subcommander
  child with `--goal`. After that child has successfully set up its own monitor
  process, send the launch record through its inherited parent capability;
  failures to send are ignored and never prevent the child monitor from
  running. Remove the parent acknowledgement environment before the child
  launches its own App Server or TUI so its `atct-mcp` server cannot emit any
  parent-monitor response acknowledgements.

- [ ] **Step 4: Gate the bridge sink with consume-before-enqueue**

  Add a line-to-key parser beside `isCodexMonitorActionLine`. In
  `LineSinkWithContext`, preserve action filtering and queue retry behavior,
  but return nil without `Enqueue` only when the parser yields a key that the
  commander-local acknowledgement or lifecycle-armed set consumes. Do not
  change watch rendering or global delivery maps.

- [ ] **Step 5: Run focused monitor tests**

  Run: `go test ./cmd/atct -run '^(TestCodexMonitor.*(Acknowledg|Environment|LocalMutation)|TestRunCodexMonitor.*Acknowledg|TestCodexMonitorQueue)' -count=1`

  Expected: PASS.

### Task 3: Acknowledge only successful MCP response writes

**Files:**

- Create: `cmd/atct-mcp/ack_transport.go`
- Modify: `cmd/atct-mcp/main.go`
- Test: `cmd/atct-mcp/ack_transport_test.go`
- Test: `cmd/atct-mcp/main_test.go`

**Consumes:** Task 1's wire protocol and the MCP Go SDK's `Transport` /
`Connection` interfaces.

**Produces:** A stdio transport wrapper that records a supported incoming
`tools/call` mutation and emits its key only after the matching successful
JSON-RPC response write completes.

- [ ] **Step 1: Write failing matrix-driven transport and parser tests**

  Use table rows for all 17 response-correlated mappings in the specification.
  For each, feed an in-memory fake `tools/call` request and its JSON-RPC
  success response containing the stated IDs; assert the transport produces
  exactly the stated tuple and the exact formatted bridge line consumes it.
  Add a separate assertion that the lifecycle-only `goal.handoff.receive` row
  is never transport-produced. Add table rows for every listed unsupported
  mutation and each read-only tool; assert no key is produced. Add no-key
  cases for missing/invalid IDs, daemon/tool errors, mismatched
  request/response IDs, failed writes, missing monitor environment, and
  invalid capability.

- [ ] **Step 2: Run the transport tests to verify failure**

  Run: `go test ./cmd/atct-mcp -run '^TestAckTransport' -count=1`

  Expected: FAIL because the wrapper is absent.

- [ ] **Step 3: Implement response-write correlation**

  Wrap `mcp.StdioTransport` rather than individual tool handlers. On `Read`,
  retain only the request ID and name of a matrix-listed mutation. On `Write`,
  decode the matching JSON-RPC success result with the matrix's exact ID
  fields, derive its closed action class, then call the underlying connection;
  only after that write succeeds may it report the tuple. The watch parser
  accepts only the same closed action classes and all IDs in the tuple.
  Construct the wrapper in `main` only when both environment values validate;
  otherwise use the existing stdio transport unchanged. Reporting failures are
  ignored after the MCP response write, preserving fail-open behavior.

- [ ] **Step 4: Run focused MCP tests**

  Run: `go test ./cmd/atct-mcp -run '^(TestAckTransport|TestMain.*MonitorAck)' -count=1`

  Expected: PASS.

### Task 4: Verify cross-process behavior and regressions

**Files:**

- Modify: `cmd/atct/codex_monitor_lifecycle_test.go`
- Modify: `cmd/atct/codex_monitor_test.go`
- Modify: `cmd/atct-mcp/ack_transport_test.go`

**Consumes:** Tasks 1-3.

**Produces:** End-to-end evidence that the ordering and session isolation
requirements survive the real monitor/MCP boundary.

- [ ] **Step 1: Add the ordering and matrix-isolation integration cases**

  Exercise a fake commander TUI/MCP connection and watch bridge. Assert that:
  response-write acknowledgement followed by its action line causes zero
  turns; action line before acknowledgement causes one turn; a second same-key
  action line causes one turn; a same-ID line with a different action class
  causes one turn; and a different monitor capability cannot suppress a local
  line. Repeat the exact-key case for one task, goal, and plan matrix row.
  Add lifecycle ordering rows: request ACK then exact launched child then
  receipt causes zero turns; receipt before launch, launch before request ACK,
  mismatched goal or handoff, expired candidate, failed child setup/no launch
  signal, a duplicate receipt, and a receipt from an independently started or
  remote subcommander each causes one turn. Verify a remote
  `goal.handoff.receive` cannot consume either response or lifecycle state.
  Verify the child strips the inherited capability before its App Server/TUI
  start, so an MCP mutation made by that child cannot acknowledge anything to
  the parent monitor.

- [ ] **Step 2: Run focused integration tests**

  Run: `go test ./cmd/atct -run '^(TestCodexMonitor.*(LocalMutation|Acknowledg|Isolation|Ordering)|TestRunCodexMonitor.*Acknowledg)' -count=1 && go test ./cmd/atct-mcp -run '^TestAckTransport' -count=1`

  Expected: PASS.

- [ ] **Step 3: Run package regressions and static diff checks**

  Run: `go test ./cmd/atct -count=1 && go test ./cmd/atct-mcp -count=1 && git diff --check`

  Expected: exit 0.

  Then run each untracked-document check separately:
  `git diff --no-index --check /dev/null doc/specs/2026-09-06-commander-monitor-local-mutation-suppression.md`
  and
  `git diff --no-index --check /dev/null doc/plans/2026-09-06-commander-monitor-local-mutation-suppression.md`.
  Each normally exits 1 because it compares a new file with `/dev/null`; it
  must produce no whitespace-error output.

## Review matrix

- Same commander monitor: successful MCP stdout response write -> matching ACK
  -> one matching watch line suppressed.
- Race: matching line -> ACK leaves the line delivered; no delayed decision is
  introduced.
- Remote/unmonitored/noncommander session: no matching local capability ->
  line delivered.
- Failure: failed daemon call, tool error, write error, listener error, or
  malformed capability -> line delivered.
- Delegated lifecycle: only response-acknowledged local request + successful
  authenticated exact child launch -> one receipt line suppressed; a launch
  signal alone, stale/mismatched state, independent/remote receipt, and every
  timing failure -> line delivered.
- Lifecycle: listener capability and socket exist only for the commander TUI
  lifetime and are cleaned up with the monitor.
- Matrix: all 17 response-correlated tools map to their exact action class,
  the one lifecycle-only receipt mapping is listener-only, and every listed
  unsupported/read-only tool reports no key.
