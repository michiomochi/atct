# Normal decision Goal Detail restoration

## Problem

The goal endpoint still returns open taskless ordinary `kind: "decision"` records in `unattached_decisions`, and the existing generic `answerDecision` API remains usable. However, GoalDetail extracts only completion, goal approval, and goal review decisions. It does not render an answer form for ordinary taskless decisions.

Task-linked ordinary decisions are not affected: TaskDetail already renders its existing `DecisionAnswerForm` for a task's `open_decisions`.

The committed investigation records a read-only Playwright MCP reproduction against Goal 225 and traces the regression to `33547a4`, which removed the prior GoalDetail `UnattachedDecisionList` render path while leaving API, daemon, and store behavior intact.

## Decision

Restore the pre-existing GoalDetail answer path only for open, taskless ordinary `kind: "decision"` entries.

GoalDetail will:

1. Continue deriving the specialized open completion, goal approval, and goal review cards exactly as it does now.
2. Derive a separate ordinary list from `goal.unattached_decisions` by selecting `kind === "decision"` and `status === "open"`.
3. Render that list in a restored `UnattachedDecisionList` component, which reuses the existing `DecisionAnswerForm` and calls GoalDetail's existing reload callback after an answer.
4. Render nothing for an empty ordinary list.

The restored list is separate from the specialized cards so an ordinary decision cannot be rendered by a completion/approval/review card and no specialized decision can be duplicated by the ordinary list.

## Existing interfaces

- `GoalResponse.unattached_decisions` already supplies the taskless decisions; no response shape changes.
- `DecisionAnswerForm` already calls the generic existing answer API and handles its answer controls and errors.
- GoalDetail's `load` callback already reloads the response after a successful operation.
- TaskDetail continues to use `open_decisions` for task-linked decisions and is not changed.

## Acceptance criteria

- An active GoalDetail response with an open taskless ordinary decision renders its question, choice control, optional answer text field, and existing submit control.
- Submitting through that form invokes the existing generic decision answer route and reloads GoalDetail after success.
- Completion, goal approval, and goal review cards remain specialized and render exactly as before.
- A GoalDetail response without an open ordinary taskless decision renders no ordinary-decision section.
- TaskDetail's task-linked decision path remains unchanged.

## Non-goals and constraints

- Do not add a decision API, daemon/store/query change, migration, snapshot, decision history format, or new decision framework.
- Do not redesign GoalDetail or TaskDetail.
- Do not operate Decision #638 during development or verification.
- Do not modify/delete `internal/store/migrations/0026_goal_review_snapshots.sql` before Decision #638 is resolved.
- Do not touch Goal 231, 232, or 236 worktrees/files.
