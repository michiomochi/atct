-- Pad every stored timestamp's fractional second to nine digits.
--
-- These columns are TEXT and SQLite orders TEXT byte by byte, so the spelling
-- is the sort key. They were written with RFC3339Nano, which trims trailing
-- zeros, so the fraction varies in length. A shorter one is then compared
-- against the longer one's next digit -- "…657Z" meets "…657337Z" at 'Z'
-- versus '3' -- and 'Z' is the larger byte, so the earlier time sorts last.
--
-- Rows written from here on are padded at the source. These are the ones
-- already stored, left alone by that and still comparing against each other
-- and against the new ones.
--
-- agent_sessions.started_at is not one of these: it holds the operating
-- system's own process start text, in a different format entirely.

UPDATE agent_sessions SET discarded_at =
  substr(discarded_at, 1, instr(discarded_at, '.'))
  || substr(substr(discarded_at, instr(discarded_at, '.') + 1, length(discarded_at) - instr(discarded_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE discarded_at IS NOT NULL
  AND discarded_at LIKE '%Z'
  AND instr(discarded_at, '.') > 0
  AND length(discarded_at) - instr(discarded_at, '.') - 1 < 9;

UPDATE agent_sessions SET discarded_at = substr(discarded_at, 1, length(discarded_at) - 1) || '.000000000Z'
WHERE discarded_at IS NOT NULL
  AND discarded_at LIKE '%Z'
  AND instr(discarded_at, '.') = 0
  AND length(discarded_at) = 20;
UPDATE agent_sessions SET last_heartbeat_at =
  substr(last_heartbeat_at, 1, instr(last_heartbeat_at, '.'))
  || substr(substr(last_heartbeat_at, instr(last_heartbeat_at, '.') + 1, length(last_heartbeat_at) - instr(last_heartbeat_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE last_heartbeat_at IS NOT NULL
  AND last_heartbeat_at LIKE '%Z'
  AND instr(last_heartbeat_at, '.') > 0
  AND length(last_heartbeat_at) - instr(last_heartbeat_at, '.') - 1 < 9;

UPDATE agent_sessions SET last_heartbeat_at = substr(last_heartbeat_at, 1, length(last_heartbeat_at) - 1) || '.000000000Z'
WHERE last_heartbeat_at IS NOT NULL
  AND last_heartbeat_at LIKE '%Z'
  AND instr(last_heartbeat_at, '.') = 0
  AND length(last_heartbeat_at) = 20;
UPDATE agent_sessions SET registered_at =
  substr(registered_at, 1, instr(registered_at, '.'))
  || substr(substr(registered_at, instr(registered_at, '.') + 1, length(registered_at) - instr(registered_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE registered_at IS NOT NULL
  AND registered_at LIKE '%Z'
  AND instr(registered_at, '.') > 0
  AND length(registered_at) - instr(registered_at, '.') - 1 < 9;

UPDATE agent_sessions SET registered_at = substr(registered_at, 1, length(registered_at) - 1) || '.000000000Z'
WHERE registered_at IS NOT NULL
  AND registered_at LIKE '%Z'
  AND instr(registered_at, '.') = 0
  AND length(registered_at) = 20;
UPDATE decisions SET answered_at =
  substr(answered_at, 1, instr(answered_at, '.'))
  || substr(substr(answered_at, instr(answered_at, '.') + 1, length(answered_at) - instr(answered_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE answered_at IS NOT NULL
  AND answered_at LIKE '%Z'
  AND instr(answered_at, '.') > 0
  AND length(answered_at) - instr(answered_at, '.') - 1 < 9;

UPDATE decisions SET answered_at = substr(answered_at, 1, length(answered_at) - 1) || '.000000000Z'
WHERE answered_at IS NOT NULL
  AND answered_at LIKE '%Z'
  AND instr(answered_at, '.') = 0
  AND length(answered_at) = 20;
UPDATE decisions SET applied_at =
  substr(applied_at, 1, instr(applied_at, '.'))
  || substr(substr(applied_at, instr(applied_at, '.') + 1, length(applied_at) - instr(applied_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE applied_at IS NOT NULL
  AND applied_at LIKE '%Z'
  AND instr(applied_at, '.') > 0
  AND length(applied_at) - instr(applied_at, '.') - 1 < 9;

UPDATE decisions SET applied_at = substr(applied_at, 1, length(applied_at) - 1) || '.000000000Z'
WHERE applied_at IS NOT NULL
  AND applied_at LIKE '%Z'
  AND instr(applied_at, '.') = 0
  AND length(applied_at) = 20;
UPDATE decisions SET created_at =
  substr(created_at, 1, instr(created_at, '.'))
  || substr(substr(created_at, instr(created_at, '.') + 1, length(created_at) - instr(created_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') > 0
  AND length(created_at) - instr(created_at, '.') - 1 < 9;

UPDATE decisions SET created_at = substr(created_at, 1, length(created_at) - 1) || '.000000000Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') = 0
  AND length(created_at) = 20;
UPDATE decisions SET default_applied_at =
  substr(default_applied_at, 1, instr(default_applied_at, '.'))
  || substr(substr(default_applied_at, instr(default_applied_at, '.') + 1, length(default_applied_at) - instr(default_applied_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE default_applied_at IS NOT NULL
  AND default_applied_at LIKE '%Z'
  AND instr(default_applied_at, '.') > 0
  AND length(default_applied_at) - instr(default_applied_at, '.') - 1 < 9;

UPDATE decisions SET default_applied_at = substr(default_applied_at, 1, length(default_applied_at) - 1) || '.000000000Z'
WHERE default_applied_at IS NOT NULL
  AND default_applied_at LIKE '%Z'
  AND instr(default_applied_at, '.') = 0
  AND length(default_applied_at) = 20;
UPDATE goal_handoffs SET completed_report_at =
  substr(completed_report_at, 1, instr(completed_report_at, '.'))
  || substr(substr(completed_report_at, instr(completed_report_at, '.') + 1, length(completed_report_at) - instr(completed_report_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE completed_report_at IS NOT NULL
  AND completed_report_at LIKE '%Z'
  AND instr(completed_report_at, '.') > 0
  AND length(completed_report_at) - instr(completed_report_at, '.') - 1 < 9;

UPDATE goal_handoffs SET completed_report_at = substr(completed_report_at, 1, length(completed_report_at) - 1) || '.000000000Z'
WHERE completed_report_at IS NOT NULL
  AND completed_report_at LIKE '%Z'
  AND instr(completed_report_at, '.') = 0
  AND length(completed_report_at) = 20;
UPDATE goal_handoffs SET received_at =
  substr(received_at, 1, instr(received_at, '.'))
  || substr(substr(received_at, instr(received_at, '.') + 1, length(received_at) - instr(received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE received_at IS NOT NULL
  AND received_at LIKE '%Z'
  AND instr(received_at, '.') > 0
  AND length(received_at) - instr(received_at, '.') - 1 < 9;

UPDATE goal_handoffs SET received_at = substr(received_at, 1, length(received_at) - 1) || '.000000000Z'
WHERE received_at IS NOT NULL
  AND received_at LIKE '%Z'
  AND instr(received_at, '.') = 0
  AND length(received_at) = 20;
UPDATE goal_handoffs SET recovered_at =
  substr(recovered_at, 1, instr(recovered_at, '.'))
  || substr(substr(recovered_at, instr(recovered_at, '.') + 1, length(recovered_at) - instr(recovered_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE recovered_at IS NOT NULL
  AND recovered_at LIKE '%Z'
  AND instr(recovered_at, '.') > 0
  AND length(recovered_at) - instr(recovered_at, '.') - 1 < 9;

UPDATE goal_handoffs SET recovered_at = substr(recovered_at, 1, length(recovered_at) - 1) || '.000000000Z'
WHERE recovered_at IS NOT NULL
  AND recovered_at LIKE '%Z'
  AND instr(recovered_at, '.') = 0
  AND length(recovered_at) = 20;
UPDATE goal_handoffs SET requested_at =
  substr(requested_at, 1, instr(requested_at, '.'))
  || substr(substr(requested_at, instr(requested_at, '.') + 1, length(requested_at) - instr(requested_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE requested_at IS NOT NULL
  AND requested_at LIKE '%Z'
  AND instr(requested_at, '.') > 0
  AND length(requested_at) - instr(requested_at, '.') - 1 < 9;

UPDATE goal_handoffs SET requested_at = substr(requested_at, 1, length(requested_at) - 1) || '.000000000Z'
WHERE requested_at IS NOT NULL
  AND requested_at LIKE '%Z'
  AND instr(requested_at, '.') = 0
  AND length(requested_at) = 20;
UPDATE goal_handoffs SET review_received_at =
  substr(review_received_at, 1, instr(review_received_at, '.'))
  || substr(substr(review_received_at, instr(review_received_at, '.') + 1, length(review_received_at) - instr(review_received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_received_at IS NOT NULL
  AND review_received_at LIKE '%Z'
  AND instr(review_received_at, '.') > 0
  AND length(review_received_at) - instr(review_received_at, '.') - 1 < 9;

UPDATE goal_handoffs SET review_received_at = substr(review_received_at, 1, length(review_received_at) - 1) || '.000000000Z'
WHERE review_received_at IS NOT NULL
  AND review_received_at LIKE '%Z'
  AND instr(review_received_at, '.') = 0
  AND length(review_received_at) = 20;
UPDATE goal_handoffs SET review_rejected_at =
  substr(review_rejected_at, 1, instr(review_rejected_at, '.'))
  || substr(substr(review_rejected_at, instr(review_rejected_at, '.') + 1, length(review_rejected_at) - instr(review_rejected_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_rejected_at IS NOT NULL
  AND review_rejected_at LIKE '%Z'
  AND instr(review_rejected_at, '.') > 0
  AND length(review_rejected_at) - instr(review_rejected_at, '.') - 1 < 9;

UPDATE goal_handoffs SET review_rejected_at = substr(review_rejected_at, 1, length(review_rejected_at) - 1) || '.000000000Z'
WHERE review_rejected_at IS NOT NULL
  AND review_rejected_at LIKE '%Z'
  AND instr(review_rejected_at, '.') = 0
  AND length(review_rejected_at) = 20;
UPDATE goal_handoffs SET review_rejection_received_at =
  substr(review_rejection_received_at, 1, instr(review_rejection_received_at, '.'))
  || substr(substr(review_rejection_received_at, instr(review_rejection_received_at, '.') + 1, length(review_rejection_received_at) - instr(review_rejection_received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_rejection_received_at IS NOT NULL
  AND review_rejection_received_at LIKE '%Z'
  AND instr(review_rejection_received_at, '.') > 0
  AND length(review_rejection_received_at) - instr(review_rejection_received_at, '.') - 1 < 9;

UPDATE goal_handoffs SET review_rejection_received_at = substr(review_rejection_received_at, 1, length(review_rejection_received_at) - 1) || '.000000000Z'
WHERE review_rejection_received_at IS NOT NULL
  AND review_rejection_received_at LIKE '%Z'
  AND instr(review_rejection_received_at, '.') = 0
  AND length(review_rejection_received_at) = 20;
UPDATE goal_handoffs SET review_requested_at =
  substr(review_requested_at, 1, instr(review_requested_at, '.'))
  || substr(substr(review_requested_at, instr(review_requested_at, '.') + 1, length(review_requested_at) - instr(review_requested_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_requested_at IS NOT NULL
  AND review_requested_at LIKE '%Z'
  AND instr(review_requested_at, '.') > 0
  AND length(review_requested_at) - instr(review_requested_at, '.') - 1 < 9;

UPDATE goal_handoffs SET review_requested_at = substr(review_requested_at, 1, length(review_requested_at) - 1) || '.000000000Z'
WHERE review_requested_at IS NOT NULL
  AND review_requested_at LIKE '%Z'
  AND instr(review_requested_at, '.') = 0
  AND length(review_requested_at) = 20;
UPDATE goals SET created_at =
  substr(created_at, 1, instr(created_at, '.'))
  || substr(substr(created_at, instr(created_at, '.') + 1, length(created_at) - instr(created_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') > 0
  AND length(created_at) - instr(created_at, '.') - 1 < 9;

UPDATE goals SET created_at = substr(created_at, 1, length(created_at) - 1) || '.000000000Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') = 0
  AND length(created_at) = 20;
UPDATE goals SET updated_at =
  substr(updated_at, 1, instr(updated_at, '.'))
  || substr(substr(updated_at, instr(updated_at, '.') + 1, length(updated_at) - instr(updated_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE updated_at IS NOT NULL
  AND updated_at LIKE '%Z'
  AND instr(updated_at, '.') > 0
  AND length(updated_at) - instr(updated_at, '.') - 1 < 9;

UPDATE goals SET updated_at = substr(updated_at, 1, length(updated_at) - 1) || '.000000000Z'
WHERE updated_at IS NOT NULL
  AND updated_at LIKE '%Z'
  AND instr(updated_at, '.') = 0
  AND length(updated_at) = 20;
UPDATE monitor_bindings SET created_at =
  substr(created_at, 1, instr(created_at, '.'))
  || substr(substr(created_at, instr(created_at, '.') + 1, length(created_at) - instr(created_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') > 0
  AND length(created_at) - instr(created_at, '.') - 1 < 9;

UPDATE monitor_bindings SET created_at = substr(created_at, 1, length(created_at) - 1) || '.000000000Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') = 0
  AND length(created_at) = 20;
UPDATE monitor_health SET last_seen_at =
  substr(last_seen_at, 1, instr(last_seen_at, '.'))
  || substr(substr(last_seen_at, instr(last_seen_at, '.') + 1, length(last_seen_at) - instr(last_seen_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE last_seen_at IS NOT NULL
  AND last_seen_at LIKE '%Z'
  AND instr(last_seen_at, '.') > 0
  AND length(last_seen_at) - instr(last_seen_at, '.') - 1 < 9;

UPDATE monitor_health SET last_seen_at = substr(last_seen_at, 1, length(last_seen_at) - 1) || '.000000000Z'
WHERE last_seen_at IS NOT NULL
  AND last_seen_at LIKE '%Z'
  AND instr(last_seen_at, '.') = 0
  AND length(last_seen_at) = 20;
UPDATE monitor_health SET process_started_at =
  substr(process_started_at, 1, instr(process_started_at, '.'))
  || substr(substr(process_started_at, instr(process_started_at, '.') + 1, length(process_started_at) - instr(process_started_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE process_started_at IS NOT NULL
  AND process_started_at LIKE '%Z'
  AND instr(process_started_at, '.') > 0
  AND length(process_started_at) - instr(process_started_at, '.') - 1 < 9;

UPDATE monitor_health SET process_started_at = substr(process_started_at, 1, length(process_started_at) - 1) || '.000000000Z'
WHERE process_started_at IS NOT NULL
  AND process_started_at LIKE '%Z'
  AND instr(process_started_at, '.') = 0
  AND length(process_started_at) = 20;
UPDATE monitor_health SET stopped_at =
  substr(stopped_at, 1, instr(stopped_at, '.'))
  || substr(substr(stopped_at, instr(stopped_at, '.') + 1, length(stopped_at) - instr(stopped_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE stopped_at IS NOT NULL
  AND stopped_at LIKE '%Z'
  AND instr(stopped_at, '.') > 0
  AND length(stopped_at) - instr(stopped_at, '.') - 1 < 9;

UPDATE monitor_health SET stopped_at = substr(stopped_at, 1, length(stopped_at) - 1) || '.000000000Z'
WHERE stopped_at IS NOT NULL
  AND stopped_at LIKE '%Z'
  AND instr(stopped_at, '.') = 0
  AND length(stopped_at) = 20;
UPDATE monitor_health SET transitioned_at =
  substr(transitioned_at, 1, instr(transitioned_at, '.'))
  || substr(substr(transitioned_at, instr(transitioned_at, '.') + 1, length(transitioned_at) - instr(transitioned_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE transitioned_at IS NOT NULL
  AND transitioned_at LIKE '%Z'
  AND instr(transitioned_at, '.') > 0
  AND length(transitioned_at) - instr(transitioned_at, '.') - 1 < 9;

UPDATE monitor_health SET transitioned_at = substr(transitioned_at, 1, length(transitioned_at) - 1) || '.000000000Z'
WHERE transitioned_at IS NOT NULL
  AND transitioned_at LIKE '%Z'
  AND instr(transitioned_at, '.') = 0
  AND length(transitioned_at) = 20;
UPDATE plan_handoffs SET completed_report_at =
  substr(completed_report_at, 1, instr(completed_report_at, '.'))
  || substr(substr(completed_report_at, instr(completed_report_at, '.') + 1, length(completed_report_at) - instr(completed_report_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE completed_report_at IS NOT NULL
  AND completed_report_at LIKE '%Z'
  AND instr(completed_report_at, '.') > 0
  AND length(completed_report_at) - instr(completed_report_at, '.') - 1 < 9;

UPDATE plan_handoffs SET completed_report_at = substr(completed_report_at, 1, length(completed_report_at) - 1) || '.000000000Z'
WHERE completed_report_at IS NOT NULL
  AND completed_report_at LIKE '%Z'
  AND instr(completed_report_at, '.') = 0
  AND length(completed_report_at) = 20;
UPDATE plan_handoffs SET review_received_at =
  substr(review_received_at, 1, instr(review_received_at, '.'))
  || substr(substr(review_received_at, instr(review_received_at, '.') + 1, length(review_received_at) - instr(review_received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_received_at IS NOT NULL
  AND review_received_at LIKE '%Z'
  AND instr(review_received_at, '.') > 0
  AND length(review_received_at) - instr(review_received_at, '.') - 1 < 9;

UPDATE plan_handoffs SET review_received_at = substr(review_received_at, 1, length(review_received_at) - 1) || '.000000000Z'
WHERE review_received_at IS NOT NULL
  AND review_received_at LIKE '%Z'
  AND instr(review_received_at, '.') = 0
  AND length(review_received_at) = 20;
UPDATE plan_handoffs SET review_rejected_at =
  substr(review_rejected_at, 1, instr(review_rejected_at, '.'))
  || substr(substr(review_rejected_at, instr(review_rejected_at, '.') + 1, length(review_rejected_at) - instr(review_rejected_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_rejected_at IS NOT NULL
  AND review_rejected_at LIKE '%Z'
  AND instr(review_rejected_at, '.') > 0
  AND length(review_rejected_at) - instr(review_rejected_at, '.') - 1 < 9;

UPDATE plan_handoffs SET review_rejected_at = substr(review_rejected_at, 1, length(review_rejected_at) - 1) || '.000000000Z'
WHERE review_rejected_at IS NOT NULL
  AND review_rejected_at LIKE '%Z'
  AND instr(review_rejected_at, '.') = 0
  AND length(review_rejected_at) = 20;
UPDATE plan_handoffs SET review_rejection_received_at =
  substr(review_rejection_received_at, 1, instr(review_rejection_received_at, '.'))
  || substr(substr(review_rejection_received_at, instr(review_rejection_received_at, '.') + 1, length(review_rejection_received_at) - instr(review_rejection_received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_rejection_received_at IS NOT NULL
  AND review_rejection_received_at LIKE '%Z'
  AND instr(review_rejection_received_at, '.') > 0
  AND length(review_rejection_received_at) - instr(review_rejection_received_at, '.') - 1 < 9;

UPDATE plan_handoffs SET review_rejection_received_at = substr(review_rejection_received_at, 1, length(review_rejection_received_at) - 1) || '.000000000Z'
WHERE review_rejection_received_at IS NOT NULL
  AND review_rejection_received_at LIKE '%Z'
  AND instr(review_rejection_received_at, '.') = 0
  AND length(review_rejection_received_at) = 20;
UPDATE plan_handoffs SET review_requested_at =
  substr(review_requested_at, 1, instr(review_requested_at, '.'))
  || substr(substr(review_requested_at, instr(review_requested_at, '.') + 1, length(review_requested_at) - instr(review_requested_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_requested_at IS NOT NULL
  AND review_requested_at LIKE '%Z'
  AND instr(review_requested_at, '.') > 0
  AND length(review_requested_at) - instr(review_requested_at, '.') - 1 < 9;

UPDATE plan_handoffs SET review_requested_at = substr(review_requested_at, 1, length(review_requested_at) - 1) || '.000000000Z'
WHERE review_requested_at IS NOT NULL
  AND review_requested_at LIKE '%Z'
  AND instr(review_requested_at, '.') = 0
  AND length(review_requested_at) = 20;
UPDATE projects SET claimed_at =
  substr(claimed_at, 1, instr(claimed_at, '.'))
  || substr(substr(claimed_at, instr(claimed_at, '.') + 1, length(claimed_at) - instr(claimed_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE claimed_at IS NOT NULL
  AND claimed_at LIKE '%Z'
  AND instr(claimed_at, '.') > 0
  AND length(claimed_at) - instr(claimed_at, '.') - 1 < 9;

UPDATE projects SET claimed_at = substr(claimed_at, 1, length(claimed_at) - 1) || '.000000000Z'
WHERE claimed_at IS NOT NULL
  AND claimed_at LIKE '%Z'
  AND instr(claimed_at, '.') = 0
  AND length(claimed_at) = 20;
UPDATE projects SET created_at =
  substr(created_at, 1, instr(created_at, '.'))
  || substr(substr(created_at, instr(created_at, '.') + 1, length(created_at) - instr(created_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') > 0
  AND length(created_at) - instr(created_at, '.') - 1 < 9;

UPDATE projects SET created_at = substr(created_at, 1, length(created_at) - 1) || '.000000000Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') = 0
  AND length(created_at) = 20;
UPDATE task_commits SET created_at =
  substr(created_at, 1, instr(created_at, '.'))
  || substr(substr(created_at, instr(created_at, '.') + 1, length(created_at) - instr(created_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') > 0
  AND length(created_at) - instr(created_at, '.') - 1 < 9;

UPDATE task_commits SET created_at = substr(created_at, 1, length(created_at) - 1) || '.000000000Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') = 0
  AND length(created_at) = 20;
UPDATE task_create_handoffs SET completed_at =
  substr(completed_at, 1, instr(completed_at, '.'))
  || substr(substr(completed_at, instr(completed_at, '.') + 1, length(completed_at) - instr(completed_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE completed_at IS NOT NULL
  AND completed_at LIKE '%Z'
  AND instr(completed_at, '.') > 0
  AND length(completed_at) - instr(completed_at, '.') - 1 < 9;

UPDATE task_create_handoffs SET completed_at = substr(completed_at, 1, length(completed_at) - 1) || '.000000000Z'
WHERE completed_at IS NOT NULL
  AND completed_at LIKE '%Z'
  AND instr(completed_at, '.') = 0
  AND length(completed_at) = 20;
UPDATE task_create_handoffs SET received_at =
  substr(received_at, 1, instr(received_at, '.'))
  || substr(substr(received_at, instr(received_at, '.') + 1, length(received_at) - instr(received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE received_at IS NOT NULL
  AND received_at LIKE '%Z'
  AND instr(received_at, '.') > 0
  AND length(received_at) - instr(received_at, '.') - 1 < 9;

UPDATE task_create_handoffs SET received_at = substr(received_at, 1, length(received_at) - 1) || '.000000000Z'
WHERE received_at IS NOT NULL
  AND received_at LIKE '%Z'
  AND instr(received_at, '.') = 0
  AND length(received_at) = 20;
UPDATE task_create_handoffs SET recovered_at =
  substr(recovered_at, 1, instr(recovered_at, '.'))
  || substr(substr(recovered_at, instr(recovered_at, '.') + 1, length(recovered_at) - instr(recovered_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE recovered_at IS NOT NULL
  AND recovered_at LIKE '%Z'
  AND instr(recovered_at, '.') > 0
  AND length(recovered_at) - instr(recovered_at, '.') - 1 < 9;

UPDATE task_create_handoffs SET recovered_at = substr(recovered_at, 1, length(recovered_at) - 1) || '.000000000Z'
WHERE recovered_at IS NOT NULL
  AND recovered_at LIKE '%Z'
  AND instr(recovered_at, '.') = 0
  AND length(recovered_at) = 20;
UPDATE task_create_handoffs SET requested_at =
  substr(requested_at, 1, instr(requested_at, '.'))
  || substr(substr(requested_at, instr(requested_at, '.') + 1, length(requested_at) - instr(requested_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE requested_at IS NOT NULL
  AND requested_at LIKE '%Z'
  AND instr(requested_at, '.') > 0
  AND length(requested_at) - instr(requested_at, '.') - 1 < 9;

UPDATE task_create_handoffs SET requested_at = substr(requested_at, 1, length(requested_at) - 1) || '.000000000Z'
WHERE requested_at IS NOT NULL
  AND requested_at LIKE '%Z'
  AND instr(requested_at, '.') = 0
  AND length(requested_at) = 20;
UPDATE task_handoffs SET completed_report_at =
  substr(completed_report_at, 1, instr(completed_report_at, '.'))
  || substr(substr(completed_report_at, instr(completed_report_at, '.') + 1, length(completed_report_at) - instr(completed_report_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE completed_report_at IS NOT NULL
  AND completed_report_at LIKE '%Z'
  AND instr(completed_report_at, '.') > 0
  AND length(completed_report_at) - instr(completed_report_at, '.') - 1 < 9;

UPDATE task_handoffs SET completed_report_at = substr(completed_report_at, 1, length(completed_report_at) - 1) || '.000000000Z'
WHERE completed_report_at IS NOT NULL
  AND completed_report_at LIKE '%Z'
  AND instr(completed_report_at, '.') = 0
  AND length(completed_report_at) = 20;
UPDATE task_handoffs SET received_at =
  substr(received_at, 1, instr(received_at, '.'))
  || substr(substr(received_at, instr(received_at, '.') + 1, length(received_at) - instr(received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE received_at IS NOT NULL
  AND received_at LIKE '%Z'
  AND instr(received_at, '.') > 0
  AND length(received_at) - instr(received_at, '.') - 1 < 9;

UPDATE task_handoffs SET received_at = substr(received_at, 1, length(received_at) - 1) || '.000000000Z'
WHERE received_at IS NOT NULL
  AND received_at LIKE '%Z'
  AND instr(received_at, '.') = 0
  AND length(received_at) = 20;
UPDATE task_handoffs SET recovered_at =
  substr(recovered_at, 1, instr(recovered_at, '.'))
  || substr(substr(recovered_at, instr(recovered_at, '.') + 1, length(recovered_at) - instr(recovered_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE recovered_at IS NOT NULL
  AND recovered_at LIKE '%Z'
  AND instr(recovered_at, '.') > 0
  AND length(recovered_at) - instr(recovered_at, '.') - 1 < 9;

UPDATE task_handoffs SET recovered_at = substr(recovered_at, 1, length(recovered_at) - 1) || '.000000000Z'
WHERE recovered_at IS NOT NULL
  AND recovered_at LIKE '%Z'
  AND instr(recovered_at, '.') = 0
  AND length(recovered_at) = 20;
UPDATE task_handoffs SET requested_at =
  substr(requested_at, 1, instr(requested_at, '.'))
  || substr(substr(requested_at, instr(requested_at, '.') + 1, length(requested_at) - instr(requested_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE requested_at IS NOT NULL
  AND requested_at LIKE '%Z'
  AND instr(requested_at, '.') > 0
  AND length(requested_at) - instr(requested_at, '.') - 1 < 9;

UPDATE task_handoffs SET requested_at = substr(requested_at, 1, length(requested_at) - 1) || '.000000000Z'
WHERE requested_at IS NOT NULL
  AND requested_at LIKE '%Z'
  AND instr(requested_at, '.') = 0
  AND length(requested_at) = 20;
UPDATE task_handoffs SET review_received_at =
  substr(review_received_at, 1, instr(review_received_at, '.'))
  || substr(substr(review_received_at, instr(review_received_at, '.') + 1, length(review_received_at) - instr(review_received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_received_at IS NOT NULL
  AND review_received_at LIKE '%Z'
  AND instr(review_received_at, '.') > 0
  AND length(review_received_at) - instr(review_received_at, '.') - 1 < 9;

UPDATE task_handoffs SET review_received_at = substr(review_received_at, 1, length(review_received_at) - 1) || '.000000000Z'
WHERE review_received_at IS NOT NULL
  AND review_received_at LIKE '%Z'
  AND instr(review_received_at, '.') = 0
  AND length(review_received_at) = 20;
UPDATE task_handoffs SET review_rejected_at =
  substr(review_rejected_at, 1, instr(review_rejected_at, '.'))
  || substr(substr(review_rejected_at, instr(review_rejected_at, '.') + 1, length(review_rejected_at) - instr(review_rejected_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_rejected_at IS NOT NULL
  AND review_rejected_at LIKE '%Z'
  AND instr(review_rejected_at, '.') > 0
  AND length(review_rejected_at) - instr(review_rejected_at, '.') - 1 < 9;

UPDATE task_handoffs SET review_rejected_at = substr(review_rejected_at, 1, length(review_rejected_at) - 1) || '.000000000Z'
WHERE review_rejected_at IS NOT NULL
  AND review_rejected_at LIKE '%Z'
  AND instr(review_rejected_at, '.') = 0
  AND length(review_rejected_at) = 20;
UPDATE task_handoffs SET review_rejection_received_at =
  substr(review_rejection_received_at, 1, instr(review_rejection_received_at, '.'))
  || substr(substr(review_rejection_received_at, instr(review_rejection_received_at, '.') + 1, length(review_rejection_received_at) - instr(review_rejection_received_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_rejection_received_at IS NOT NULL
  AND review_rejection_received_at LIKE '%Z'
  AND instr(review_rejection_received_at, '.') > 0
  AND length(review_rejection_received_at) - instr(review_rejection_received_at, '.') - 1 < 9;

UPDATE task_handoffs SET review_rejection_received_at = substr(review_rejection_received_at, 1, length(review_rejection_received_at) - 1) || '.000000000Z'
WHERE review_rejection_received_at IS NOT NULL
  AND review_rejection_received_at LIKE '%Z'
  AND instr(review_rejection_received_at, '.') = 0
  AND length(review_rejection_received_at) = 20;
UPDATE task_handoffs SET review_requested_at =
  substr(review_requested_at, 1, instr(review_requested_at, '.'))
  || substr(substr(review_requested_at, instr(review_requested_at, '.') + 1, length(review_requested_at) - instr(review_requested_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE review_requested_at IS NOT NULL
  AND review_requested_at LIKE '%Z'
  AND instr(review_requested_at, '.') > 0
  AND length(review_requested_at) - instr(review_requested_at, '.') - 1 < 9;

UPDATE task_handoffs SET review_requested_at = substr(review_requested_at, 1, length(review_requested_at) - 1) || '.000000000Z'
WHERE review_requested_at IS NOT NULL
  AND review_requested_at LIKE '%Z'
  AND instr(review_requested_at, '.') = 0
  AND length(review_requested_at) = 20;
UPDATE tasks SET created_at =
  substr(created_at, 1, instr(created_at, '.'))
  || substr(substr(created_at, instr(created_at, '.') + 1, length(created_at) - instr(created_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') > 0
  AND length(created_at) - instr(created_at, '.') - 1 < 9;

UPDATE tasks SET created_at = substr(created_at, 1, length(created_at) - 1) || '.000000000Z'
WHERE created_at IS NOT NULL
  AND created_at LIKE '%Z'
  AND instr(created_at, '.') = 0
  AND length(created_at) = 20;
UPDATE tasks SET updated_at =
  substr(updated_at, 1, instr(updated_at, '.'))
  || substr(substr(updated_at, instr(updated_at, '.') + 1, length(updated_at) - instr(updated_at, '.') - 1) || '000000000', 1, 9)
  || 'Z'
WHERE updated_at IS NOT NULL
  AND updated_at LIKE '%Z'
  AND instr(updated_at, '.') > 0
  AND length(updated_at) - instr(updated_at, '.') - 1 < 9;

UPDATE tasks SET updated_at = substr(updated_at, 1, length(updated_at) - 1) || '.000000000Z'
WHERE updated_at IS NOT NULL
  AND updated_at LIKE '%Z'
  AND instr(updated_at, '.') = 0
  AND length(updated_at) = 20;
