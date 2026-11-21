# Force commander on start

`/atct:start` must promote its own identified session to commander before it
works on goals. The promotion replaces any existing live project claim
for the current project. Ordinary project claims remain non-preemptive.

The MCP project-claim input exposes `force: true`; the daemon passes it to the
store, which transfers the claim atomically through the existing project row.
The start skill resolves the current project, forces that claim, and verifies
`atct_role(expected_role="commander")` before monitor setup.
