# Goal 273: Persistent Next Goals

## Purpose

Replace the free-form `next_steps` completion-report field with persistent,
queryable successor links in a new `next_goals` table. A completion report
should be able to say which existing goals should be considered next without
encoding ownership or sequencing in prose.

## Survey evidence

The repository and the ATCT database were surveyed on 2026-09-19.

- `next_steps` is part of the domain completion report, store queries and
  validation, daemon and MCP inputs, HTTP/React output, translations, tests,
  and the current ATCT skill/instructions.
- The database contained 295 goals. 190 had non-empty `next_steps`, including
  178 done goals and 6 active goals. The text is mostly merge/finalization
  prose, not a stable reference to a successor goal.
- Goal 221 described a merge in `next_steps`; goal 251 said the commander
  would finalize work; goals 294, 286, 287, and 272 used the field for work
  owned elsewhere. These values cannot be safely parsed into links.
- `derived_from_goal_id` is used by 29 goals. One parent already has five
  children, so parent-to-many is real, but the current column still permits
  only one parent per child.
- Goal 171 proposes replacing `derived_from_goal_id` with a relation. That is
  a provenance/lineage relation, while `next_goals` is a planning
  relation. They have different lifecycle and constraint semantics and remain
  separate in this goal.

## Decisions

### Link existing goals only

`next_goals` stores links to goals that already exist. It does not contain
inline proposal text and it does not create a goal as a side effect. When the
next goal does not exist, the caller creates it through the existing goal
creation flow and then submits its ID in the completion report. This prevents
duplicate goal content and reuses the existing approval/lifecycle rules.

The link is restricted to an existing goal in the same project, is rejected
for a self-link, and rejects duplicate IDs. The links are a set with no
caller-defined order; readers list them by `next_goal_id` ascending. An empty
list is valid.

### Table shape

```sql
CREATE TABLE next_goals (
  goal_id INTEGER NOT NULL REFERENCES goals(id),
  next_goal_id INTEGER NOT NULL REFERENCES goals(id),
  created_at TEXT NOT NULL,
  PRIMARY KEY (goal_id, next_goal_id),
  CHECK (goal_id <> next_goal_id)
);
CREATE INDEX idx_next_goals_next_goal_id ON next_goals(next_goal_id);
```

The relation is current state per source goal, not a history row per review
request. The links are replaced atomically with each review request. A rejected
review changes nothing further: like the text report fields, the links stay as
the rejected request set them and the next review request replaces them. No
snapshot table, restore step, or migration exists for this; main's
`RejectGoalReview` never restored the previous report, and a table of that kind
(`goal_review_snapshots`, migration 0026) was already dropped in 0028.
Finalization reads the stored report and does not rewrite the links.

Withdrawal leaves links in place: the relationship remains useful as a record
of intended follow-up, and the target's status is visible when queried. There
is no goal deletion workflow to cascade from. A later target withdrawal also
does not remove the link.

### Removing `next_steps`

The migration rebuilds the `goals` table without the `next_steps` column and
does not keep its values anywhere: no `legacy_next_steps` column, no copy
table. The human decided on 2026-10-03 (decision 855) that the old prose need
not be preserved. Existing prose is therefore discarded and never converted
into `next_goals` links.

One migration, `0046_next_goals.sql`, creates `next_goals` and drops the column
(0045 is Goal 282's `0045_retire_legacy_completion.sql` on main). It advances
the logical schema version by one. None of this goal's migrations has been
applied to any shared database, so the file is edited in place rather than
adding a second migration. `schema.sql`, schema validation, SQLC output, and
migration tests must describe the same post-migration shape. There is no
downgrade path.

Done-goal validation keeps the five report fields that remain meaningful:
`work_done`, `now_possible`, `how_to_verify`, `surprises`, and `needs_review`.
An empty successor list is allowed and is not a completion error.

### Report and API contract

- Remove `next_steps` from the public completion-report domain type, goal
  model, goal-review request, MCP schemas, HTTP/React models, and
  completion-report UI.
- Add optional `next_goal_ids` (a set of goal IDs, no order) to the goal-review
  request. Omitting it means no successor links. Goal 282 retired
  `goal.complete` / `atct_goal_complete`; that call keeps its stable retirement
  diagnostic and gains no new parameter. Callers that still send `next_steps`
  to `atct_goal_review_request` receive an explicit unknown or
  unsupported-field error rather than silently losing text.
- `goal.get` and the HTTP goal detail response expose `next_goals` summaries
  (target ID, headline, status) sorted by target ID. The summaries do not
  recursively embed their own successor lists. Goal list responses do not grow
  a second full goal graph; callers can use detail for the summaries.
- The web completion report removes the `next_steps` field and translations
  and renders the successor summaries as links, with a concise empty state when
  there are none.

### Separation from derived-goal lineage

Do not reuse Goal 171's eventual generic relation for this migration. A
`derived_from` edge answers “where did this goal come from?” and needs lineage
rules such as self/cycle and parent cardinality decisions. A `next_goals`
edge answers “what should follow this goal?” and follows the report lifecycle. Combining them now would couple two migrations
with different owners and make either relation's constraints ambiguous.

## Cross-cutting constraints

- Work only in the Goal 273 worktree.
- Do not edit Goal 272, 294, 287, or 290 owned files as part of this goal.
  In particular, `internal/mcpshim/instructions.go` and
  `skills/atct/SKILL.md` need a follow-up by Goal 290 to describe the new
  contract; this is an external dependency, not a reason to modify those
  files here.
- Use existing SQLite, store, SQLC, HTTP, and web patterns; add no dependency.
- Keep report/link replacement transactional.
- Do not push or run daemon operations. Merging current main into the worktree
  is permitted (Goal 282 is already on main and conflicts with this branch).

## Acceptance criteria

1. A migrated database has `next_goals` (no `sort_order`), no `next_steps`
   column, and no `legacy_next_steps` column or other copy of the old text.
2. The review request accepts existing successor IDs, rejects
   invalid/self/duplicate/cross-project IDs, and atomically replaces links.
3. Rejection leaves the links as the rejected request set them; withdrawal
   leaves links in place. No snapshot table exists.
4. MCP and HTTP detail responses expose successor summaries sorted by target ID
   without recursive expansion; the UI can navigate to them.
5. No public model, request schema, response, or completion UI exposes
   `next_steps`.
6. Store, daemon/MCP, HTTP, migration, and web tests cover the changed paths,
   and the repository's normal verification commands pass.

## Coordination notes

The implementation plan is split by persistence, protocol, and presentation
boundaries so each executor has one coherent surface. After implementation,
Goal 290 should update the canonical skill and MCP instruction text, and Goal
171 should decide its lineage migration independently.
