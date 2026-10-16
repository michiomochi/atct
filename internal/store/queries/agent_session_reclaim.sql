-- Reclaim of agent sessions (goal 423 T5). The reference lists below must match
-- protectedSessionRefs and heldSessionRefs in agent_session_reclaim.go; the test
-- TestReclaimCatalogueCoversEverySessionColumn checks both directions.

-- name: ReclaimDiscardReferencedSessions :execrows
UPDATE agent_sessions SET discarded_at = sqlc.arg(discarded_at), discard_reason = sqlc.arg(discard_reason)
WHERE id IN (
  SELECT agent_sessions.id FROM agent_sessions
  WHERE COALESCE(agent_sessions.last_heartbeat_at, agent_sessions.registered_at) < sqlc.arg(idle_before)
      AND agent_sessions.session_key = ''
    AND agent_sessions.discarded_at IS NULL
    AND agent_sessions.id NOT IN (
      SELECT requested_by FROM task_handoffs WHERE requested_by IS NOT NULL
      UNION SELECT received_by FROM task_handoffs WHERE received_by IS NOT NULL
      UNION SELECT review_requested_by FROM task_handoffs WHERE review_requested_by IS NOT NULL
      UNION SELECT review_received_by FROM task_handoffs WHERE review_received_by IS NOT NULL
      UNION SELECT review_rejection_received_by FROM task_handoffs WHERE review_rejection_received_by IS NOT NULL
      UNION SELECT requested_by FROM goal_handoffs WHERE requested_by IS NOT NULL
      UNION SELECT received_by FROM goal_handoffs WHERE received_by IS NOT NULL
      UNION SELECT review_requested_by FROM goal_handoffs WHERE review_requested_by IS NOT NULL
      UNION SELECT review_received_by FROM goal_handoffs WHERE review_received_by IS NOT NULL
      UNION SELECT review_rejection_received_by FROM goal_handoffs WHERE review_rejection_received_by IS NOT NULL
      UNION SELECT review_requested_by FROM plan_handoffs WHERE review_requested_by IS NOT NULL
      UNION SELECT review_received_by FROM plan_handoffs WHERE review_received_by IS NOT NULL
      UNION SELECT review_rejection_received_by FROM plan_handoffs WHERE review_rejection_received_by IS NOT NULL
      UNION SELECT requested_by FROM task_create_handoffs WHERE requested_by IS NOT NULL
      UNION SELECT received_by FROM task_create_handoffs WHERE received_by IS NOT NULL
      UNION SELECT completed_by FROM task_create_handoffs WHERE completed_by IS NOT NULL
      UNION SELECT author_session_id FROM task_handoff_entries WHERE author_session_id IS NOT NULL
      UNION SELECT author_session_id FROM goal_handoff_entries WHERE author_session_id IS NOT NULL
      UNION SELECT author_session_id FROM plan_handoff_entries WHERE author_session_id IS NOT NULL
      UNION SELECT claimed_by FROM projects WHERE claimed_by IS NOT NULL
    )
    AND agent_sessions.id NOT IN (SELECT monitor_health.agent_session_id FROM monitor_health WHERE monitor_health.stopped_at IS NULL AND monitor_health.last_seen_at >= sqlc.arg(lease_after))
    AND agent_sessions.id IN (
      SELECT agent_session_id FROM decisions WHERE agent_session_id IS NOT NULL
      UNION SELECT agent_session_id FROM monitor_bindings WHERE agent_session_id IS NOT NULL
      UNION SELECT agent_session_id FROM monitor_health WHERE agent_session_id IS NOT NULL
      UNION SELECT discarded_by FROM agent_sessions WHERE discarded_by IS NOT NULL
    )
  LIMIT sqlc.arg(row_limit)
);

