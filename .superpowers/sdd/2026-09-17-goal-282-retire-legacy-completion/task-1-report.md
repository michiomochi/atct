# Task 1 report: retire legacy completion authority

## Result

Task 1 is implemented in the current worktree.

The Go completion authority was retired while the named goal-review flow and
the public `goal.complete` / `atct_goal_complete` transport shape remain
available. The compatibility entry point now returns the stable diagnostic:

`goal.complete is retired; use atct_goal_review_request followed by atct_goal_review_complete`

It returns that diagnostic before Store lookup or goal/decision mutation.

## Changes

- Removed `domain.KindCompletion`, the legacy Store completion methods, and
  completion-only SQLC queries and regenerated `internal/store/sqlcgen`.
- Removed HTTP completion approve/reject dispatch and the legacy goal-list
  completion authority check.
- Required a nonzero `agent_session_id`, an explicit handoff ID, and the
  reviewer-owned path for task and goal handoff completion. Session-zero
  completion returns:

  `agent_session_id is required; identify the session and use the named handoff review flow`

- Removed completion-decision suppression from Store wakeup evaluation.
- Updated pending/watch/MCP guidance to named goal-review operations.
- Reworked focused Go tests and E2E fixtures to cover retired completion
  diagnostics, named goal review, session identity guards, and nonzero
  reviewer completion.

The persisted wakeup transport contract was preserved. The constant and event
name `wakeup.completion_report_missing` remain in Store, daemon emission,
HTTP SSE/WebSocket paths, daemon tests, `cmd/atct watch_scope`, and related
tests. Only the Store suppression decision and human-facing guidance changed.

No migration 0044 was created. No web, skill, or unrelated documentation file
was edited; this requested report is the only report artifact added.

## Verification

Passed:

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./internal/store ./internal/daemon ./internal/httpapi ./internal/mcpshim ./internal/e2e ./cmd/atct -run 'Test.*(GoalComplete|Handoff|Completion|Pending|Wakeup|FullFlow|GoalReview)' -count=1
```

All six packages passed. `git diff --check` also passed.

The full unfiltered repository test suite and web-specific verification were
not run because the brief specifies the focused command and this task does not
modify web code.

## ATCT availability

`atct_session_identify`, `atct_task_handoff_receive`, `atct_role`, and the
review request (`atct_task_handoff_review_request`) could not run. The ATCT
PreToolUse monitor-check rejected the session before any ATCT tool call because
the session had never run `atct_session_identify`, matching the known Goal 295
infrastructure defect. The received task handoff was therefore used as the
implementation authorization, as instructed. After Goal 295 lands, the ATCT
record is expected to be repaired.
