# Role API Authorization

## Outcome

The daemon authorizes every state-changing RPC from one role/scope policy before
calling the store. Skills remain guidance; the daemon is the enforcement point.

## Policy

| Role | Write scope |
|---|---|
| executor | received Task: receipt and task-review request only |
| subcommander | received Goal: spec/plan, task creation/delegation, task review |
| commander | claimed Project: project claims, goal delegation/review/completion, publication and cleanup records |

Reads require the same project/goal/task containment but do not grant writes.
Every write additionally retains its existing claim or handoff validation.

## Design

Add a daemon authorization policy keyed by RPC method. It resolves the caller's
current role once, resolves the target scope once, and rejects a method outside
the matching role/scope before invoking store code. Unknown state-changing
methods are denied. Development mode is recorded on `agent_sessions`; it allows
only subcommander → executor and commander → subcommander within their current
scope, never commander → executor.
