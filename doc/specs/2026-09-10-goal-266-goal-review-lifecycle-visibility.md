# Goal 266: Goal-review lifecycle visibility

## Outcome

The MCP `atct_goal_get` read model and commander monitor make the lifecycle of
the human `goal_review` decision observable as `open`, `answered`, or
`applied`. In the approved (`applied`) state, they explicitly state that the
commander must run `goal.review.complete`; the goal remains active until that
commander-only operation succeeds.

This covers the visibility gap represented by Goal 258 decision #740 without
changing Goal 258's lifecycle or granting a subcommander a finalization path.

## Evidence and current gap

- `ApproveGoalReview` deliberately writes `status = applied` and leaves the
  goal active. `FinalizeGoalReview` is separately authorized in the daemon by
  `authorizeCommander`.
- `atct_goal_get` calls daemon `goal.get` through a raw MCP envelope. The
  daemon handler returns only `goal` and `tasks`; this is the read model used
  by Goal 258. The HTTP goal detail has a separate handler and response type,
  so it is not a shared projection path.
- Watch reconciliation emits an approved applied decision only for
  `goal_approval`. A restarted commander monitor cannot reconstruct that an
  applied `goal_review` needs finalization; the live generic
  `decision.approved` message also does not say what must happen next.
- A rejected `goal_review` is `answered`, and must stay distinct from an
  approved `applied` decision. Its next operation is a commander re-handoff,
  not finalization.

## Design

### MCP read model

Add one `goal_review` lifecycle projection alongside `goal` and `tasks` in the
daemon `goal.get` response returned by `atct_goal_get`. It is derived from the
latest taskless decision whose kind is `goal_review`, using the existing
persisted decision status; no schema change or new lifecycle state is
introduced. The projection labels its follow-up explicitly as commander work;
it does not vary by caller.

The projection contains the decision identity, status, answer metadata, and a
role-neutral lifecycle condition. When its status is `applied`, its answer is
`approve`, and the goal is still active, it additionally exposes:

```json
{
  "next_commander_action": "goal.review.complete"
}
```

The field describes work for the commander. It is not an authorization token
and does not alter the existing daemon authorization that rejects
subcommander finalization. A subcommander may read it, but cannot use it to
finalize.

The three required observable states are:

| Decision status | Meaning | Follow-up |
| --- | --- | --- |
| `open` | Human review is pending. | Await the human answer. |
| `answered` | Human rejected the review. | Commander reissues the goal handoff if work resumes. |
| `applied` with `approve` and active goal | Human approved; the goal is not done yet. | Commander calls `goal.review.complete`. |

`withdrawn`, non-goal-review decisions, older goal-review decisions, done
goals, and an `applied` decision without `approve` do not advertise a
finalization action.

The HTTP goal detail is intentionally excluded: it does not share this response
type, and adding a second independent projection would duplicate lifecycle
logic. It can reuse the established MCP/daemon projection in a separate goal
if a human-facing display is needed.

### Commander monitor

Extend project-scoped watch reconciliation to project an active goal's applied,
approved `goal_review` as a dedicated lifecycle notification. Its text names
the goal and says that the commander must finalize it with
`goal.review.complete`. This is generated from reconciliation, so it survives
a missed live event or monitor restart.

Goal- and task-scoped monitors do not receive this commander-actionable
projection. Existing generic decision events and scope rules remain intact;
the dedicated notification does not call any MCP tool or mutate state.

### Boundaries

- Preserve Goal 258's existing decision and finalization transitions.
- Do not change Goal 260 workflow behavior or Goal 265 stale-action scope.
- Do not add migrations, configuration, skills, roles, or new authorization.
- Keep `goal.review.complete` commander-only and test the subcommander denial.

## Acceptance criteria

1. `atct_goal_get` distinguishes the latest taskless `goal_review` as
   `open`, rejected `answered`, or approved `applied`.
2. Only an active goal with an applied `approve` review exposes
   `next_commander_action: "goal.review.complete"`.
3. Project-scoped reconciliation emits a dedicated, actionable finalization
   notice for that state; goal/task scopes do not receive it.
4. Reconciliation after restart produces the same notice as the live path.
5. A subcommander still receives a commander-authorization denial if it calls
   `goal.review.complete`.
