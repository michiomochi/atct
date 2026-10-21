---
name: start
description: Start working through the ATCT goals for this repository without stopping between tasks. Use when the human says to start, resume, or keep going, and at the beginning of a session in a repository that ATCT manages.
---

# Start

This skill turns an ATCT-managed repository into work. It is the entry point the
human reaches for when they want progress rather than a plan.

## First step: identify the session

Before entering the goal loop, call `atct_session_identify`. Pass the exact
`session_key` and `monitor_token` printed by SessionStart; do not substitute an
agent name or generate a different token. Only when no SessionStart key was
emitted, use this session's full `<project>-<unit>-<role>` agent name. Do not use
only a role such as `commander`: it can collide across projects and merge their
sessions into one row.

Pass the current directory as `cwd` in the same call. That is what binds the
session to its project, and a session with no project cannot be revoked later:
the request is refused because nothing says which project it belonged to.

A claim taken before the key was registered is not restored after a reconnect;
only a claim retaken after identification can return. If a new version has just
been installed and `atct_session_identify` is not yet in the tool list because
MCP has not reconnected, use the recovery section of the `atct:atct` skill once
the tools are available.

## Claim the project as commander

Call `atct_goal_list` with the current directory and take `data.project.id` from
its response. Then call `atct_project_claim` with `force` set to `true` and that
project ID. Finally call `atct_role` with `expected_role` set to `commander`.
Do all three before attaching a monitor or touching a Goal. The forced claim
replaces a live project claim, and the resulting project claim makes this
`/atct:start` session the commander even when it already holds a goal handoff.
Then invoke `atct:commander` before continuing.

## Claude Code: attach the Watch

After identifying the session, arm the watch as `atct:atct` `## Watch` says. The
default is the Monitor tool; that section also says when to switch to a
background Bash.

- `atct watch --monitor --token <monitor_token>` waits for the server-derived
  assignment. Do not pass a role, project, goal, or task selector.
- Always set `persistent: true`; otherwise `timeout_ms` defaults to `300000ms` (5
  minutes) and monitoring stops silently.
- Set `description` to `ATCT answer watch`.
- Plain `atct watch` is for human diagnostics; it is not the Claude action
  channel. Reconnect, keepalive, and ensure diagnostics are never agent actions
  and must not be forwarded to the agent.
- Keep one watch; do not arm a second.
- This step applies only in Claude Code. The MCP response attachment remains the
  shared foundation for both harnesses.

## Codex: launch the monitor before `/atct:start`

Start a new interactive monitored Codex session from a shell before invoking
`/atct:start`:

```bash
atct codex monitor -- <codex args>
```

The wrapper injects a monitor token and waits for the server-derived assignment.
`--role`, `--project`, `--goal`, `--task`, and `--scope` are not monitor options.

`/atct:start` only identifies the already-launched session and enters the
existing daemon/goal loop; it does not start or attach a Codex monitor. A normal
Codex process that is already running cannot be retrofitted or attached. To use
the monitor, preserve or hand off its uncommitted work, exit it normally, and
relaunch through the explicit entry point. `atct codex monitor start` is not a
monitor start subcommand.

Ordinary `codex` and `codex exec` remain unchanged. The known
`atct codex monitor exec ...` pass-through is non-interactive and is not a
monitored interactive session.

For an executor, the commander first records its goal handoff, then starts the
executor process through `atct codex monitor -- <codex args>`. Placing that
command in a workspace is the terminal multiplexer's concern. The executor then
performs `atct_session_identify` → `atct_goal_handoff_receive` with its `goal_id`,
using the exact `session_key` and optional `handoff_id` and `monitor_token` from
SessionStart → `atct_role`. Starting Codex directly bypasses the monitor wrapper
and is forbidden for a monitored executor.

### Liveness is a recheck, not authority

A one-minute liveness line means that the last scoped reconciliation should be
rechecked. It does not approve a human decision, authorize a scope change,
create work, or authorize a commit.

- An executor has its plan accepted first. Only then does it implement. It may
  start sub-agents for the work; they call no ATCT tool.
- An executor keeps an open human decision parked and stays within its goal. It
  implements and tests, then requests the goal review; it does not cross the
  decision because a liveness prompt arrived.

Transient watch or daemon failures recover in the watch loop with bounded
backoff and reconnect. A prompt is not durable work and does not replace the
normal handoff or review sequence.

## Ensure the daemon is running

After completing the applicable harness-specific session setup above, run `atct
daemon start` before entering the goal loop. It reuses a healthy daemon and
starts one when needed.

Only after these steps, continue with the goal loop below.

## Running this makes you the commander

Invoking this skill is not only a request to begin. It assigns you a role: **for
this repository, you own what ATCT says.** Every claim, every `done`, every
parked decision is yours to keep accurate, and nobody else will do it for you.

That obligation transfers with the work. Delegate every goal to an executor,
then receive its goal review and accept it with the matching completion tool. A
delegate reporting success is not a substitute for the recorded review.

The failure this prevents is specific and has happened: an orchestrator
delegated six pieces of work, all six landed, and every one of them still read
`todo` on the dashboard because the orchestrator moved on to the next request.
The human found it before the agent did.

## The loop

The loop coordinates delegated work. An executor receives each goal with
`atct_goal_handoff_receive` before starting it.

Run this until nothing is left, not until the next natural pause.

1. **Look.** Call `atct_goal_list` with the current directory as `cwd`. This
   selects the project for this session. If a human assigns a different
   project, pass that project's `root_path` as `cwd` instead. The MCP tool
   definition is unchanged; do not add a CLI-style project argument. Once
   selected, keep the project fixed for the entire session. Working on another
   project's goals can conflict with the run assigned to that project. It
   returns the active goals, the tasks under them, and any answers waiting for
   you.

2. **Collect what you were told.** For every decision in `orphaned_decisions` or
   `answered_decisions`, call `atct_decision_poll` with its `decision_id` before
   deciding what to do next. An answer that arrived while you were away changes
   what the right next step is, and polling is how the human learns their answer
   landed.

3. **Fill the gaps.** A goal nobody holds is a goal nobody has started. Call
   `atct_goal_handoff_request`, then wake an executor for it. Do not create
   tasks for it; the executor does not create any either.

4. **Review it.** When the executor requests the goal review, receive it and
   accept or reject it. A goal left open after the work landed makes the
   dashboard lie, and the human plans around that dashboard.

5. **Go back to 1.** Do not report and wait. When this goal has been handed to
   an executor, move to another active goal.

## When to come back to the human

Four times, and no others:

- You are about to do something that cannot be undone or is destructive — see the
  `atct` skill for what counts
- You need something only the human can do (a permission, a credential, an
  external account, approval of a cost not yet approved) — `atct_decision_ask`
  (an executor appends a `review_requested` entry to its goal handoff instead;
  see `atct:atct` `### An executor's decision request`)
- The work would change an outcome the human asked for or approved —
  `atct_decision_ask` with `wait_ms=0` (an executor: the entry above), then
  keep going on something that does not depend on it
- A goal is met — `atct_goal_review_request` for human approval, then
  `atct_goal_review_complete` after approval and merge

A reversible, non-destructive choice of means inside the approved outcome is none
of these: decide it and record it in the completion report.

Finishing a piece of work inside a goal is none of these.

## What this skill is not

It is not permission to skip the work. Writing six progress notes and claiming all six
does not advance a goal; the commit does. Nor is it permission to decide what
the human already asked to decide — an open decision stays open until they
answer it or you withdraw it.
