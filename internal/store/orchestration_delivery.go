package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const orchestrationDeliveryLease = 30 * time.Second

const (
	OrchestrationDeliveryReceiptReserved = "reserved"
	OrchestrationDeliveryReceiptAccepted = "accepted"
	OrchestrationDeliveryReceiptUnknown  = "unknown"
)

var (
	ErrOrchestrationDeliveryLeaseNotHeld       = errors.New("orchestration delivery lease is not held")
	ErrOrchestrationDeliveryReceiptNotReserved = errors.New("orchestration delivery receipt is not reserved")
)

// OrchestrationDeliveryLease is the fenced owner for one scope and target
// role. FencingToken changes whenever an expired owner is replaced.
type OrchestrationDeliveryLease struct {
	ScopeKey        string    `json:"scope_key"`
	TargetRole      string    `json:"target_role"`
	HolderMonitorID string    `json:"holder_monitor_id"`
	FencingToken    int64     `json:"fencing_token"`
	ExpiresAt       time.Time `json:"expires_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// OrchestrationDeliveryReceipt records the durable state of one delivery.
// Accepted and unknown receipts suppress retries after a process reconnect.
type OrchestrationDeliveryReceipt struct {
	ScopeKey        string    `json:"scope_key"`
	DeliveryKey     string    `json:"delivery_key"`
	Generation      string    `json:"generation"`
	TargetRole      string    `json:"target_role"`
	HolderMonitorID string    `json:"holder_monitor_id"`
	FencingToken    int64     `json:"fencing_token"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// AcquireOrchestrationDeliveryLease renews the current owner or atomically
// replaces an expired owner. A lease is granted only to a live monitor health
// row that matches the active lifecycle-owned scope.
func (s *Store) AcquireOrchestrationDeliveryLease(ctx context.Context, scopeKey, targetRole, holderMonitorID string, now time.Time) (OrchestrationDeliveryLease, bool, error) {
	if err := validateDeliveryLeaseIdentity(scopeKey, targetRole, holderMonitorID, now); err != nil {
		return OrchestrationDeliveryLease{}, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("begin orchestration delivery lease: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(s.db).WithTx(tx)

	scopeRow, err := q.GetOrchestrationDeliveryScope(ctx, scopeKey)
	if errors.Is(err, sql.ErrNoRows) {
		return OrchestrationDeliveryLease{}, false, nil
	}
	if err != nil {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("get orchestration delivery scope: %w", err)
	}
	scope, err := orchestrationScopeFromRow(scopeRow)
	if err != nil {
		return OrchestrationDeliveryLease{}, false, err
	}
	healthRow, err := q.GetOrchestrationDeliveryMonitorHealth(ctx, holderMonitorID)
	if errors.Is(err, sql.ErrNoRows) {
		return OrchestrationDeliveryLease{}, false, nil
	}
	if err != nil {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("get orchestration delivery monitor health: %w", err)
	}
	health, err := orchestrationDeliveryMonitorHealthFromRow(healthRow)
	if err != nil {
		return OrchestrationDeliveryLease{}, false, err
	}
	if !monitorHealthMatchesScopeAt(health, scope, now) {
		return OrchestrationDeliveryLease{}, false, nil
	}

	nowText := now.UTC().Format(time.RFC3339Nano)
	expiresText := now.Add(orchestrationDeliveryLease).UTC().Format(time.RFC3339Nano)
	renewed, err := q.RenewOrchestrationDeliveryLease(ctx, sqlcgen.RenewOrchestrationDeliveryLeaseParams{
		ExpiresAt:       expiresText,
		UpdatedAt:       nowText,
		ScopeKey:        scopeKey,
		TargetRole:      targetRole,
		HolderMonitorID: holderMonitorID,
		ExpiresAt_2:     nowText,
	})
	if err != nil {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("renew orchestration delivery lease: %w", err)
	}
	if affected, err := renewed.RowsAffected(); err != nil {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("inspect orchestration delivery lease renewal: %w", err)
	} else if affected > 0 {
		lease, err := getOrchestrationDeliveryLeaseTx(ctx, q, scopeKey, targetRole)
		if err != nil {
			return OrchestrationDeliveryLease{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return OrchestrationDeliveryLease{}, false, fmt.Errorf("commit orchestration delivery lease renewal: %w", err)
		}
		return lease, true, nil
	}

	acquired, err := q.AcquireOrchestrationDeliveryLease(ctx, sqlcgen.AcquireOrchestrationDeliveryLeaseParams{
		ScopeKey:        scopeKey,
		TargetRole:      targetRole,
		HolderMonitorID: holderMonitorID,
		FencingToken:    1,
		ExpiresAt:       expiresText,
		UpdatedAt:       nowText,
	})
	if err != nil {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("acquire orchestration delivery lease: %w", err)
	}
	if _, err := acquired.RowsAffected(); err != nil {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("inspect orchestration delivery lease acquisition: %w", err)
	}
	lease, err := getOrchestrationDeliveryLeaseTx(ctx, q, scopeKey, targetRole)
	if errors.Is(err, sql.ErrNoRows) {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("acquire orchestration delivery lease produced no row")
	}
	if err != nil {
		return OrchestrationDeliveryLease{}, false, err
	}
	if lease.HolderMonitorID != holderMonitorID || !lease.ExpiresAt.After(now.UTC()) {
		if err := tx.Commit(); err != nil {
			return OrchestrationDeliveryLease{}, false, fmt.Errorf("commit competing orchestration delivery lease: %w", err)
		}
		return lease, false, nil
	}
	if err := tx.Commit(); err != nil {
		return OrchestrationDeliveryLease{}, false, fmt.Errorf("commit orchestration delivery lease acquisition: %w", err)
	}
	return lease, true, nil
}

// ReserveOrchestrationDelivery records the reservation before a transport is
// called. A false claim means an accepted, unknown, or already-reserved
// receipt must not be submitted again.
func (s *Store) ReserveOrchestrationDelivery(ctx context.Context, lease OrchestrationDeliveryLease, deliveryKey, generation string, now time.Time) (OrchestrationDeliveryReceipt, bool, error) {
	if err := validateDeliveryReservation(lease, deliveryKey, generation, now); err != nil {
		return OrchestrationDeliveryReceipt{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrchestrationDeliveryReceipt{}, false, fmt.Errorf("begin orchestration delivery reservation: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(s.db).WithTx(tx)
	if err := assertCurrentOrchestrationDeliveryLease(ctx, q, lease, now); err != nil {
		return OrchestrationDeliveryReceipt{}, false, err
	}

	existing, err := getOrchestrationDeliveryReceiptTx(ctx, q, lease.ScopeKey, deliveryKey, generation)
	if err == nil {
		if existing.Status == OrchestrationDeliveryReceiptAccepted || existing.Status == OrchestrationDeliveryReceiptUnknown {
			if err := tx.Commit(); err != nil {
				return OrchestrationDeliveryReceipt{}, false, fmt.Errorf("commit settled orchestration delivery lookup: %w", err)
			}
			return existing, false, nil
		}
		if existing.Status == OrchestrationDeliveryReceiptReserved && existing.FencingToken >= lease.FencingToken {
			if err := tx.Commit(); err != nil {
				return OrchestrationDeliveryReceipt{}, false, fmt.Errorf("commit existing orchestration delivery reservation: %w", err)
			}
			return existing, false, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return OrchestrationDeliveryReceipt{}, false, err
	}

	at := now.UTC().Format(time.RFC3339Nano)
	result, err := q.ReserveOrchestrationDelivery(ctx, sqlcgen.ReserveOrchestrationDeliveryParams{
		ScopeKey:        lease.ScopeKey,
		DeliveryKey:     deliveryKey,
		Generation:      generation,
		TargetRole:      lease.TargetRole,
		HolderMonitorID: lease.HolderMonitorID,
		FencingToken:    lease.FencingToken,
		CreatedAt:       at,
		UpdatedAt:       at,
	})
	if err != nil {
		return OrchestrationDeliveryReceipt{}, false, fmt.Errorf("reserve orchestration delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return OrchestrationDeliveryReceipt{}, false, fmt.Errorf("inspect orchestration delivery reservation: %w", err)
	}
	receipt, err := getOrchestrationDeliveryReceiptTx(ctx, q, lease.ScopeKey, deliveryKey, generation)
	if err != nil {
		return OrchestrationDeliveryReceipt{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return OrchestrationDeliveryReceipt{}, false, fmt.Errorf("commit orchestration delivery reservation: %w", err)
	}
	return receipt, affected > 0, nil
}

// AcceptOrchestrationDelivery durably records that the transport accepted the
// action. It is idempotent for the same accepted receipt and rejects stale
// fencing tokens.
func (s *Store) AcceptOrchestrationDelivery(ctx context.Context, lease OrchestrationDeliveryLease, deliveryKey, generation string, now time.Time) error {
	return s.settleOrchestrationDelivery(ctx, lease, deliveryKey, generation, OrchestrationDeliveryReceiptAccepted, now)
}

// MarkOrchestrationDeliveryUnknown records a post-submit timeout or crash.
// Unknown receipts are intentionally never retried automatically.
func (s *Store) MarkOrchestrationDeliveryUnknown(ctx context.Context, lease OrchestrationDeliveryLease, deliveryKey, generation string, now time.Time) error {
	return s.settleOrchestrationDelivery(ctx, lease, deliveryKey, generation, OrchestrationDeliveryReceiptUnknown, now)
}

func (s *Store) settleOrchestrationDelivery(ctx context.Context, lease OrchestrationDeliveryLease, deliveryKey, generation, status string, now time.Time) error {
	if err := validateDeliveryReservation(lease, deliveryKey, generation, now); err != nil {
		return err
	}
	if status != OrchestrationDeliveryReceiptAccepted && status != OrchestrationDeliveryReceiptUnknown {
		return fmt.Errorf("invalid orchestration delivery settlement %q", status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin orchestration delivery settlement: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(s.db).WithTx(tx)
	if err := assertCurrentOrchestrationDeliveryLease(ctx, q, lease, now); err != nil {
		return err
	}
	receipt, err := getOrchestrationDeliveryReceiptTx(ctx, q, lease.ScopeKey, deliveryKey, generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOrchestrationDeliveryReceiptNotReserved
	}
	if err != nil {
		return err
	}
	if receipt.Status == status {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit idempotent orchestration delivery settlement: %w", err)
		}
		return nil
	}
	if receipt.Status != OrchestrationDeliveryReceiptReserved {
		return ErrOrchestrationDeliveryReceiptNotReserved
	}

	at := now.UTC().Format(time.RFC3339Nano)
	var result sql.Result
	if status == OrchestrationDeliveryReceiptAccepted {
		result, err = q.AcceptOrchestrationDelivery(ctx, sqlcgen.AcceptOrchestrationDeliveryParams{
			UpdatedAt:         at,
			ScopeKey:          lease.ScopeKey,
			DeliveryKey:       deliveryKey,
			Generation:        generation,
			HolderMonitorID:   lease.HolderMonitorID,
			FencingToken:      lease.FencingToken,
			ScopeKey_2:        lease.ScopeKey,
			TargetRole:        lease.TargetRole,
			HolderMonitorID_2: lease.HolderMonitorID,
			FencingToken_2:    lease.FencingToken,
			ExpiresAt:         at,
		})
	} else {
		result, err = q.MarkOrchestrationDeliveryUnknown(ctx, sqlcgen.MarkOrchestrationDeliveryUnknownParams{
			UpdatedAt:         at,
			ScopeKey:          lease.ScopeKey,
			DeliveryKey:       deliveryKey,
			Generation:        generation,
			HolderMonitorID:   lease.HolderMonitorID,
			FencingToken:      lease.FencingToken,
			ScopeKey_2:        lease.ScopeKey,
			TargetRole:        lease.TargetRole,
			HolderMonitorID_2: lease.HolderMonitorID,
			FencingToken_2:    lease.FencingToken,
			ExpiresAt:         at,
		})
	}
	if err != nil {
		return fmt.Errorf("settle orchestration delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect orchestration delivery settlement: %w", err)
	}
	if affected == 0 {
		return ErrOrchestrationDeliveryLeaseNotHeld
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit orchestration delivery settlement: %w", err)
	}
	return nil
}

// ReleaseOrchestrationDelivery removes only a still-reserved receipt after a
// known pre-submit failure, allowing the same fenced owner to retry.
func (s *Store) ReleaseOrchestrationDelivery(ctx context.Context, lease OrchestrationDeliveryLease, deliveryKey, generation string, now time.Time) error {
	if err := validateDeliveryReservation(lease, deliveryKey, generation, now); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin orchestration delivery release: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(s.db).WithTx(tx)
	if err := assertCurrentOrchestrationDeliveryLease(ctx, q, lease, now); err != nil {
		return err
	}
	receipt, err := getOrchestrationDeliveryReceiptTx(ctx, q, lease.ScopeKey, deliveryKey, generation)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit empty orchestration delivery release: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if receipt.Status != OrchestrationDeliveryReceiptReserved {
		return ErrOrchestrationDeliveryReceiptNotReserved
	}
	at := now.UTC().Format(time.RFC3339Nano)
	result, err := q.ReleaseOrchestrationDelivery(ctx, sqlcgen.ReleaseOrchestrationDeliveryParams{
		ScopeKey:          lease.ScopeKey,
		DeliveryKey:       deliveryKey,
		Generation:        generation,
		HolderMonitorID:   lease.HolderMonitorID,
		FencingToken:      lease.FencingToken,
		ScopeKey_2:        lease.ScopeKey,
		TargetRole:        lease.TargetRole,
		HolderMonitorID_2: lease.HolderMonitorID,
		FencingToken_2:    lease.FencingToken,
		ExpiresAt:         at,
	})
	if err != nil {
		return fmt.Errorf("release orchestration delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect orchestration delivery release: %w", err)
	}
	if affected == 0 {
		return ErrOrchestrationDeliveryLeaseNotHeld
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit orchestration delivery release: %w", err)
	}
	return nil
}

func (s *Store) GetOrchestrationDeliveryReceipt(ctx context.Context, scopeKey, deliveryKey, generation string) (OrchestrationDeliveryReceipt, error) {
	if strings.TrimSpace(scopeKey) == "" || strings.TrimSpace(deliveryKey) == "" || strings.TrimSpace(generation) == "" {
		return OrchestrationDeliveryReceipt{}, errors.New("orchestration delivery receipt identity is required")
	}
	return getOrchestrationDeliveryReceipt(ctx, sqlcgen.New(s.db), scopeKey, deliveryKey, generation)
}

func validateDeliveryLeaseIdentity(scopeKey, targetRole, holderMonitorID string, now time.Time) error {
	if strings.TrimSpace(scopeKey) == "" || strings.TrimSpace(targetRole) == "" || strings.TrimSpace(holderMonitorID) == "" {
		return errors.New("orchestration delivery lease identity is required")
	}
	if now.IsZero() {
		return errors.New("orchestration delivery lease time is required")
	}
	return nil
}

func validateDeliveryReservation(lease OrchestrationDeliveryLease, deliveryKey, generation string, now time.Time) error {
	if err := validateDeliveryLeaseIdentity(lease.ScopeKey, lease.TargetRole, lease.HolderMonitorID, now); err != nil {
		return err
	}
	if lease.FencingToken <= 0 {
		return errors.New("orchestration delivery lease fence is required")
	}
	if strings.TrimSpace(deliveryKey) == "" || strings.TrimSpace(generation) == "" {
		return errors.New("orchestration delivery receipt identity is required")
	}
	return nil
}

func assertCurrentOrchestrationDeliveryLease(ctx context.Context, q *sqlcgen.Queries, want OrchestrationDeliveryLease, now time.Time) error {
	got, err := getOrchestrationDeliveryLeaseTx(ctx, q, want.ScopeKey, want.TargetRole)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOrchestrationDeliveryLeaseNotHeld
	}
	if err != nil {
		return err
	}
	if got.HolderMonitorID != want.HolderMonitorID || got.FencingToken != want.FencingToken || !got.ExpiresAt.After(now.UTC()) {
		return ErrOrchestrationDeliveryLeaseNotHeld
	}
	return nil
}

func getOrchestrationDeliveryLeaseTx(ctx context.Context, q *sqlcgen.Queries, scopeKey, targetRole string) (OrchestrationDeliveryLease, error) {
	row, err := q.GetOrchestrationDeliveryLease(ctx, sqlcgen.GetOrchestrationDeliveryLeaseParams{ScopeKey: scopeKey, TargetRole: targetRole})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OrchestrationDeliveryLease{}, sql.ErrNoRows
		}
		return OrchestrationDeliveryLease{}, fmt.Errorf("get orchestration delivery lease: %w", err)
	}
	return orchestrationDeliveryLeaseFromRow(row)
}

func getOrchestrationDeliveryReceiptTx(ctx context.Context, q *sqlcgen.Queries, scopeKey, deliveryKey, generation string) (OrchestrationDeliveryReceipt, error) {
	return getOrchestrationDeliveryReceipt(ctx, q, scopeKey, deliveryKey, generation)
}

func getOrchestrationDeliveryReceipt(ctx context.Context, q *sqlcgen.Queries, scopeKey, deliveryKey, generation string) (OrchestrationDeliveryReceipt, error) {
	row, err := q.GetOrchestrationDeliveryReceipt(ctx, sqlcgen.GetOrchestrationDeliveryReceiptParams{ScopeKey: scopeKey, DeliveryKey: deliveryKey, Generation: generation})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OrchestrationDeliveryReceipt{}, sql.ErrNoRows
		}
		return OrchestrationDeliveryReceipt{}, fmt.Errorf("get orchestration delivery receipt: %w", err)
	}
	return orchestrationDeliveryReceiptFromRow(row)
}

func orchestrationDeliveryLeaseFromRow(row sqlcgen.OrchestrationDeliveryLease) (OrchestrationDeliveryLease, error) {
	lease := OrchestrationDeliveryLease{
		ScopeKey:        row.ScopeKey,
		TargetRole:      row.TargetRole,
		HolderMonitorID: row.HolderMonitorID,
		FencingToken:    row.FencingToken,
	}
	var err error
	if lease.ExpiresAt, err = time.Parse(time.RFC3339Nano, row.ExpiresAt); err != nil {
		return OrchestrationDeliveryLease{}, fmt.Errorf("parse orchestration delivery lease expiry: %w", err)
	}
	if lease.UpdatedAt, err = time.Parse(time.RFC3339Nano, row.UpdatedAt); err != nil {
		return OrchestrationDeliveryLease{}, fmt.Errorf("parse orchestration delivery lease update: %w", err)
	}
	return lease, nil
}

func orchestrationDeliveryReceiptFromRow(row sqlcgen.OrchestrationDeliveryReceipt) (OrchestrationDeliveryReceipt, error) {
	receipt := OrchestrationDeliveryReceipt{
		ScopeKey:        row.ScopeKey,
		DeliveryKey:     row.DeliveryKey,
		Generation:      row.Generation,
		TargetRole:      row.TargetRole,
		HolderMonitorID: row.HolderMonitorID,
		FencingToken:    row.FencingToken,
		Status:          row.Status,
	}
	var err error
	if receipt.CreatedAt, err = time.Parse(time.RFC3339Nano, row.CreatedAt); err != nil {
		return OrchestrationDeliveryReceipt{}, fmt.Errorf("parse orchestration delivery receipt creation: %w", err)
	}
	if receipt.UpdatedAt, err = time.Parse(time.RFC3339Nano, row.UpdatedAt); err != nil {
		return OrchestrationDeliveryReceipt{}, fmt.Errorf("parse orchestration delivery receipt update: %w", err)
	}
	return receipt, nil
}

func orchestrationDeliveryMonitorHealthFromRow(row sqlcgen.GetOrchestrationDeliveryMonitorHealthRow) (MonitorHealth, error) {
	health := MonitorHealth{
		MonitorID:      row.MonitorID,
		AgentKey:       row.AgentKey,
		ScopeKey:       row.ScopeKey,
		AgentSessionID: row.AgentSessionID,
		CWD:            row.Cwd,
		Role:           row.Role,
		ProjectID:      row.ProjectID,
		PID:            int(row.Pid),
		State:          row.State,
		Reason:         row.Reason,
	}
	if row.GoalID.Valid {
		value := row.GoalID.Int64
		health.GoalID = &value
	}
	if row.TaskID.Valid {
		value := row.TaskID.Int64
		health.TaskID = &value
	}
	var err error
	if health.ProcessStartedAt, err = time.Parse(time.RFC3339Nano, row.ProcessStartedAt); err != nil {
		return MonitorHealth{}, fmt.Errorf("parse orchestration delivery monitor process start: %w", err)
	}
	if health.TransitionedAt, err = time.Parse(time.RFC3339Nano, row.TransitionedAt); err != nil {
		return MonitorHealth{}, fmt.Errorf("parse orchestration delivery monitor transition: %w", err)
	}
	if health.LastSeenAt, err = time.Parse(time.RFC3339Nano, row.LastSeenAt); err != nil {
		return MonitorHealth{}, fmt.Errorf("parse orchestration delivery monitor last seen: %w", err)
	}
	if row.StoppedAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, row.StoppedAt.String)
		if err != nil {
			return MonitorHealth{}, fmt.Errorf("parse orchestration delivery monitor stop: %w", err)
		}
		health.StoppedAt = &value
	}
	return health, nil
}
