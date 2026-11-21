# Goal 248: Commander Monitor Local Mutation Suppression

## Purpose

An ATCT mutation performed by the same commander session can produce two
signals: the synchronous MCP response shown to that commander and the later
watch-derived monitor notification. The second signal is redundant only when
the monitor can prove that its own Codex session has already received the
successful synchronous response. It additionally suppresses the matching
subcommander receipt only when that same commander recorded and launched the
exact goal handoff. This specification retains every remote-session,
unacknowledged, independently-created, and unrelated notification.

## Current path

`cmd/atct/watch.go` treats an SSE frame as a wake-up and renders canonical
reconciliation state. `emitWatchDecisionWithStateAndSink` writes each rendered
action line and then invokes the optional sink. For a Codex monitor,
`runCodexMonitorWatchScoped` supplies `codexMonitorBridge.LineSinkWithContext`;
the bridge recognizes actionable ATCT lines and starts a Codex turn.

The monitor bridge is deliberately separate from the MCP server. The
supervisor launches the App Server and remote Codex TUI in
`cmd/atct/codex_monitor_supervisor.go`, while `cmd/atct-mcp/main.go` runs the
ATCT MCP stdio server that forwards calls through `internal/mcpshim`. Neither
path currently carries a successful MCP response back to the monitor, so line
delivery cannot distinguish a local mutation from another actor's mutation.

## Decision

Add a monitor-local acknowledgement channel, owned by each commander monitor.

1. The commander monitor supervisor creates an authenticated Unix-domain
   acknowledgement listener and a fresh opaque capability before launching its
   remote TUI. It passes the listener address and capability only in the TUI
   process environment, so the TUI's `atct-mcp` child inherits them. The
   listener and capability live only for this monitor process and are removed
   during the existing monitor cleanup.
2. `atct-mcp` wraps its stdio MCP transport when those two environment values
   are present. The wrapper records incoming `tools/call` requests that invoke
   an ATCT mutation. It sends an acknowledgement only after the corresponding
   successful MCP response has been written to stdout. A daemon response,
   handler return, failed call, malformed response, or failed stdout write is
   not an acknowledgement.
3. The acknowledgement identifies the mutation with a canonical suppression
   key derived from the tool name plus authoritative IDs decoded from its
   serialized successful response (goal, task, handoff, or decision as
   applicable). The monitor listener
   verifies the capability, accepts only well-formed keys, and adds each key to
   that monitor's in-memory acknowledgement set.
4. Immediately before the bridge enqueues a watch-rendered action line, the
   monitor derives the same key. It consumes and suppresses the line only when
   that monitor's acknowledgement set already contains the key. A missing or
   nonmatching key is delivered normally. Suppression is one-shot; a later
   independently generated notification with the same text is not hidden.

The effective ordering rule is strict: notification first means notification
delivered; successful local MCP response write and acknowledgement first means
the matching notification suppressed. The monitor never waits for an
acknowledgement, delays a remote event, or guesses origin from a session ID.

## Delegated goal-handoff lifecycle correlation

The local-response acknowledgement for `atct_goal_handoff_request` covers the
commander's own `goal.handoff.request` line. It does not by itself authorize
suppressing a later `goal.handoff.receive`: another session can receive the
same handoff, and a goal ID alone is not an origin proof.

For the extra delegated-lifecycle case, the commander monitor keeps a second,
ephemeral correlation record keyed by `(goal_id, handoff_id)`. It reaches the
`launch-confirmed` state only in this order:

1. the commander's successful response-write acknowledgement for
   `atct_goal_handoff_request` registers the exact pair as a candidate;
2. the same commander TUI launches `atct codex monitor --role subcommander`
   with both `--goal <goal_id>` and a new required `--handoff <handoff_id>`;
   the child inherits the parent monitor's capability and sends a
   `subcommander-launched` record only after its own monitor setup succeeds,
   then removes the parent acknowledgement environment before starting its own
   TUI/App Server;
