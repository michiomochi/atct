# SessionStart Execution-Flow Confirmation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox ("- [ ]") syntax for tracking.

**Goal:** Make both Codex and Claude Code tell an ATCT agent to read and follow `doc/execution-flow.md` before ATCT work begins.

**Architecture:** Keep the existing harness-specific SessionStart registrations unchanged. Both routes already call atct session-key --hook-input; extend its shared SessionStart message with one fixed execution-flow directive and prove the formatter, the Codex command, and the Claude wrapper route all preserve that shared CLI boundary.

**Tech Stack:** Go standard library tests and the existing Bash hook-contract test.

## Global Constraints

- Change neither dotfiles nor `doc/execution-flow.md`.
- Do not add a hook, dependency, daemon call, or file-existence check.
- The directive must be exactly: "Before any ATCT work, read `doc/execution-flow.md` and follow its procedure."
- Preserve the existing session-key and monitor-token instructions.
- The executor changes implementation and tests but does not commit; the subcommander commits only after task review.

---

## File Structure

- cmd/atct/session_key.go: formats the single SessionStart message consumed by both harnesses.
- cmd/atct/session_key_test.go: unit-tests the message with and without a monitor token.
- tests/wrapper_test.bash: verifies both installed SessionStart routes execute through the shared CLI, pass raw hook input through, and emit the directive.

### Task 1: Add and verify the shared SessionStart directive

**Files:**

- Modify: cmd/atct/session_key.go:42-52
- Modify: cmd/atct/session_key_test.go:5-19
- Modify: tests/wrapper_test.bash:248-322,381-410

**Interfaces:**

- Consumes: sessionKeyMessageWithMonitorToken(sessionID string, monitorToken string) string.
- Produces: a SessionStart message containing the unchanged handoff/session instruction and the exact execution-flow directive for both token variants; both Codex and Claude hook routes are covered by the Bash contract.

- [ ] **Step 1: Add exact failing expectations for both formatter variants and both hook routes**

Update the two existing want strings in cmd/atct/session_key_test.go to the exact values below:

~~~go
want := "ATCT session key: codex-session-1. When receiving a task or goal handoff, pass this exact session_key and monitor_token token-1 to its receive tool. Otherwise, call atct_session_identify with them before other ATCT operations. Before any ATCT work, read `doc/execution-flow.md` and follow its procedure.\n"

want := "ATCT session key: codex-session-1. When receiving a task or goal handoff, pass this exact session_key to its receive tool. Otherwise, call atct_session_identify with it before other ATCT operations. Before any ATCT work, read `doc/execution-flow.md` and follow its procedure.\n"
~~~

In tests/wrapper_test.bash, make the existing fake session-key command emit:

~~~text
ATCT session key: hook-session-1. Before any ATCT work, read `doc/execution-flow.md` and follow its procedure.
~~~

Update the Codex SessionStart assertion to that exact output. Add a separate test_session_start_hook_uses_shared_session_key_message function after test_session_start_is_silent_without_atct_wrapper that copies hooks/session-start into a temporary fixture and executes that Claude hook with raw input such as `{"session_id":"hook-session-1"}`. Its fake atct must return the fixture plugin version for version, exit successfully with no output for context -brief, and, for session-key --hook-input, record both the command and the stdin it received before emitting the same exact directive. Assert the hook's output and the exact invocation log:

~~~bash
assert_eq 'ATCT session key: hook-session-1. Before any ATCT work, read `doc/execution-flow.md` and follow its procedure.' "$output" 'Claude SessionStart hook must use the shared session-key output'
assert_eq $'version\ncontext -brief\nsession-key --hook-input\n{"session_id":"hook-session-1"}' "$(<"$log")" 'Claude SessionStart hook must pass raw input through the shared CLI'
~~~

The fixture test must execute `hooks/session-start` itself; checking only the Claude hook registration or the shared formatter is insufficient. Keep the existing missing-CLI and old-CLI compatibility assertions unchanged.

Register the new function in the test runner's function list at the bottom of the file.

- [ ] **Step 2: Run the focused red tests**

Run:

~~~bash
go test ./cmd/atct -run 'TestSessionKeyMessage'
~~~

Expected: FAIL because sessionKeyMessageWithMonitorToken does not yet include the directive.

Run:

~~~bash
bash tests/wrapper_test.bash
~~~

Expected: FAIL because the current fake CLI output lacks the new directive.

- [ ] **Step 3: Append one shared directive to the formatter output**

Add one package-level constant in cmd/atct/session_key.go:

~~~go
const sessionStartExecutionFlowDirective = " Before any ATCT work, read `doc/execution-flow.md` and follow its procedure.\n"
~~~

Append sessionStartExecutionFlowDirective to each existing fmt.Sprintf result in sessionKeyMessageWithMonitorToken. Do not alter runSessionKey, hooks/codex-hooks.json, hooks/claude-hooks.json, or hooks/session-start.

- [ ] **Step 4: Run focused verification**

Run:

~~~bash
go test ./cmd/atct -run 'TestSessionKeyMessage' -count=1
bash tests/wrapper_test.bash
~~~

Expected: PASS. The Go test proves both token variants; the Bash contract proves the Codex and Claude SessionStart routes invoke session-key --hook-input, emit the directive, and preserve compatibility guidance.

- [ ] **Step 5: Run the package check and inspect the diff**

Run:

~~~bash
go test ./cmd/atct
git diff --check -- cmd/atct/session_key.go cmd/atct/session_key_test.go tests/wrapper_test.bash
git diff -- cmd/atct/session_key.go cmd/atct/session_key_test.go tests/wrapper_test.bash
~~~

Expected: PASS, no whitespace errors, and only the shared message plus its tests change. The diff must not touch either hook definition, hooks/session-start, `doc/execution-flow.md`, or any daemon/MCP file.

- [ ] **Step 6: Request task review without committing**

Call atct_task_handoff_review_request with a non-empty report stating:

- the shared formatter now emits the exact directive for monitor-token and no-token cases;
- the Codex and Claude SessionStart paths were verified through their existing commands;
- go test ./cmd/atct -run 'TestSessionKeyMessage' -count=1, go test ./cmd/atct, and bash tests/wrapper_test.bash passed;
- git diff --check passed;
- changed paths are cmd/atct/session_key.go, cmd/atct/session_key_test.go, and tests/wrapper_test.bash;
- no commit was made, because the subcommander owns the goal commit after review.

## Plan Self-Review

- Spec coverage: Task 1 implements the one shared message, covers both token variants, executes and verifies both installed routes—including Claude raw-input forwarding—and leaves all stated exclusions untouched.
- Placeholder scan: no TBD/TODO markers or unspecified test behavior remain.
- Interface consistency: every step uses the existing sessionKeyMessageWithMonitorToken formatter and the existing hook commands; no new interface or dependency is introduced.
- ATCT boundary: the executor owns implementation, tests, and review request; the subcommander owns review and the later goal commit.
