---
name: start
description: Start working through the ATCT goals for this repository without stopping between tasks. Use when the human says to start, resume, or keep going, and at the beginning of a session in a repository that ATCT manages.
---

# Start

This skill turns an ATCT-managed repository into work. It is the entry point the
human reaches for when they want progress rather than a plan.

## First step: identify the session

Before entering the goal loop, call `atct_session_identify` with `session_key`
set to this pane's agent name in the `<project>-<unit>-<role>` form. Use the
full agent name rather than only the role, such as `commander`: a role-only key
can collide across projects and merge their sessions into one row.

A claim taken before the key was registered is not restored after a reconnect;
only a claim retaken after identification can return. If a new version has just
been installed and `atct_session_identify` is not yet in the tool list because
MCP has not reconnected, use the recovery section in `skills/atct/SKILL.md` once
the tools are available.

## Attach the Claude Code Monitor

After identifying the session, attach a role-appropriate `atct watch` Monitor
and keep its id.

- Commander: `atct watch -project`; subcommander: `atct watch -goal <goal_id>`.
- Keep the session's Monitor; do not attach a second. Two Monitors in one
  session emit the same answer twice.
- `atct watch` stops an existing watch for the same scope at startup.
- Set `persistent: true`; otherwise `timeout_ms` defaults to `300000ms` (5
  minutes) and monitoring stops silently.
- Set `description` for the scope: `ATCT answer watch project` or
  `ATCT answer watch goal <goal_id>`, substituting the number.
- This step applies only in Claude Code. Codex has no Monitor, so a Codex reader
  must skip it and must not try to call or attach Monitor. The MCP response
  attachment remains the shared foundation for both harnesses.

## Ensure the daemon is running

After attaching the Monitor, run `atct daemon start` before entering the goal
loop. It reuses a healthy daemon and starts one when needed.

Only after these steps, continue with the goal loop below.

## Running this makes you the commander

Invoking this skill is not only a request to begin. It assigns you a role: **for
this repository, you own what ATCT says.** Every claim, every `done`, every
parked decision is yours to keep accurate, and nobody else will do it for you.

That obligation transfers with the work. If you delegate a task to another
agent, the delegate calls `atct_task_update` with `done` as soon as it finishes.
If that call cannot complete, the delegator closes the task as a fallback. A
delegate reporting success is not a substitute for a successful task update.

The failure this prevents is specific and has happened: an orchestrator
delegated six tasks, all six landed, and every one of them still read `todo` on
the dashboard because the orchestrator moved on to the next request. The human
found it before the agent did.

## The loop

The following loop is for self-directed work: find and take a task yourself.
A delegated worker receives the task with `atct_handoff_receive` and owns the
delegated task; the claim step applies only to self-directed work.

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

3. **Fill the gaps.** A goal with no tasks is a goal nobody has broken down yet.
   Call `atct_task_declare` with the tasks you intend to do. Do not ask whether
   the breakdown is right: propose it by declaring it, and let the human correct
   it from the dashboard.

4. **Take one (self-directed work only).** Call `atct_task_claim`. If the claim
   fails, another run owns it; take a different one. Then do the work and carry
   it to a commit.

5. **Close it.** Call `atct_task_update` with `done` **as soon as the work is
   finished**, before you claim anything else. A task left open after the work
   landed makes the dashboard lie, and the human plans around that dashboard.

6. **Go back to 1.** Do not report and wait. When this goal has no unclaimed
   tasks left, move to another active goal.

## When to come back to the human

Three times, and no others:

- A choice would change the shape of the work and you cannot settle it from the
  code — `atct_decision_ask` with `wait_ms=0`, then keep going on something that
  does not depend on it
- You are about to do something that cannot be undone — see the `atct` skill for
  what counts
- A goal is met — `atct_goal_complete` for approval

Finishing a task is none of these.

## What this skill is not

It is not permission to skip the work. Declaring six tasks and claiming all six
does not advance a goal; the commit does. Nor is it permission to decide what
the human already asked to decide — an open decision stays open until they
answer it or you withdraw it.
