# Force Start Commander Implementation Plan

**Goal:** Make `/atct:start` take the current project claim and verify commander role.

1. Add a failing daemon test proving `project.claim(force: true)` transfers a
   live claim and yields commander role.
2. Add the smallest store, daemon, and MCP `force` plumbing; preserve ordinary
   live-claim rejection.
3. Document the `/atct:start` sequence and verify the focused Go test plus the
   wrapper contract test.
