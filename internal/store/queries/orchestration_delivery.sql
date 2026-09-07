-- name: GetOrchestrationDeliveryScope :one
SELECT scope_key, project_id, goal_id, task_id, role, agent_session_id, agent_key,
       source_generation, active, created_at, updated_at
FROM orchestration_scope
WHERE scope_key = ?;

-- name: GetOrchestrationDeliveryMonitorHealth :one
SELECT monitor_id, agent_key, scope_key, agent_session_id, cwd, role, project_id, goal_id, task_id, pid,
       process_started_at, state, reason, transitioned_at, last_seen_at, stopped_at
FROM monitor_health
WHERE monitor_id = ?;

-- name: GetOrchestrationDeliveryLease :one
SELECT scope_key, target_role, holder_monitor_id, fencing_token, expires_at, updated_at
FROM orchestration_delivery_leases
WHERE scope_key = ? AND target_role = ?;

-- name: RenewOrchestrationDeliveryLease :execresult
UPDATE orchestration_delivery_leases
SET expires_at = ?, updated_at = ?
WHERE scope_key = ? AND target_role = ? AND holder_monitor_id = ? AND expires_at > ?;

-- name: AcquireOrchestrationDeliveryLease :execresult
INSERT INTO orchestration_delivery_leases (
    scope_key, target_role, holder_monitor_id, fencing_token, expires_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(scope_key, target_role) DO UPDATE SET
    holder_monitor_id = excluded.holder_monitor_id,
    fencing_token = orchestration_delivery_leases.fencing_token + 1,
    expires_at = excluded.expires_at,
    updated_at = excluded.updated_at
WHERE orchestration_delivery_leases.expires_at <= excluded.updated_at;

-- name: GetOrchestrationDeliveryReceipt :one
SELECT scope_key, delivery_key, generation, target_role, holder_monitor_id,
       fencing_token, status, created_at, updated_at
FROM orchestration_delivery_receipts
WHERE scope_key = ? AND delivery_key = ? AND generation = ?;

-- name: ReserveOrchestrationDelivery :execresult
INSERT INTO orchestration_delivery_receipts (
    scope_key, delivery_key, generation, target_role, holder_monitor_id,
    fencing_token, status, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, 'reserved', ?, ?)
ON CONFLICT(scope_key, delivery_key, generation) DO UPDATE SET
    target_role = excluded.target_role,
    holder_monitor_id = excluded.holder_monitor_id,
    fencing_token = excluded.fencing_token,
    status = 'reserved',
    updated_at = excluded.updated_at
WHERE orchestration_delivery_receipts.status = 'reserved'
  AND orchestration_delivery_receipts.fencing_token < excluded.fencing_token;

-- name: AcceptOrchestrationDelivery :execresult
UPDATE orchestration_delivery_receipts
SET status = 'accepted', updated_at = ?
WHERE orchestration_delivery_receipts.scope_key = ? AND orchestration_delivery_receipts.delivery_key = ? AND orchestration_delivery_receipts.generation = ?
  AND orchestration_delivery_receipts.status = 'reserved'
  AND orchestration_delivery_receipts.holder_monitor_id = ? AND orchestration_delivery_receipts.fencing_token = ?
  AND EXISTS (
      SELECT 1
      FROM orchestration_delivery_leases
      WHERE orchestration_delivery_leases.scope_key = ? AND orchestration_delivery_leases.target_role = ?
        AND orchestration_delivery_leases.holder_monitor_id = ? AND orchestration_delivery_leases.fencing_token = ? AND orchestration_delivery_leases.expires_at > ?
  );

-- name: MarkOrchestrationDeliveryUnknown :execresult
UPDATE orchestration_delivery_receipts
SET status = 'unknown', updated_at = ?
WHERE orchestration_delivery_receipts.scope_key = ? AND orchestration_delivery_receipts.delivery_key = ? AND orchestration_delivery_receipts.generation = ?
  AND orchestration_delivery_receipts.status = 'reserved'
  AND orchestration_delivery_receipts.holder_monitor_id = ? AND orchestration_delivery_receipts.fencing_token = ?
  AND EXISTS (
      SELECT 1
      FROM orchestration_delivery_leases
      WHERE orchestration_delivery_leases.scope_key = ? AND orchestration_delivery_leases.target_role = ?
        AND orchestration_delivery_leases.holder_monitor_id = ? AND orchestration_delivery_leases.fencing_token = ? AND orchestration_delivery_leases.expires_at > ?
  );

-- name: ReleaseOrchestrationDelivery :execresult
DELETE FROM orchestration_delivery_receipts
WHERE orchestration_delivery_receipts.scope_key = ? AND orchestration_delivery_receipts.delivery_key = ? AND orchestration_delivery_receipts.generation = ?
  AND orchestration_delivery_receipts.status = 'reserved'
  AND orchestration_delivery_receipts.holder_monitor_id = ? AND orchestration_delivery_receipts.fencing_token = ?
  AND EXISTS (
      SELECT 1
      FROM orchestration_delivery_leases
      WHERE orchestration_delivery_leases.scope_key = ? AND orchestration_delivery_leases.target_role = ?
        AND orchestration_delivery_leases.holder_monitor_id = ? AND orchestration_delivery_leases.fencing_token = ? AND orchestration_delivery_leases.expires_at > ?
  );