3. the parent listener authenticates that record and accepts it only when its
   exact pair is already a candidate. It then arms one
   `goal.handoff.receive` suppression for that pair.

The child launch signal neither creates a candidate nor refreshes an expired
one. A candidate and an armed receipt expire after five minutes; the latter
also disappears when consumed. A request ACK without a launch, a launch
without a prior local request ACK, a stale candidate, a different
goal/handoff pair, a failed child setup, a direct remote receipt, or a receipt
that arrives before the launch confirmation leaves the `goal handoff received`
line deliverable. Both candidate and armed state are bounded, one-shot,
monitor-local memory and are cleared on monitor cleanup.

This is a deliberate extension of the closed matrix, not a generic
remote-session suppression rule. It suppresses only the commander's own
`goal.handoff.request` response-correlated line and one exact,
launch-confirmed `goal.handoff.receive` line. Review, completion, rejection,
independent goal handoffs, task/plan handoffs, and every nonmatching lifecycle
event remain on the normal delivery path.

## Canonical suppression-key matrix

The transport and sink share one closed mapping. A key is the exact tuple
`(action_class, handoff_id, target_id)`: `target_id` is the task ID for task
handoffs and the goal ID for goal/plan handoffs. A key never consists of an ID
alone. The response must contain every listed ID; otherwise the transport emits
no acknowledgement. The sink parses only the corresponding formatted action
line and requires all tuple components to match.

| MCP tool(s) | Exact watch action class | Authoritative response IDs |
| --- | --- | --- |
| `atct_handoff_request`, `atct_task_handoff_request` | `task.handoff.request` | task ID and handoff ID |
| `atct_handoff_receive`, `atct_task_handoff_receive` | `task.handoff.receive` | task ID and handoff ID |
| `atct_handoff_complete`, `atct_task_handoff_complete` | `task.handoff.complete` | task ID and handoff ID |
| `atct_task_handoff_review_request` | `task.handoff.review.request` | task ID and handoff ID |
| `atct_task_handoff_review_receive` | `task.handoff.review.receive` | task ID and handoff ID |
| `atct_task_handoff_review_reject` | `task.handoff.review.reject` | task ID and handoff ID |
| `atct_handoff_report_amend` | `handoff_reported.task` | task ID and handoff ID |
| `atct_goal_handoff_request` | `goal.handoff.request` | goal ID and handoff ID |
| `atct_goal_handoff_receive` | `goal.handoff.receive` | goal ID and handoff ID |
| `atct_goal_handoff_complete` | `goal.handoff.complete` | goal ID and handoff ID |
| `atct_goal_handoff_review_request` | `goal.handoff.review.request` | goal ID and handoff ID |
| `atct_goal_handoff_review_receive` | `goal.handoff.review.receive` | goal ID and handoff ID |
| `atct_goal_handoff_review_reject` | `goal.handoff.review.reject` | goal ID and handoff ID |
| `atct_goal_handoff_report_amend` | `handoff_reported.goal` | goal ID and handoff ID |
| `atct_plan_handoff_review_request` | `plan.handoff.review.request` | goal ID and handoff ID |
| `atct_plan_handoff_review_receive` | `plan.handoff.review.receive` | goal ID and handoff ID |
| `atct_plan_handoff_review_reject` | `plan.handoff.review.reject` | goal ID and handoff ID |

One additional lifecycle-only mapping is permitted after the three-step
correlation above: parent candidate plus authenticated exact child launch
`(goal_id, handoff_id)` -> `goal.handoff.receive` with those same IDs. It has
no MCP-response mapping of its own and must not be emitted by the transport.

