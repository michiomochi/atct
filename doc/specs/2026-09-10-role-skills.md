# Role-specific ATCT skills

ATCT keeps the shared state model, safety boundary, claim rules, and role
derivation in `atct:atct`. After role verification, it routes an agent to one
of three role skills. The role skills contain only the operations and exclusions
for their role; they refer back to `atct:atct` rather than copying shared rules.

This is instruction-level enforcement: a role mismatch stops work, and the
daemon remains the authority for claims and tool authorization. It does not add
new server-side permission checks.
