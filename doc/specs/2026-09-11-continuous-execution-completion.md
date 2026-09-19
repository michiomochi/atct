# Continuous execution completion

## Goal

An active delegated handoff must not become silent when its monitor stops, and a
subcommander must not stop while a child task is awaiting review.

## Rules

- A monitor-loss detection applies only after a monitor has reported the same
  live handoff scope. It notifies the parent role; it never restarts Codex.
- An executor monitor loss is delivered to the goal-scoped subcommander. A
  subcommander monitor loss is delivered to the project-scoped commander.
- A subcommander stop check blocks only for a child task handoff that is
  awaiting its review, not for an executor still implementing.
- `atct` is the sole human-decision rule: human judgment is requested only
  immediately before an irreversible or destructive operation.

## Non-goals

- No new escalation table or monitor process manager.
- No automatic recovery, restart, or duplicate session creation.
