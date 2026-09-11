# Development Escalation

## Outcome

`atct:dev:start` marks the identified agent session as development mode. Only
that marked session may use the parent-role development override; after it
resolves an escalation, it creates a remediation Goal.

## Data and API

Add `development_mode` to `agent_sessions` and expose an MCP tool that sets it
only through `atct:dev:start`. Do not add an escalation table. The remediation
Goal is the durable record of the source condition, resolution, verification,
and permanent fix.

## Rules

- executor escalates only to its subcommander; subcommander only to commander.
- A subcommander may act as executor; a commander may act as subcommander but
  never executor.
- Development-mode operations may include publish, history rewrite, discard,
  and delete when needed to resolve the escalated condition.
- Normal sessions retain existing restrictions.
- The remediation Goal contains both cause and permanent solution and follows
  the normal canonical spec/plan flow.
