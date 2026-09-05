# Main baseline test repair

## Problem

The accepted Goal247 migration-lineage change merges cleanly, but `go test ./...`
also exposes pre-existing failures in the main branch. They are unrelated to the
five Goal247 paths and prevent a verified integration.

## Scope

Repair stale test fixtures and expectations so they reflect the existing
handoff, completion-review, MCP wrapper, and tool-registration contracts. Build
the generated web assets before the full Go suite; the assets are intentionally
ignored and are not committed.

## Non-goals

- No change to the runtime contracts under test.
- No changes to shared DB, daemon process, launcher, or ATCT records.
- No change to the user's root worktree modification.

## Acceptance

`go test ./...` passes from the merged integration worktree after `pnpm --dir
web build`, and the Goal247 migration tests remain green.
