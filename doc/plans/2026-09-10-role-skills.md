# Role Skills Implementation Plan

**Goal:** Route ATCT agents to role-specific operating skills without duplicating common rules.

1. Add a failing wrapper contract for the three role skills and shared router.
2. Add `commander`, `subcommander`, and `executor` skills containing only their
   role operations and role check.
3. Add the router to `atct:atct`, then run the wrapper contract and full Go tests.
