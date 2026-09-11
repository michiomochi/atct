# Development Escalation Implementation Plan

**Goal:** Add audited, scope-limited parent-role recovery for development sessions.

## Tasks

1. Add `agent_sessions.development_mode`, its store API, and lifecycle tests; allow only subcommander → executor and commander → subcommander overrides.
2. Expose activation through daemon and MCP; the later remediation Goal records escalation and resolution.
3. Add `skills/dev/start/SKILL.md`; update the common ATCT and role skills to route unexpected work upward and permit only a recorded escalation override.
4. Run focused lifecycle tests, skill validation, and `go test ./...`.
