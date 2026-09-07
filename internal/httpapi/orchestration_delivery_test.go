package httpapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

func TestOrchestrationDeliveryAPIClaimsAndPersistsAcceptedReceipt(t *testing.T) {
	f := newBareFixture(t)
	ctx := f.ctx
	sessionID := registerTestSession(t, f.store, "delivery-api-owner", os.Getpid())
	now := time.Now().UTC().Truncate(time.Microsecond)
	scopeKey := store.GoalOrchestrationScopeKey(f.goal.ID, "api-delivery-handoff")
	if _, err := f.store.DB().ExecContext(ctx, `
		INSERT INTO orchestration_scope (
			scope_key, project_id, goal_id, task_id, role, agent_session_id,
			agent_key, source_generation, active, created_at, updated_at
		) VALUES (?, ?, ?, NULL, 'subcommander', ?, ?, ?, 1, ?, ?)`,
		scopeKey, f.project.ID, f.goal.ID, sessionID, "delivery-api-owner", "delivery-api-generation",
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert orchestration scope: %v", err)
	}
	goalID := f.goal.ID
	health := store.MonitorHealth{
		AgentKey:         "delivery-api-owner",
		ScopeKey:         scopeKey,
		AgentSessionID:   sessionID,
		CWD:              f.project.RootPath,
		Role:             "subcommander",
		ProjectID:        f.project.ID,
		GoalID:           &goalID,
		PID:              42001,
		ProcessStartedAt: now.Add(-time.Minute),
		State:            "healthy",
		TransitionedAt:   now,
		LastSeenAt:       now,
	}
	health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt, health.ScopeKey)
	if err := f.store.UpsertMonitorHealth(ctx, health); err != nil {
		t.Fatalf("UpsertMonitorHealth: %v", err)
	}

	post := func(payload map[string]any) (int, map[string]any) {
		t.Helper()
		status, _, body := doHandlerRequest(t, httpapi.New(f.store).Handler(), http.MethodPost, "/api/orchestration-delivery", mustJSON(t, payload))
		var response map[string]any
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatalf("decode orchestration delivery response: %v; body=%s", err, body)
		}
		return status, response
	}

	status, acquired := post(map[string]any{
		"operation":         "acquire",
		"scope_key":         scopeKey,
		"target_role":       "subcommander",
		"holder_monitor_id": health.MonitorID,
	})
	if status != http.StatusOK || acquired["acquired"] != true {
		t.Fatalf("acquire response status=%d body=%#v; want acquired", status, acquired)
	}
	lease, ok := acquired["lease"].(map[string]any)
	if !ok || lease["fencing_token"] != float64(1) {
		t.Fatalf("acquired lease = %#v; want fencing token 1", acquired["lease"])
	}

	status, reserved := post(map[string]any{
		"operation":         "reserve",
		"scope_key":         scopeKey,
		"target_role":       "subcommander",
		"holder_monitor_id": health.MonitorID,
		"fencing_token":     lease["fencing_token"],
		"delivery_key":      "api-delivery-1",
		"generation":        "generation-1",
	})
	if status != http.StatusOK || reserved["claimed"] != true || reserved["receipt"].(map[string]any)["status"] != store.OrchestrationDeliveryReceiptReserved {
		t.Fatalf("reserve response status=%d body=%#v; want reserved claim", status, reserved)
	}

	status, accepted := post(map[string]any{
		"operation":         "accepted",
		"scope_key":         scopeKey,
		"target_role":       "subcommander",
		"holder_monitor_id": health.MonitorID,
		"fencing_token":     lease["fencing_token"],
		"delivery_key":      "api-delivery-1",
		"generation":        "generation-1",
	})
	if status != http.StatusOK || accepted["status"] != store.OrchestrationDeliveryReceiptAccepted {
		t.Fatalf("accepted response status=%d body=%#v; want accepted", status, accepted)
	}

	status, reconnect := post(map[string]any{
		"operation":         "reserve",
		"scope_key":         scopeKey,
		"target_role":       "subcommander",
		"holder_monitor_id": health.MonitorID,
		"fencing_token":     lease["fencing_token"],
		"delivery_key":      "api-delivery-1",
		"generation":        "generation-1",
	})
	if status != http.StatusOK || reconnect["claimed"] != false || reconnect["receipt"].(map[string]any)["status"] != store.OrchestrationDeliveryReceiptAccepted {
		t.Fatalf("reconnect response status=%d body=%#v; want accepted and suppressed", status, reconnect)
	}
}

func TestOrchestrationDeliveryAPIRejectsStaleFence(t *testing.T) {
	f := newBareFixture(t)
	status, _, body := doHandlerRequest(t, httpapi.New(f.store).Handler(), http.MethodPost, "/api/orchestration-delivery", mustJSON(t, map[string]any{
		"operation":         "accepted",
		"scope_key":         "goal:missing:subcommander:handoff",
		"target_role":       "subcommander",
		"holder_monitor_id": "monitor-missing",
		"fencing_token":     1,
		"delivery_key":      "delivery",
		"generation":        "generation",
	}))
	if status != http.StatusConflict && status != http.StatusBadRequest {
		t.Fatalf("stale/missing fence status = %d; body=%s", status, body)
	}
}
