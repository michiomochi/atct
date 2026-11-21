# Universal Goal Artifacts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Require type-appropriate canonical specs and plans for every ATCT goal.

**Architecture:** Keep the existing `goals.spec`, `goals.plan`, request-report MCP tool, and plan-review handoff. The plan-review store entry rejects blank artifacts; workflow documentation defines the type-specific contents.

**Tech Stack:** Markdown, ripgrep.

## Global Constraints

- Do not change the database, MCP schema, or role-specific skills.
- `doc/execution-flow.md` remains the operational source referenced by `atct:atct`.
- A spike records an investigation plan, not speculative implementation tasks.

---

### Task 1: Define universal goal artifacts

**Files:**
- Create: `doc/specs/2026-09-11-universal-goal-artifacts.md`
- Modify: `doc/execution-flow.md:143-158`
- Modify: `internal/store/goal_handoff.go:949-985`
- Test: `internal/store/plan_handoff_test.go`, `internal/mcpshim/task_create_handoff_test.go`

- [ ] Replace the classification table so spike, bounded, and architectural work each require canonical spec and plan content.
- [ ] Require `atct_goal_update_request_report` before `atct_plan_handoff_review_request` for every classification.
- [ ] Reject plan-review requests when either stored artifact is blank.
- [ ] Verify with `rg -n 'すべての Goal は canonical spec と plan を持つ|spike（調査）.*問い・scope|bounded（限定的）.*目的・変更面|atct_goal_update_request_report|atct_plan_handoff_review_request' doc/execution-flow.md`.
- [ ] Verify with `go test ./internal/store -run 'TestPlanHandoffReview' -count=1`.

### Task 2: Review the documentation change

**Files:**
- Modify: `doc/execution-flow.md`
- Test: `doc/execution-flow.md`

- [ ] Run `git diff --check -- doc/specs/2026-09-11-universal-goal-artifacts.md doc/plans/2026-09-11-universal-goal-artifacts.md doc/execution-flow.md`.
- [ ] Confirm the new text preserves type-specific scope and does not claim an API change.