-- name: ReclaimDeleteUnreferencedSessions :execrows
-- Only a session with no key is deleted: a keyed one could register again under its
-- key, which would lift a revoke. A revoke record (discarded_by / discarded_decision_id)
-- is kept whether or not the session has a key.
DELETE FROM agent_sessions
WHERE id IN (
  SELECT agent_sessions.id FROM agent_sessions
  WHERE COALESCE(agent_sessions.last_heartbeat_at, agent_sessions.registered_at) < sqlc.arg(idle_before)
      AND agent_sessions.session_key = ''
      AND agent_sessions.discarded_by IS NULL
      AND agent_sessions.discarded_decision_id IS NULL
    AND agent_sessions.id NOT IN (
      SELECT requested_by FROM task_handoffs WHERE requested_by IS NOT NULL
      UNION SELECT received_by FROM task_handoffs WHERE received_by IS NOT NULL
      UNION SELECT review_requested_by FROM task_handoffs WHERE review_requested_by IS NOT NULL
      UNION SELECT review_received_by FROM task_handoffs WHERE review_received_by IS NOT NULL
      UNION SELECT review_rejection_received_by FROM task_handoffs WHERE review_rejection_received_by IS NOT NULL
      UNION SELECT requested_by FROM goal_handoffs WHERE requested_by IS NOT NULL
      UNION SELECT received_by FROM goal_handoffs WHERE received_by IS NOT NULL
      UNION SELECT review_requested_by FROM goal_handoffs WHERE review_requested_by IS NOT NULL
      UNION SELECT review_received_by FROM goal_handoffs WHERE review_received_by IS NOT NULL
      UNION SELECT review_rejection_received_by FROM goal_handoffs WHERE review_rejection_received_by IS NOT NULL
      UNION SELECT review_requested_by FROM plan_handoffs WHERE review_requested_by IS NOT NULL
      UNION SELECT review_received_by FROM plan_handoffs WHERE review_received_by IS NOT NULL
      UNION SELECT review_rejection_received_by FROM plan_handoffs WHERE review_rejection_received_by IS NOT NULL
      UNION SELECT requested_by FROM task_create_handoffs WHERE requested_by IS NOT NULL
      UNION SELECT received_by FROM task_create_handoffs WHERE received_by IS NOT NULL
      UNION SELECT completed_by FROM task_create_handoffs WHERE completed_by IS NOT NULL
      UNION SELECT author_session_id FROM task_handoff_entries WHERE author_session_id IS NOT NULL
      UNION SELECT author_session_id FROM goal_handoff_entries WHERE author_session_id IS NOT NULL
      UNION SELECT author_session_id FROM plan_handoff_entries WHERE author_session_id IS NOT NULL
      UNION SELECT claimed_by FROM projects WHERE claimed_by IS NOT NULL
    )
    AND agent_sessions.id NOT IN (SELECT monitor_health.agent_session_id FROM monitor_health WHERE monitor_health.stopped_at IS NULL AND monitor_health.last_seen_at >= sqlc.arg(lease_after))
    AND agent_sessions.id NOT IN (
      SELECT agent_session_id FROM decisions WHERE agent_session_id IS NOT NULL
      UNION SELECT agent_session_id FROM monitor_bindings WHERE agent_session_id IS NOT NULL
      UNION SELECT agent_session_id FROM monitor_health WHERE agent_session_id IS NOT NULL
      UNION SELECT discarded_by FROM agent_sessions WHERE discarded_by IS NOT NULL
    )
  LIMIT sqlc.arg(row_limit)
);

-- name: ReclaimDeleteDeadMonitorHealth :execrows
DELETE FROM monitor_health
WHERE rowid IN (
  SELECT rowid FROM monitor_health
  WHERE NOT (monitor_health.stopped_at IS NULL AND monitor_health.last_seen_at >= sqlc.arg(lease_after))
    AND ((monitor_health.stopped_at IS NOT NULL AND monitor_health.stopped_at < sqlc.arg(week_before))
      OR (monitor_health.agent_session_id > 0 AND monitor_health.agent_session_id NOT IN (SELECT id FROM agent_sessions)))
  LIMIT sqlc.arg(row_limit)
);

-- name: ReclaimDeleteDeadMonitorBindings :execrows
DELETE FROM monitor_bindings
WHERE rowid IN (
  SELECT rowid FROM monitor_bindings
  WHERE monitor_bindings.agent_session_id NOT IN (SELECT id FROM agent_sessions)
     OR (monitor_bindings.agent_session_id IN (SELECT id FROM agent_sessions WHERE discarded_at IS NOT NULL)
         AND COALESCE(NULLIF(monitor_bindings.last_reconciled_at, ''), monitor_bindings.created_at) < sqlc.arg(week_before))
  LIMIT sqlc.arg(row_limit)
);
