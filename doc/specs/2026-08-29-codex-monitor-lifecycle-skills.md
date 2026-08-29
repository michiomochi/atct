# Goal 206: Codex monitor lifecycle in the ATCT skills

Date: 2026-08-29

## Outcome

Teach the repository-owned `skills/start` and `skills/stop` skills the explicit
Codex monitor lifecycle already implemented by Goal 204. A Codex user can tell
when monitoring must be established before process launch, how to stop the
project's monitor supervisors, and how to perform a safe restart without
mistaking a normal Codex process for a monitored one.

## Decisions

### Codex monitoring is an opt-in launch path

The user starts an interactive monitored Codex session from a shell with:

```bash
atct codex monitor -- <codex interactive arguments>
```

Once that process is running, `/atct:start` identifies the session and enters
the ATCT goal loop; it does not attach or start a Codex monitor. Ordinary
`codex` and `codex exec` remain unchanged. `atct codex monitor exec ...` is the
existing non-interactive pass-through and is not a monitored interactive
session.

### A running Codex process cannot be retrofitted

The skills must state this as a factual boundary. Running `atct codex monitor`
from another shell creates a new supervised Codex launch; it does not attach to
the already-running normal Codex process or its thread. A user who needs
monitoring must preserve or hand off uncommitted work, exit the unmonitored
process normally, and launch a new session through the explicit entry point.
The skills must not suggest an in-place attach, PID handoff, shell shim, or
unsupported `start` subcommand.

### Stop and restart are separate operations

The implemented stop command is:

```bash
atct codex monitor stop
```

It targets every live Codex monitor supervisor registered for the current exact
project path. It does not stop the ATCT daemon. There is no `restart` or
`exit` monitor subcommand. To restart, run the project-scoped stop command from
a separate shell when the current terminal is the monitored TUI, wait for it to
return, and then launch a new session with `atct codex monitor -- <args>`. A
literal Codex argument named `stop` goes after `--` so it is not interpreted as
the monitor stop action.

### Claude behavior remains unchanged

The existing Claude branch continues to attach the role-appropriate
`atct watch` Monitor in `skills/start` and stop it with `TaskStop` in
`skills/stop`. The Codex branch must not invoke Claude's `Monitor` or
`TaskStop` features.

## Scope

Modify only:

- `skills/start/SKILL.md`
- `skills/stop/SKILL.md`

Add the design/implementation records under `doc/specs/` and `doc/plans/`, and
retain the RED pressure-scenario evidence under `doc/investigations/`. Do not
modify Goal 204's production monitor implementation, its existing spec/plan,
or global/dotfile configuration. The README already contains the basic
explicit command and pass-through note and does not need duplication in this
goal.

## Acceptance criteria

1. `skills/start/SKILL.md` keeps the Claude Monitor procedure and adds a clear
   Codex pre-launch branch with the explicit command, no-retrofit boundary,
   normal-process handoff/exit guidance, and the `codex exec` pass-through
   boundary.
2. `skills/stop/SKILL.md` keeps Claude `TaskStop` behavior and adds the Codex
   stop command, exact-project/project-wide scope, separate-daemon warning,
   safe restart sequence, no `start`/`restart`/`exit` subcommand claim, and the
   `--` literal-argument rule.
3. The neutral pressure scenario that chose `atct codex monitor start` without
   the target skills is corrected by the updated skills: the agent states the
   explicit launch form and refuses retrofit/guessed start behavior.
4. Existing focused Codex parser/lifecycle tests and the repository's final Go
   verification pass; no production monitor file changes are introduced.
5. Skill word counts are measured before and after, and the modified skills
   remain concise enough to be loaded as procedural guidance.

## Verification

The consuming-agent pressure scenario is the behavioral test for the skill
prose. Source tests are regression checks for the already-implemented command
contract, not source-text tests for the skill. Use the focused parser tests
while implementing and run the full Go test/build/diff checks from the
subcommander worktree before completion.
