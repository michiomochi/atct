# Role-scoped Codex Stop Hook

## Goal

Prevent a Codex agent from stopping while ATCT has unfinished work assigned to
that agent's current role, without treating another role's work as its own.

## Scope

`atct codex monitor` supplies the resolved role and its project, goal, or task
scope to the Codex TUI. The plugin's Codex `Stop` hook invokes a read-only
`atct stop-check` command with that scope.

`stop-check` exits successfully with no output when the role has no remaining
work. When work remains, it writes the Codex Stop response:

```json
{"decision":"block","reason":"ATCT work remains: …"}
```

Codex then creates the continuation prompt from `reason`. The hook never
blocks again when its input says `stop_hook_active: true`, preventing a
continuation loop.

## Role-specific work

- **executor:** the monitor's task is still an open, received task handoff.
- **subcommander:** the monitor's goal still has an open goal, plan, or
  task-creation handoff assigned to the role.
- **commander:** the monitor's project has actionable active-goal work or a
  handoff/review addressed to commander.

The check does not use the project-wide `pending` command because that command
also reports work assigned to other sessions and roles.

## Stop reporting

`handoff yielded <task-id>` is removed from the Codex Stop hook. Reporting a
yield while returning `decision: "block"` would claim that an executor stopped
even though Codex continues it. The existing Claude report-only hook is out of
scope.

## Failure handling

The check fails closed for an ATCT-scoped monitored session: if it cannot read
the local ATCT state, it blocks with a reason that names the check failure.
Sessions without ATCT scope remain unaffected.

## Verification

Tests cover no-work continuation, each role's matching unfinished work,
non-matching-role work, already-active Stop hooks, and check failures.
