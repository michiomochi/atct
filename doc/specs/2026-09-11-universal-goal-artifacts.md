# Universal Goal Artifacts

## Outcome

Every ATCT goal persists a canonical Markdown spec and plan in `goal.spec` and
`goal.plan`, then submits them for plan review. The artifact size follows the
brainstorming classification; its existence does not.

## Requirements

| Classification | Canonical spec | Canonical plan |
|---|---|---|
| spike | question, scope, and evidence/decision criteria | investigation steps, evidence to collect, and recommendation output |
| bounded | objective, changed surface, and acceptance criteria | implementation, verification, and completion steps |
| architectural | design, constraints, alternatives, and acceptance criteria | ordered implementation and verification tasks |

- Each artifact is complete Markdown, not a path or a chat-only summary.
- `atct_goal_update_request_report` stores both artifacts before
  `atct_plan_handoff_review_request`.
- A spike plan describes the investigation; it must not invent implementation
  work. A later implementation is a separate goal.
- Plan review rejects a Goal whose stored spec or plan is blank. This adds no
  database or MCP schema.
