# execution-flow 残差分 Implementation Plan

> **For agentic workers:** execute inline in this session. Do not create a second task orchestration loop.

**Goal:** Make handoff receipt identify the caller, remove generic task-handoff names, and prevent agents from self-claiming implementation tasks.

**Architecture:** Keep `session.identify` as the canonicalization operation, but invoke it inside each MCP handoff-receive handler before recording receipt. Delete generic task-handoff adapters and the agent-facing self-claim surface; retain the task-specific handoff state machine and store-only recovery support.

**Tech Stack:** Go, MCP SDK, daemon RPC, Bash wrapper contracts.

## Global Constraints

- Preserve `atct_session_identify` for explicit reconnect and monitor-token binding.
- Canonical task and goal receive tools require `session_key`; `monitor_token` is optional.
- Delete `atct_handoff_*`, `handoff.*`, `atct_task_claim`, `atct_task_release`, `task.claim`, and `task.release` from agent-facing contracts.
- Do not remove store-level claim/release support used for human recovery and existing-data verification.
- Use TDD: observe each new test fail before production code changes.

---

### Task 1: Bind handoff receipt to a stable session

**Files:**
- Modify: `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go`
- Modify: `cmd/atct/session_key.go`, `cmd/atct/session_key_test.go`
- Modify: `README.md`, `skills/atct/SKILL.md`, `skills/start/SKILL.md`, `tests/wrapper_test.bash`

**Interface:** `TaskHandoffReceiveIn` and `GoalHandoffReceiveIn` gain required `SessionKey string` and optional `MonitorToken string`. Their handlers call `callSessionIdentify`, then submit the receive RPC using the canonical ID stored in `agentSessionIDHolder`.

- [ ] Write capture-daemon tests that call each receive tool with `session_key: "receiver-key"` and `monitor_token: "token-1"`; assert `session.identify` occurs first and the canonical returned ID becomes `received_by`. Assert missing keys fail schema validation.
- [ ] Run `go test ./internal/mcpshim -run 'Test.*HandoffReceive.*SessionKey' -count=1` and confirm it fails because receive has no key input.
- [ ] Add the two input fields and the smallest shared identify-before-receive helper. Do not change store receive methods or daemon payload formats.
- [ ] Update the session-start message and active README/skill instructions: recipients pass key/token to receive; explicit identify is for non-receiving sessions or reconnects.
- [ ] Run `go test ./internal/mcpshim -run 'Test.*HandoffReceive.*SessionKey' -count=1`, `go test ./cmd/atct -run 'TestSession.*Key' -count=1`, and `bash tests/wrapper_test.bash`.
- [ ] Commit explicit paths with `git commit -m "feat: bind handoff receipt to session key"`.

### Task 2: Remove generic task-handoff names

**Files:**
- Modify: `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go`
- Modify: `internal/daemon/handler.go`, `internal/daemon/task_handoff_test.go`, `internal/daemon/web_test.go`
- Modify: `internal/store/task_handoff.go`
- Modify: `README.md`, `skills/atct/SKILL.md`, `skills/start/SKILL.md`, `tests/wrapper_test.bash`

**Interface:** only `atct_task_handoff_*` MCP names and `task.handoff.*` task RPC names remain. Add the missing `atct_task_handoff_report_amend` wrapper over the existing canonical daemon method.

- [ ] Change MCP tool-list assertions to reject all `atct_handoff_*` names and require `atct_task_handoff_report_amend`. Add negative daemon dispatch tests for `handoff.request`, `handoff.receive`, `handoff.complete`, and `handoff.report.amend`.
- [ ] Run `go test ./internal/mcpshim ./internal/daemon -run 'Test.*(Legacy|Generic|TaskHandoff).*' -count=1` and confirm it fails while generic aliases exist.
- [ ] Remove generic MCP registrations and generic daemon switch aliases. Register the canonical amend tool, change task-handoff error guidance, and replace active instruction references with canonical names.
- [ ] Re-run the focused command and `bash tests/wrapper_test.bash`.
- [ ] Commit explicit paths with `git commit -m "refactor: remove generic task handoff APIs"`.

### Task 3: Require delegation for agent task execution

**Files:**
- Modify: `internal/mcpshim/tools.go`, `internal/mcpshim/schema_test.go`, `internal/mcpshim/instructions.go`, `internal/mcpshim/notifications_test.go`
- Modify: `internal/daemon/handler.go`, `internal/daemon/handler_test.go`, `internal/daemon/pending_response_test.go`, `internal/daemon/server_test.go`, `internal/daemon/task_handoff_test.go`
- Modify: `cmd/atct/context.go`, `cmd/atct/context_test.go`, `cmd/atct/pending.go`
- Modify: `README.md`, `skills/atct/SKILL.md`, `skills/start/SKILL.md`, `tests/wrapper_test.bash`

**Interface:** agent-facing MCP and daemon APIs do not expose self task claim/release. A subcommander requests handoff; an executor owns work only after receive.

- [ ] Remove task claim/release from MCP expected-tool tests; add negative daemon dispatch tests. Change context/pending tests to forbid `atct_task_claim` and require `atct_task_handoff_request` where delegation is actionable.
- [ ] Run `go test ./internal/mcpshim ./internal/daemon ./cmd/atct -run 'Test.*(TaskClaim|TaskRelease|Context|Pending)' -count=1` and confirm it fails while the self-claim surface exists.
- [ ] Remove MCP registrations and daemon cases, retaining `Store.ClaimTask` and `ReleaseTaskForHuman`. Replace active instructions and recommendations with delegation; add no replacement self-claim path.
- [ ] Re-run the focused command and `bash tests/wrapper_test.bash`.
- [ ] Commit explicit paths with `git commit -m "feat: require task delegation for agent work"`.

### Task 4: Full verification

**Files:** none expected.

- [ ] Run `GOCACHE=/private/tmp/atct-go-cache go test ./... -count=1`, `bash tests/wrapper_test.bash`, and `git diff --check`.
- [ ] If a check fails, return to the task that owns the failing contract; do not add an unrelated regression-fix task.

## Plan review

The plan covers every requirement in `doc/specs/2026-09-12-execution-flow-remaining-gaps.md`. It deliberately reuses canonical session identification and store recovery helpers, avoiding a second session model or replacement claim API.
