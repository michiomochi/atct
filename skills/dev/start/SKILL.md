---
name: dev-start
description: Use when starting ATCT development work that may require a recorded escalation to an immediate parent role.
---

# Start development mode

Use this entry point only for development and incident resolution. It enables a
durable capability for this agent session; ordinary `atct:start` does not.

1. Call `atct_session_identify` first, then `atct_development_start`.
2. Verify the normal role with `atct_role`; this capability does not change it.
3. On an unexpected condition, escalate directly: executor → subcommander,
   subcommander → commander. Do not act outside your normal role first.
4. The parent may resolve only the escalated lower-role work. Commander may act
   as subcommander, never as executor. Record the cause and resolution in a
   remediation Goal before returning to the normal spec → plan → review flow.

See `doc/dev-escalation.md` for the policy and limits.
