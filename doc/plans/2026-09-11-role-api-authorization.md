# Role API Authorization Implementation Plan

**Goal:** Enforce ATCT role boundaries at the daemon RPC boundary.

1. Inventory all state-changing RPC methods and classify role plus target scope.
2. Add failing daemon tests for executor/subcommander/commander denied and allowed calls.
3. Add a shared authorization function in `internal/daemon/handler.go`; preserve existing store claim/handoff checks.
4. Expose denied errors consistently through MCP and run `go test ./...`.
