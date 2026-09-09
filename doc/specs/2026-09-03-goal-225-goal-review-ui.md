# Goal 225: actionable human goal-review decision on GoalDetail

## Outcome

When a goal has one open, taskless `goal_review` decision, its GoalDetail page renders a visible, actionable approval form. A human can approve it or reject it with a reason without leaving the goal page.

This fixes the release-flow defect that withdrew decision #621: the database contained an open `KindGoalReview`, but GoalDetail extracted only `completion` and `goal_approval` from `unattached_decisions`, so no action was visible in the UI.

## Existing contract and evidence

- `GET /api/goals/:id` already returns taskless open decisions in `unattached_decisions` (`internal/httpapi/server.go` and `web/src/lib/api.ts`). No new response field or server route is required.
- `web/src/lib/api.ts` already provides generic `approveDecision(id)` and `rejectDecision(id, reason)`, using `POST /api/decisions/:id/approve` and `POST /api/decisions/:id/reject`.
- `GoalDetail` currently selects only `findOpenCompletion` and `findOpenGoalApproval`; its existing cards demonstrate the desired reload, submit, and error behavior.

## UI contract

1. GoalDetail finds the first open `goal_review` in `unattached_decisions`, independently of goal status, and renders it as a distinct review card.
2. The card states that the goal handoff awaits human review, shows the decision question, and offers Approve and Reject actions.
3. Approve calls the existing generic approve endpoint with that decision ID. Reject requires a non-blank reason and calls the existing generic reject endpoint with that decision ID and reason.
4. While submitting, both actions are disabled. A successful answer reloads GoalDetail; an error remains visible in the card. A conflict offers the existing explicit refresh affordance.
5. The completion card remains responsible only for `completion`, including its optional rejection reason and completion-report display. The proposed-goal approval card remains restricted to a proposed goal and `goal_approval`. No card substitutes for another kind.

## Lifecycle boundaries

- This is a presentation and existing-HTTP-client change only. It neither creates nor changes a decision, handoff, goal, task, event, cursor, daemon, or MCP transition.
- Approving `goal_review` does **not** call `goal.complete`, write the six-part completion report, or close a goal. Those remain commander-only operations under the accepted Goal 225 lifecycle.
- Rejecting `goal_review` retains the existing lifecycle: it records the human decision; any later re-handoff is an explicit commander action. This UI introduces no automatic reopen.

## Acceptance criteria

- A GoalDetail fixture containing an open taskless `goal_review` displays an accessible action card and its question.
- Approve and reject use only the generic existing decision endpoints with the visible decision ID; reject cannot submit without a reason.
- The component refreshes after either success and presents server/conflict failures without silently changing state.
- Existing completion and proposed-goal approval behavior remains covered and unchanged.
- No HTTP/store/daemon/MCP/migration/config/chezmoi path changes.
