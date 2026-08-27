# One watch per scope, and a commander grain

Goal 185. Two failures with one root: **nothing owns the set of running
`atct watch` processes.** Nobody can say how many exist, what each one is
scoped to, or whether the session that started one is still alive.

- A dead session's watch keeps running. Its claim gets inherited; its watch
  does not. Measured 2026-08-27: two project-wide `atct watch` processes, one
  belonging to a session that was already dead at the observing session's
  start. Killing the orphan dropped daemon connections from 34 to 30.
- `atct watch` has two grains, `-goal N` and everything. The commander can
  only pick "everything", and 74 of the 137 notifications it received in one
  day produced zero actions.

## Decisions

### 1. The reap lives in `atct watch`, not in the skill's prose

`skills/start/SKILL.md` already forbids a second Monitor:

> If this session already has an `atct watch` Monitor, keep it; do not attach
> a second one. Two Monitors emit the same answer twice.

That prose did not prevent the observed duplication, and it could not: it
scopes the check to "this session", while the residue belongs to another one.
Adding more prose — "run `pgrep`, then kill" — repeats the failure the goal
itself names: *役割から導けるものを覚えさせるな*. A procedure an agent must
remember to run is a procedure that gets skipped.

So `atct watch` reaps on startup, unconditionally, in code. The skill's job
shrinks to choosing the right flag.

### 2. Scope is `(project, goal)`, and `-project` does not create a new scope

    atct watch              scope = (project, "")
    atct watch -project     scope = (project, "")     <- same scope
    atct watch -goal 185    scope = (project, "185")

`-project` changes **which events are printed**, not **which events are
subscribed to**. Two project-wide watches on one project are a duplicate
regardless of their filter, because ATCT already allows only one commander per
project — and the project-wide watch is the commander's inbox.

This is what makes condition 4 reachable. The orphan measured on 2026-08-27
was a bare `atct watch`, and its process was alive; its *session* was dead.
Liveness of the watch process cannot detect that, and neither can liveness of
its parent pid — the observed orphan's parent was a live but unrelated Claude
process. Scope equality can: the arriving commander's watch owns
`(project, "")`, so the incumbent holder of that scope loses it.

### 3. The reap kills scope-equal watches and nothing else

    dead pid, any scope         remove the registration file (no signal)
    live pid, scope == mine     SIGTERM, wait for exit, remove the file
    live pid, scope != mine     leave completely alone

A running subcommander's `-goal N` therefore survives a commander's restart
(condition 3), and a second `atct watch -goal 185` replaces the first rather
than doubling it.

### 4. The `-project` classification is a table in its own file

Goal 184 owns *what flows*; this goal owns *how it is attached* and the
`-project` vessel. To keep both landable in either order, every rule about
which event names a scope admits lives in one new file, `cmd/atct/watch_scope.go`,
behind one function. Nothing in `watch.go` knows a rule; it asks. 184 edits the
table, not the plumbing.

The table is seeded here from goal 185's own measurements, because conditions
5, 7 and 9 are stated in terms of them. It is a starting classification, not
the final one.

| event | `-project` | why |
|---|---|---|
| `decision.approved`, `decision.rejected` | pass | 34/34 acted on; condition 7 forbids filtering these |
| `decision.answered`, human answer | pass | a human spoke |
| `decision.answered`, default applied | drop | 11 notifications, 0 actions — nobody answered |
| `handoff_reported` with no `task_id` | pass | 28/28 acted on: merge, reject, or re-issue |
| `handoff_reported` with `task_id` | drop | 63 notifications, 0 actions |
| `handoff_yielded` | drop | task grain |
| `goal.created` | pass | an unassigned goal is commander work |
| `wakeup` | pass | 2 actions; already deduplicated by content, so not a 30s drip |
| `detection.*` with `task_id` | drop | 0 actions; the task's own watch already carries it |
| `detection.*` with only `goal_id` | pass | goal grain: the commander is the one who repairs it |
| `detection.decision_answered_unapplied` | drop | 0 actions; whoever asked should poll |
| `wakeup.discrepancy`, `wakeup.evaluate_failed` | pass | tool health, not work |

`handoff_reported` is emitted for both grains under one name, so the name
alone is not enough. `task_id` is: `internal/store/task_handoff.go` sets it,
`internal/store/goal_handoff.go` leaves it zero.

An event name absent from the table **passes**, and a test asserts the table
covers every name `formatWatchDecision` renders. A new event should be loud
at test time and harmless at runtime; the reverse — silent suppression of
something nobody classified — is how a commander misses an approval.

### 5. `-goal` is untouched

No filtering is added to the `-goal` path. Conditions 6 and 7 both say so, and
goal 176 already owns a defect in that path's snapshot.

### 6. The wrapper chain is not refactored

`watchLoopWithEnsureAndProjectIDAndGoal` should be a struct. It is not becoming
one here: goal 184 edits the same file, and a signature-wide rename guarantees
a conflict for no functional gain. Scope is threaded through the existing
parameter lists. The cleanup belongs after 184 merges.

### 7. Release ordering is out of scope

`script/release.sh` says "then re-arm the watch" but never "stop the watch
first", which is why a v0.58.0 swap stalled: a live watch rebuilt the daemon
the moment it was stopped. That is a different failure (version swap) in a
different file, and none of this goal's ten conditions reach it.

What is in scope, because it is the same registry: `StopWithWatchWarning`
already warns that a watch will restart the daemon, but cannot say how many
watches or whose. With scoped registrations it can, and a warning that names
"3 watches: project-wide, goal 180, goal 181" is actionable where "a watch is
running" was not.

## Shape

`internal/daemonctl/` — registrations become JSON carrying pid, project, goal,
and start time instead of a bare pid. New: list, and reap. `RegisterWatch`'s
per-process file layout is kept; only the contents and the scope logic are new.

`cmd/atct/main.go` — `-project` as a boolean on `watch`, mutually exclusive
with `-goal`.

`cmd/atct/watch_scope.go` — new, the table and one predicate.

`cmd/atct/watch.go` — register with a scope and reap at startup; consult the
predicate before emitting.

`skills/start/SKILL.md` — the Monitor section stops describing a check the
binary now performs, and says which flag each role attaches.
