# Goal handoffs implementation plan

## Scope

Add `goal_handoffs` with the same lifecycle and persistence shape as
`task_handoffs`, then expose request/receive/complete through the daemon RPC
and MCP layers. A handoff request is accepted only while the target goal has a
live claim; receiving and completing an existing handoff do not require the
claim to remain live.

## Design decisions

- Add migration `0013_goal_handoffs.sql` and the matching `schema.sql` table
  and index.
- Add sqlc queries and generated methods for get/list/target lookup,
  request, receive, and completion.
- Resolve receive-by-goal from `goal_id`: one pending request is received,
  no pending request is not found, and multiple pending requests are
  ambiguous. The explicit pending handoff is the transfer event; the goal
  claim gates only creation of a request, matching task handoff behavior.
- Keep task handoff behavior unchanged, including its errors and tool names.
- Add three goal RPC methods and three MCP tools, and update both exact tool
  count tests.

## Implementation and verification

1. Add failing store tests for the goal handoff lifecycle, claim gate,
   receive-by-goal resolution, and ambiguity.
2. Add schema/migration/queries, run `sqlc generate`, implement the store API,
   and run focused store tests.
3. Pause for commander review after the table and store functions are complete.
4. Add daemon RPC and MCP handlers, schemas, and integration tests.
5. Run `GOCACHE=/tmp/gc25 go test ./...`; report any pre-existing web asset
   failures separately. Do not touch the production daemon or database.
