# Plan: expose role verification through MCP

1. Add a red MCP contract test for the registered tool and six real-daemon
   role fixtures, including project/goal evidence and expectation mismatch.
2. Implement the MCP adapter as a thin `session.role` RPC wrapper with an
   optional structured expectation check.
3. Update the delegation guidance to direct agents to the role-verification
   MCP tool while keeping the guidance generic.
4. Run build, vet, race tests, and a deliberate mutation of one role fixture
   to demonstrate that the corresponding inspection fails.
