package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type orchestrationDeliveryFixture struct {
	store  *Store
	path   string
	scope  OrchestrationScope
	health []MonitorHealth
}

func newOrchestrationDeliveryFixture(t *testing.T) orchestrationDeliveryFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "delivery.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "delivery-project", filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "delivery goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	sessionID := registerNamedTestAgentSession(t, s, "delivery-owner", os.Getpid())
	now := time.Now().UTC().Truncate(time.Microsecond)
	scopeKey := GoalOrchestrationScopeKey(goal.ID, "delivery-handoff")
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO orchestration_scope (
			scope_key, project_id, goal_id, task_id, role, agent_session_id,
			agent_key, source_generation, active, created_at, updated_at
		) VALUES (?, ?, ?, NULL, 'subcommander', ?, ?, ?, 1, ?, ?)`,
		scopeKey, project.ID, goal.ID, sessionID, "delivery-owner", "delivery-generation",
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert orchestration scope: %v", err)
	}

	goalID := goal.ID
	health := make([]MonitorHealth, 2)
	for i, pid := range []int{41001, 41002} {
		startedAt := now.Add(-time.Duration(i+1) * time.Minute)
		health[i] = MonitorHealth{
			AgentKey:         "delivery-owner",
			ScopeKey:         scopeKey,
			AgentSessionID:   sessionID,
			CWD:              project.RootPath,
			Role:             "subcommander",
			ProjectID:        project.ID,
			GoalID:           &goalID,
			PID:              pid,
			ProcessStartedAt: startedAt,
			State:            "healthy",
			TransitionedAt:   now,
			LastSeenAt:       now,
		}
		health[i].MonitorID = MonitorHealthID(health[i].CWD, health[i].Role, health[i].ProjectID, health[i].GoalID, health[i].TaskID, health[i].PID, health[i].ProcessStartedAt, health[i].ScopeKey)
		if err := s.UpsertMonitorHealth(ctx, health[i]); err != nil {
			t.Fatalf("UpsertMonitorHealth[%d]: %v", i, err)
		}
	}

	return orchestrationDeliveryFixture{
		store:  s,
		path:   path,
		scope:  OrchestrationScope{ScopeKey: scopeKey, ProjectID: project.ID, GoalID: &goalID, Role: "subcommander", AgentSessionID: sessionID, AgentKey: "delivery-owner", SourceGeneration: "delivery-generation", Active: true, CreatedAt: now, UpdatedAt: now},
		health: health,
	}
}

func TestOrchestrationDeliveryLeaseFencesWrappersAndAllowsFailover(t *testing.T) {
	fixture := newOrchestrationDeliveryFixture(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Microsecond)

	first, acquired, err := fixture.store.AcquireOrchestrationDeliveryLease(ctx, fixture.scope.ScopeKey, fixture.scope.Role, fixture.health[0].MonitorID, start)
	if err != nil {
		t.Fatalf("acquire first lease: %v", err)
	}
	if !acquired || first.FencingToken != 1 {
		t.Fatalf("first lease = %#v, acquired=%v; want token 1 and acquired", first, acquired)
	}
	second, acquired, err := fixture.store.AcquireOrchestrationDeliveryLease(ctx, fixture.scope.ScopeKey, fixture.scope.Role, fixture.health[1].MonitorID, start)
	if err != nil {
		t.Fatalf("acquire competing lease: %v", err)
	}
	if acquired || second.FencingToken != first.FencingToken {
		t.Fatalf("competing lease = %#v, acquired=%v; want fenced out", second, acquired)
	}

	renewed, acquired, err := fixture.store.AcquireOrchestrationDeliveryLease(ctx, fixture.scope.ScopeKey, fixture.scope.Role, fixture.health[0].MonitorID, start.Add(time.Second))
	if err != nil {
		t.Fatalf("renew first lease: %v", err)
	}
	if !acquired || renewed.FencingToken != first.FencingToken {
		t.Fatalf("renewed lease = %#v, acquired=%v; want same fencing token", renewed, acquired)
	}

	failoverAt := start.Add(orchestrationDeliveryLease + time.Second)
	failedOver, acquired, err := fixture.store.AcquireOrchestrationDeliveryLease(ctx, fixture.scope.ScopeKey, fixture.scope.Role, fixture.health[1].MonitorID, failoverAt)
	if err != nil {
		t.Fatalf("acquire replacement lease: %v", err)
	}
	if !acquired || failedOver.FencingToken <= first.FencingToken {
		t.Fatalf("replacement lease = %#v, acquired=%v; want newer fencing token", failedOver, acquired)
	}

	if _, _, err := fixture.store.ReserveOrchestrationDelivery(ctx, first, "decision\x00approve-1", "generation-1", failoverAt); !errors.Is(err, ErrOrchestrationDeliveryLeaseNotHeld) {
		t.Fatalf("stale reserve error = %v, want ErrOrchestrationDeliveryLeaseNotHeld", err)
	}
	receipt, claimed, err := fixture.store.ReserveOrchestrationDelivery(ctx, failedOver, "decision\x00approve-1", "generation-1", failoverAt)
	if err != nil {
		t.Fatalf("reserve replacement delivery: %v", err)
	}
	if !claimed || receipt.Status != OrchestrationDeliveryReceiptReserved {
		t.Fatalf("replacement receipt = %#v, claimed=%v; want reserved claim", receipt, claimed)
	}
	if err := fixture.store.AcceptOrchestrationDelivery(ctx, failedOver, receipt.DeliveryKey, receipt.Generation, failoverAt.Add(time.Second)); err != nil {
		t.Fatalf("accept delivery: %v", err)
	}

	reconnected, claimed, err := fixture.store.ReserveOrchestrationDelivery(ctx, failedOver, receipt.DeliveryKey, receipt.Generation, failoverAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("reserve accepted delivery after reconnect: %v", err)
	}
	if claimed || reconnected.Status != OrchestrationDeliveryReceiptAccepted {
		t.Fatalf("reconnected receipt = %#v, claimed=%v; want accepted and suppressed", reconnected, claimed)
	}
	if err := fixture.store.AcceptOrchestrationDelivery(ctx, first, receipt.DeliveryKey, receipt.Generation, failoverAt.Add(2*time.Second)); !errors.Is(err, ErrOrchestrationDeliveryLeaseNotHeld) {
		t.Fatalf("stale accept error = %v, want ErrOrchestrationDeliveryLeaseNotHeld", err)
	}
}

func TestOrchestrationDeliveryReleaseRetriesAndUnknownSuppresses(t *testing.T) {
	fixture := newOrchestrationDeliveryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease, acquired, err := fixture.store.AcquireOrchestrationDeliveryLease(ctx, fixture.scope.ScopeKey, fixture.scope.Role, fixture.health[0].MonitorID, now)
	if err != nil || !acquired {
		t.Fatalf("acquire lease: %#v, %v", lease, err)
	}

	deliveryKey := "codex\x00task-1"
	generation := "generation-2"
	reserved, claimed, err := fixture.store.ReserveOrchestrationDelivery(ctx, lease, deliveryKey, generation, now)
	if err != nil || !claimed || reserved.Status != OrchestrationDeliveryReceiptReserved {
		t.Fatalf("reserve delivery = %#v, claimed=%v, err=%v", reserved, claimed, err)
	}
	if err := fixture.store.ReleaseOrchestrationDelivery(ctx, lease, deliveryKey, generation, now.Add(time.Second)); err != nil {
		t.Fatalf("release pre-submit reservation: %v", err)
	}
	retry, claimed, err := fixture.store.ReserveOrchestrationDelivery(ctx, lease, deliveryKey, generation, now.Add(2*time.Second))
	if err != nil || !claimed || retry.Status != OrchestrationDeliveryReceiptReserved {
		t.Fatalf("retry after release = %#v, claimed=%v, err=%v", retry, claimed, err)
	}

	if err := fixture.store.MarkOrchestrationDeliveryUnknown(ctx, lease, deliveryKey, generation, now.Add(3*time.Second)); err != nil {
		t.Fatalf("mark unknown delivery: %v", err)
	}
	unknown, claimed, err := fixture.store.ReserveOrchestrationDelivery(ctx, lease, deliveryKey, generation, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("reserve unknown delivery after reconnect: %v", err)
	}
	if claimed || unknown.Status != OrchestrationDeliveryReceiptUnknown {
		t.Fatalf("unknown reconnect receipt = %#v, claimed=%v; want suppressed unknown", unknown, claimed)
	}
}

func TestOrchestrationDeliveryAcceptedReceiptSurvivesStoreReconnect(t *testing.T) {
	fixture := newOrchestrationDeliveryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease, acquired, err := fixture.store.AcquireOrchestrationDeliveryLease(ctx, fixture.scope.ScopeKey, fixture.scope.Role, fixture.health[0].MonitorID, now)
	if err != nil || !acquired {
		t.Fatalf("acquire lease: %#v, %v", lease, err)
	}
	if _, claimed, err := fixture.store.ReserveOrchestrationDelivery(ctx, lease, "claude\x00goal-1", "generation-3", now); err != nil || !claimed {
		t.Fatalf("reserve delivery: claimed=%v, err=%v", claimed, err)
	} else if err := fixture.store.AcceptOrchestrationDelivery(ctx, lease, "claude\x00goal-1", "generation-3", now.Add(time.Second)); err != nil {
		t.Fatalf("accept delivery: %v", err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close store before reconnect: %v", err)
	}
	reopened, err := Open(fixture.path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.GetOrchestrationDeliveryReceipt(ctx, fixture.scope.ScopeKey, "claude\x00goal-1", "generation-3")
	if err != nil {
		t.Fatalf("get persisted receipt: %v", err)
	}
	if got.Status != OrchestrationDeliveryReceiptAccepted {
		t.Fatalf("persisted receipt = %#v, want accepted", got)
	}
}
