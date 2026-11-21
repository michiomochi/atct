# Goal-review guidance lifecycle

## Problem

`skills/atct/SKILL.md` tells a commander to call `atct_goal_handoff_complete`
before `atct_goal_review_request`. The store intentionally rejects that
sequence: human review requires the latest delegated handoff to be received for
review by that commander and still open. Closing it early drops the
subcommander role and makes a human rejection require recovery.

## Scope

Correct only shared ATCT guidance and its executable documentation contract.
Do not change Goal 246 receipt-schema work, goal creation state, existing-goal
cleanup, or the store, daemon, MCP, and human-review implementation. The
existing `doc/execution-flow.md` and `internal/store/goal_handoff_test.go`
already describe and cover the intended open-handoff behavior.

## Required lifecycle

1. The subcommander requests goal-handoff review.
2. The commander receives that review and keeps the handoff open.
3. The commander requests human goal review.
4. Human rejection is translated to `atct_goal_handoff_review_reject`; the
   subcommander revises through the same handoff.
5. After human approval and merge, the commander calls
   `atct_goal_review_complete`, atomically completing the reviewed handoff and
   the goal.

`atct_goal_handoff_complete` is not part of the normal goal-review path. Its
legacy/out-of-order use remains a recovery case, not an instruction before
human review.

## Verification

Extend `tests/wrapper_test.bash` so it fails for the obsolete ordering and
passes only when the documented normal path places `atct_goal_review_request`
after commander review receipt and before `atct_goal_review_complete`. Run that
script plus focused Go lifecycle tests for the open, reviewed-handoff guard.
