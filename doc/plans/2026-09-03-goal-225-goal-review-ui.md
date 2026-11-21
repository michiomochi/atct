# Goal 225 GoalDetail goal-review UI plan

**Goal:** Make an open human `goal_review` decision actionable on GoalDetail while preserving the existing completion and proposed-goal approval forms.

**Architecture:** Reuse GoalDetail's existing taskless-decision data (`unattached_decisions`) and generic decision API functions. Add one `goal_review` selector and a dedicated client-side card; no server or lifecycle code changes.

**Tech stack:** React, TypeScript, Vitest, existing i18n and decision HTTP client.

## Constraints

- Do not alter goal/handoff/decision lifecycle semantics, shared daemon, chezmoi, configuration, or other decisions.
- Use only `approveDecision` and `rejectDecision` for the human answer; never call `goal.complete` from the web UI.
- Preserve the existing `completion` and `goal_approval` rendering conditions and request behavior.
- No implementation starts until commander accepts this plan through the received goal-handoff review.

## Work unit 1: GoalDetail review card and regression coverage

**Files:**

- Modify: `web/src/lib/ui.ts`
- Modify: `web/src/components/GoalDetail.tsx`
- Modify: `web/src/components/GoalDetail.test.tsx`
- Modify: `web/src/i18n/en.ts`
- Modify: `web/src/i18n/ja.ts`

**Steps:**

1. Add a focused failing GoalDetail test fixture for an open taskless `goal_review`. Assert its question and approve/reject controls render, rejection is disabled while its reason is blank, approval calls `approveDecision(reviewID)`, rejection calls `rejectDecision(reviewID, reason)`, and either successful answer reloads the page.
2. Add a `findOpenGoalReview` helper alongside the existing completion and goal-approval selectors. Add localized review-card text in both locales.
3. Add the review state/reason and a dedicated GoalDetail review card. Match existing form conventions for submitting, server errors, conflict refresh, and accessible labels; keep its branch separate from completion and proposed-goal approval.
4. Extend tests to assert a review response does not render or invoke completion/proposed-goal-specific behavior, and retain the existing completion/proposed-goal regression tests.
5. Run `npm --prefix web test -- GoalDetail.test.tsx` and `git diff --check`; commit only the five listed paths.

## Review checklist

- Verify the page reads `goal_review` only from `unattached_decisions`, so task-bound decisions stay in TaskTable.
- Verify approve/reject target the exact review decision ID and successful answers reload the view.
- Verify no UI action issues a goal completion request, and no change affects the existing `completion` or `goal_approval` branches.
- Verify Japanese and English labels are both present and the reason-required behavior is test-covered.
