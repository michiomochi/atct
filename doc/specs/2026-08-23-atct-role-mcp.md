# MCP role verification

## Scope

Expose the daemon's existing `session.role` judgment through the MCP server as
an agent-facing role-verification tool. The existing command-line role check
remains available for hooks and monitoring.

## Contract

- The tool uses the MCP server's bound agent session when calling
  `session.role`; the session identity is not caller-supplied.
- It accepts an optional `expected_role`, corresponding to the command-line
  check's expectation argument.
- A valid expectation mismatch is a normal structured result: the result keeps
  the actual role and its `project_id`/`goal_id` evidence and adds
  `expected_role` plus `matches: false`. An invalid expected role is a tool
  error, because it is an invalid request rather than a role judgment.
- The MCP adapter does not reimplement role classification. It calls
  `session.role`, the same daemon method used by the existing command-line
  check, and only adds the optional expectation comparison to that response.

## Verification

The MCP integration test uses a real daemon and store with two fixtures for
each role: commander, subcommander, and executor. Every returned role is
checked together with its project and goal evidence. A mismatch fixture checks
that the result is structured and reports `matches: false`.
