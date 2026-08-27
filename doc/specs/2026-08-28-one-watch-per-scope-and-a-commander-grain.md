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

**Superseded, 2026-08-28.** Goal 184 landed this table first, in `b5be398`, as
`watchScopeFilter` in the same file — same grains, same `task_id` split, same
"unknown names are delivered" rule, reached independently. The table above is
kept as the reasoning that produced the classification, not as the code: 184's
is what ships. What 184 did *not* add is the flag. It gives the commander filter
to a watch with no `-goal`, so there is no longer any way to see every event.
This goal's remaining work is therefore only the vessel — `-project` selecting
184's filter explicitly, and bare `atct watch` returning to pass-through. See
the addendum, and decision 481 for who merges what.

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

## Addendum: what the day's three swaps changed (2026-08-28)

Three version swaps happened while this goal was being built, and each produced
a measurement that bears on the design. None of them changed a decision above;
two added one, and one was rejected.

### The split-subject failure is already answered by decision 1

    01:57:36  the commander pkilled every watch
    01:57:36  atct watch -goal 183 started      <- each session re-armed, as it
    01:57:43  atct watch -goal 181 started         had been told to
    01:57:36  the 0.58.1 daemon was rebuilt

"Stop, then re-arm" fails when the stopper and the starter are different
parties: the stop is undone before the swap completes. That is not a new
constraint on this design — it is the reason decision 1 puts the reap inside
`atct watch`. The arriving watch *is* the stopper, in one process, in the order
register → reap → connect. There is no window between the stop and the start,
so nobody has to be told to re-arm.

### A watch cannot report that no watch exists

On the third swap the commander told every subcommander it was safe to re-arm
and forgot its own. For roughly forty minutes no project-wide watch existed;
four goals were approved and left undelegated, and a human noticed before any
agent did.

Three repairs were considered.

- **Record what was stopped, and detect registrations nobody re-armed.**
  Rejected. A successful reap *removes* the registration, so there is nothing
  left to detect. Keeping a tombstone only moves the problem: whatever reads the
  tombstone has to be running, and the thing that is not running is the watch.
- **Put it in the `wakeup` line.** Impossible, and worth stating plainly because
  it is the shape of the whole incident: `wakeup` arrives over the watch. A
  channel cannot announce its own absence.
- **Make the roster visible at the moment someone attaches.** Taken. Every
  watch passes through `runWatch`, so that is the one place where the question
  can be asked at all.

The roster line is not a fix for absence and is not presented as one. It prints
what is listening on this project and lets the reader draw the conclusion:

    atct watch: 2 watches on this project: project-wide, goal 185
    atct watch: 1 watch on this project: goal 185 (+11 of unknown project)

The agent that forgot sees nothing, because it ran nothing. The *next* agent to
attach sees a roster with no project-wide entry — and on the day in question two
subcommanders re-armed after the commander did. A subcommander may not speak
upward, so what it does with that is `atct_decision_ask` to the human. The point
is only that the information now exists somewhere a reader can reach it.

No advisory text is attached to the line. "No project-wide watch" is not the
concern of an agent attaching `-goal N`, and a warning that fires on every
attach is noise within a day. The line states a fact.

Legacy registrations are counted separately rather than dropped, because a count
that hides eleven live listeners is as wrong as one that claims them.

### Detecting absence belongs to another goal

It needs a transport that does not pass through the watch. The two candidates
are the web dashboard — where the human was working when they noticed — and the
session-start line, which reaches an agent with no watch attached. Both are
other subsystems, and neither is reachable from the ten conditions here.

### Removing `Ensure` from `atct watch` belongs with goal 189

The proposal is that a command which only observes should not be able to start a
daemon. Two symptoms were offered for it: a watch older than the daemon fails to
connect while reporting only "connection unavailable", and a watch older than an
absent daemon rebuilds the old one.

Both are version-skew symptoms, and goal 189 is version skew. This goal is watch
multiplicity and grain. Three further reasons to keep them apart:

- Nothing here depends on it. The reap already tolerates an unreachable daemon by
  skipping (decision 5), so removing `Ensure` neither helps nor blocks it.
- It changes who guarantees a daemon exists, which reaches every command that
  ensures — a blast radius none of the ten conditions ask for.
- It interacts with the roster line in a way worth stating: with `Ensure` gone,
  "no daemon and no watch" can persist quietly, and the roster line cannot
  report it either, because an unresolvable project prints nothing. The roster
  line is not a substitute for `Ensure`, and reading it as one would be the
  mistake.

### A watch killed by a signal exits silently with status 0

Observed twice: a watch run in a pane kept retrying for 45 seconds after the
daemon went away, printing one line; the same binary under a Monitor ended with
no output and status 0. This was recorded as an unexplained difference between
two runs of one binary. It is not a difference in the binary. `runWatch` builds
its context with `signal.NotifyContext`, so SIGTERM cancels the context and the
loop returns nil — a clean, silent exit. The pane instance was never signalled;
the Monitor instance was caught by the pkill.

That is why goal 167 matters and why this goal does not reach it: a signalled
watch is indistinguishable from a quiet one, and the reap stops live duplicates
rather than restarting dead watches.

### A watch cannot notice that its session lost its role

A fourth trigger for a silent role drop was measured on 2026-08-28: the goal
handoff was still open, the session row was still in the database under the same
id, and the daemon had not restarted. What was lost was the mapping between the
transport and the session key. The cause is unmeasured and is not claimed here.

Asked whether the watch's grain could carry a signal for it: no, and the reason
is structural rather than a matter of which events are selected.

A role is derived from a claim held by an **MCP session**. `atct watch` is a
**CLI process** with no session key at all — it names itself by `(project, goal)`
taken from flags, which is the same reason goal 184 gave for classifying by
scope instead of by role. So the watch cannot report whose role dropped, because
it does not know which session it belongs to. Filtering cannot supply an
identity that was never in the channel.

The two lifetimes hang off one session and neither can observe the other. Both
directions were measured here in one afternoon:

- The role dropped to `executor` while the watch ran and said nothing. The
  failure surfaced as `atct_handoff_request` returning "caller does not hold an
  open received handoff for goal: 185" — a message about the handoff, while the
  handoff was open. `atct_session_identify` with the same key restored it.
- The watch exited with status 0 and printed nothing while the role was intact.

Each failure is silent in the other's channel, so neither can be the detector
for the other. Correlating them needs the watch registration to carry a session
key, which means teaching a CLI process which MCP session it serves. That is a
change to what a watch *is*, not to its grain, and it belongs to whichever goal
takes the role-drop triggers. If it is ever done, the roster line from this goal
is where it would surface: naming who is listening rather than only at what
grain.