Every other mutating MCP tool is deliberately unsupported and emits no key:
`atct_project_claim`, `atct_project_release`, `atct_goal_claim`,
`atct_goal_release`, `atct_goal_update_content`,
`atct_goal_update_request_report`, `atct_task_create`, `atct_task_claim`,
`atct_task_release`, `atct_task_update`, `atct_task_update_content`,
`atct_plan_handoff_complete`, `atct_decision_ask`, `atct_decision_poll`,
`atct_decision_withdraw`, `atct_goal_complete`, `atct_goal_review_request`,
`atct_goal_review_complete`, and `atct_goal_set_derived_from`. They either
produce no bridge action line, can produce a compound/derived state change,
or have no one-to-one response-to-line identity. Read-only tools
(`atct_session_identify`, `atct_role`, `atct_goal_list`, `atct_goal_get`, and
`atct_goal_sessions`) are not candidates.

In particular, `atct_plan_handoff_complete` is excluded because the current
bridge does not recognize `plan.handoff.complete`; decision and goal-review
tools are excluded because their reconciliation output is a state projection,
not a unique response-correlated action line. Future tools or bridge action
classes must first add one matrix row and its tests; the fallback is delivery,
never suppression.

## Scope and compatibility

- Enable the response-acknowledgement channel only for `--role commander`
  monitors. A subcommander monitor may receive a parent capability only while
  being launched from that commander's TUI with explicit `--goal` and
  `--handoff`; it may use it solely once to report successful launch, and
  removes it before launching its own TUI/App Server. Existing plain,
  subcommander, executor, and unmonitored Codex/MCP launches run unchanged
  because no capability environment is present.
- Limit acknowledged mutations to operations whose response can deterministically
  map to an existing watch action line. Read-only calls and mutations with no
  corresponding monitor line do not enter the set.
- Preserve canonical reconciliation, its current per-watch delivery
  deduplication, action-line formatting, scope filtering, wakeups, detections,
  handoff/plan semantics, and the daemon's event publication.
- Treat a disabled, unavailable, malformed, or unauthorized acknowledgement
  channel as fail-open: the MCP response is still returned and the watch line
  is delivered. Suppression is an optional local UX improvement, never a
  correctness dependency.
- Do not persist acknowledgements, add daemon/store tables or RPC methods, or
  expose the capability in CLI output, monitor registry JSON, logs, or MCP
  structured results.

## Rejected alternatives

### Suppress all events from the local agent session

The watch event path does not consistently carry a caller session for every
notification, and a session-origin rule would also hide events the commander
has not yet observed. It violates the required response-before-suppression
ordering.

### Mark a mutation successful inside its MCP handler

The handler has a daemon response before the MCP transport has written that
response to Codex. A write failure or a notification racing ahead would then
silence an event without the commander having received the result.

### Store acknowledgement state in the daemon

Daemon-scoped state would couple separate commander monitors, outlive the
session that observed the response, and turn best-effort notification delivery
into a persistent protocol. The acknowledgement belongs to one monitor
process, so it remains in memory there.

## Acceptance criteria

1. A commander monitor suppresses exactly one matching watch action line after
   its own successful MCP mutation response has been written and acknowledged.
2. A matching line observed before that acknowledgement is delivered; it is not
   retroactively removed or delayed.
3. A mutation from another Codex/MCP session, a failed mutation, an MCP stdout
   write failure, an invalid capability, and an unavailable acknowledgement
   listener all leave the line deliverable.
4. Plain and non-commander monitored launches do not set acknowledgement
   environment variables and preserve current behavior.
5. After a commander's acknowledged `atct_goal_handoff_request`, only an
   authenticated successful subcommander launch carrying the identical goal
   and handoff IDs arms one `goal.handoff.receive` suppression. Every failed,
   stale, mismatched, direct, remote, duplicate, or pre-launch receipt is
   delivered.
6. Focused monitor, watch, MCP-shim, and supervisor tests prove every matrix
   row and every explicit exclusion above; existing `go test ./cmd/atct -count=1` and `go test ./cmd/atct-mcp -count=1`
   remain green.
