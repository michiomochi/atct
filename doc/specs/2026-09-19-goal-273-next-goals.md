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
  a provenance/lineage relation, while `next_goals` is an ordered planning
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
for a self-link, and rejects duplicate IDs. The ordered input list is the
display order. An empty list is valid.

### Table shape

```sql
CREATE TABLE next_goals (
  goal_id INTEGER NOT NULL REFERENCES goals(id),
  next_goal_id INTEGER NOT NULL REFERENCES goals(id),
  sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
  created_at TEXT NOT NULL,
  PRIMARY KEY (goal_id, next_goal_id),
  UNIQUE (goal_id, sort_order),
  CHECK (goal_id <> next_goal_id)
);
CREATE INDEX idx_next_goals_next_goal_id ON next_goals(next_goal_id);
```

The relation is current state per source goal, not a history row per review
request. The order and links are replaced atomically with a new review/complete
report. A rejected review leaves the previous report and links untouched.
Finalization reads the stored report and does not rewrite the links.

Withdrawal leaves links in place: the relationship remains useful as a record
of intended follow-up, and the target's status is visible when queried. There
is no goal deletion workflow to cascade from. A later target withdrawal also
does not remove the link.

### Removing `next_steps` without losing old text

The next migration rebuilds the `goals` table without the public `next_steps`
column and preserves its old values in an internal `legacy_next_steps` column.
The column is not mapped into the domain model, MCP/HTTP responses, or UI; it
is migration residue for auditability only. Existing prose is preserved
verbatim and is never auto-converted into `next_goals` links.

The migration is the next sequential migration (currently expected to be
0044) and advances the logical schema version from 6 to 7. `schema.sql`,
schema validation, SQLC output, and migration tests must describe the same
post-migration shape. There is no downgrade path.

Done-goal validation keeps the five report fields that remain meaningful:
`work_done`, `now_possible`, `how_to_verify`, `surprises`, and `needs_review`.
An empty successor list is allowed and is not a completion error.

### Report and API contract

- Remove `next_steps` from the public completion-report domain type, goal
  model, goal-review request, goal-complete request, MCP schemas, HTTP/React
  models, and completion-report UI.
- Add optional ordered `next_goal_ids` to review/complete input. Omitting it
  means no successor links. Existing callers that omit `next_steps` continue
  to work; callers that still send `next_steps` receive an explicit unknown or
  unsupported-field error rather than silently losing text.
- `goal.get` and the HTTP goal detail response expose ordered `next_goals`
  summaries containing the target ID, headline, and status. The summaries do
  not recursively embed their own successor lists. Goal list responses do not
  grow a second full goal graph; callers can use detail for the summaries.
- The web completion report removes the `next_steps` field and translations
  and renders the ordered successor summaries as links, with a concise empty
  state when there are none.

The old prose is therefore removed from the public contract while existing
data remains available only for internal migration/audit purposes.

### Separation from derived-goal lineage

Do not reuse Goal 171's eventual generic relation for this migration. A
`derived_from` edge answers “where did this goal come from?” and needs lineage
rules such as self/cycle and parent cardinality decisions. A `next_goals`
edge answers “what should follow this goal?” and needs stable ordering plus
report lifecycle semantics. Combining them now would couple two migrations
with different owners and make either relation's constraints ambiguous.

## Cross-cutting constraints

- Work only in the Goal 273 worktree.
- Do not edit Goal 272, 294, 287, or 290 owned files as part of this goal.
  In particular, `internal/mcpshim/instructions.go` and
  `skills/atct/SKILL.md` need a follow-up by Goal 290 to describe the new
  contract; this is an external dependency, not a reason to modify those
  files here.
- Use existing SQLite, store, SQLC, HTTP, and web patterns; add no dependency.
- Keep report/link replacement transactional and preserve review rejection
  semantics.
- Do not merge, rebase, push, or run daemon operations.

## Acceptance criteria

1. A migrated database has `next_goals`, no public `next_steps` column, and
   preserves every old `next_steps` value in `legacy_next_steps`.
2. Review and complete requests accept ordered existing successor IDs, reject
   invalid/self/duplicate/cross-project IDs, and atomically replace links.
3. Rejection and withdrawal behavior matches the decisions above.
4. MCP and HTTP detail responses expose ordered successor summaries without
   recursive expansion; the UI can navigate to them.
5. No public model, request schema, response, or completion UI exposes
   `next_steps`.
6. Store, daemon/MCP, HTTP, migration, and web tests cover the changed paths,
   and the repository's normal verification commands pass.

## Coordination notes

The implementation plan is split by persistence, protocol, and presentation
boundaries so each executor has one coherent surface. After implementation,
Goal 290 should update the canonical skill and MCP instruction text, and Goal
171 should decide its lineage migration independently.
