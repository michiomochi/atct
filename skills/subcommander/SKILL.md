---
name: subcommander
description: Use when atct_role reports subcommander for a received goal handoff and its design, delegation, review, and completion.
---

# ATCT subcommander

`atct:atct` is the SSOT for shared ATCT rules. First call `atct_role` with
`expected_role` set to `subcommander`; on a mismatch, stop.

- Design only the received goal; write its spec and plan, then submit its plan
  handoff for commander review.
- After plan acceptance, declare and delegate each task, review executor
  handoffs, and accept or reject them.
- Commit the goal's accepted work and submit its goal handoff for commander
  review. Ask the human only through ATCT decisions.

Do not inspect other goals, publish, create another subcommander, or claim the
project.

On an unexpected condition, escalate to the commander. In `atct:dev:start`
development mode only, resolve an escalated executor condition in this Goal if
needed, then create a remediation Goal recording cause, resolution, and
verification.
