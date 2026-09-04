# Normal decision Goal Detail restoration implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use ATCT task handoffs task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the existing generic answer path for open taskless ordinary decisions on GoalDetail without changing APIs or specialized decision flows.

**Architecture:** GoalDetail filters the existing `GoalResponse.unattached_decisions` payload to open `kind: "decision"` records and passes them to a restored presentation component. The component reuses `DecisionAnswerForm`, which already owns generic answer submission and calls GoalDetail's reload callback. Specialized cards and TaskDetail remain separate.

**Tech Stack:** React, TypeScript, Vitest, Testing Library, existing ATCT HTTP API.

## Global Constraints

- Reuse `GoalResponse.unattached_decisions`, `DecisionAnswerForm`, and the existing generic decision-answer API; add no API, daemon, store, query, migration, snapshot, or decision framework.
- Filter only open `kind: "decision"` taskless records; completion, `goal_approval`, and `goal_review` remain on their existing specialized paths.
- Do not change `TaskDetailPage.tsx`, Decision #638, `internal/store/migrations/0026_goal_review_snapshots.sql`, or Goal 231/232/236 files.
- Run only the focused UI test, web typecheck, and `git diff --check` named below.

---

### Task 1: Restore the taskless ordinary decision card

**Files:**

- Create: `web/src/components/UnattachedDecisionList.tsx`
- Modify: `web/src/components/GoalDetail.tsx`
- Modify: `web/src/components/GoalDetail.test.tsx`

**Interfaces:**

- Consumes: `GoalResponse.unattached_decisions: Decision[]`, whose entries include `kind`, `status`, `id`, question, and options.
- Consumes: `DecisionAnswerForm({ decision, onUpdated })`, which submits through the existing generic answer route.
- Produces: `UnattachedDecisionList({ decisions, onRefresh })`, rendering only the passed ordinary open decisions and returning `null` when empty.

- [ ] **Step 1: Write failing GoalDetail regressions**

Add a fixture ordinary decision with `kind: "decision"`, `status: "open"`, at least two options, and no `task_id`. Add a GoalDetail test that puts it in `unattached_decisions` and asserts that the ordinary-decision region, question, label combobox, answer textbox, and submit button render. Add a second assertion that completion, goal approval, and goal review fixtures do not appear in that region. Keep the existing TaskDetail tests unchanged.

- [ ] **Step 2: Run the focused test before implementation**

Run: `npm --prefix web test -- GoalDetail.test.tsx`

Expected: FAIL because GoalDetail has no ordinary taskless decision renderer.

- [ ] **Step 3: Restore the existing component boundary**

Create `UnattachedDecisionList.tsx` with the former focused boundary: accept `Decision[]` and an `onRefresh` callback; return `null` for an empty list; otherwise render a labelled section with one `DecisionAnswerForm` per decision. In `GoalDetail.tsx`, derive `ordinaryUnattachedDecisions` from `goal.unattached_decisions` with `decision.kind === "decision" && decision.status === "open"`, store it in `GoalDetailData`, and render the list after specialized completion/approval/review sections. Pass `load` as `onRefresh`.

Do not route specialized records through this component. Do not modify API calls or TaskDetail.

- [ ] **Step 4: Run regression and compatibility tests**

Run: `npm --prefix web test -- GoalDetail.test.tsx i18n.test.ts`

Expected: PASS, including the new ordinary-decision rendering regression and existing completion/goal-approval/goal-review cases.

Run: `npm --prefix web run typecheck`

Expected: 0 errors.

Run: `git diff --check`

Expected: no output and exit 0.

- [ ] **Step 5: Commit the implementation**

```bash
git add web/src/components/UnattachedDecisionList.tsx web/src/components/GoalDetail.tsx web/src/components/GoalDetail.test.tsx
git commit -m "fix: restore taskless decision answers on goals" -- web/src/components/UnattachedDecisionList.tsx web/src/components/GoalDetail.tsx web/src/components/GoalDetail.test.tsx
```

## Plan review gate

Submit this plan through `atct_plan_handoff_review_request` and wait for commander acceptance before declaring or delegating the implementation task.
