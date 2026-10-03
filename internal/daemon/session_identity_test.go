package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
)

func TestUnidentifiedTransportRowIsNotReportedAsExecutor(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()
	d := New(fixture.store)

	role := func(d *Daemon, id int64, require bool) (string, error) {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"agent_session_id": id, "require_identified": require})
		raw, err := d.dispatch(ctx, rpc.Request{Method: "session.role", Params: params})
		if err != nil {
			return "", err
		}
		var out struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal session.role: %v", err)
		}
		return out.Role, nil
	}
	identify := func(d *Daemon, id int64, key string) (int64, bool) {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"agent_session_id": id, "session_key": key})
		raw, err := d.dispatch(ctx, rpc.Request{Method: "session.identify", Params: params})
		if err != nil {
			t.Fatalf("session.identify: %v", err)
		}
		var out struct {
			AgentSessionID int64 `json:"agent_session_id"`
			Reattached     bool  `json:"reattached"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal session.identify: %v", err)
		}
		return out.AgentSessionID, out.Reattached
	}

	// (a) a keyless row that holds the project claim is still the commander.
	if got, err := role(d, fixture.requesterID, true); err != nil || got != "commander" {
		t.Fatalf("claim holder role = %q, %v; want commander", got, err)
	}

	// (b) identify it, then simulate a daemon restart with a new transport row.
	if canonical, reattached := identify(d, fixture.requesterID, "commander-key"); canonical != fixture.requesterID || reattached {
		t.Fatalf("identify = %d, reattached %v; want %d, false", canonical, reattached, fixture.requesterID)
	}
	restarted := New(fixture.store)
	newRow := runRegisterForTest(t, restarted, 0, "")

	// (d) the old behavior stays available to callers that do not ask.
	if got, err := role(restarted, newRow, false); err != nil || got != "executor" {
		t.Fatalf("require_identified=false role = %q, %v; want executor", got, err)
	}

	// (e) asking for an identified row refuses with the hint.
	_, err := role(restarted, newRow, true)
	if !errors.Is(err, ErrSessionNotIdentified) || !strings.Contains(err.Error(), "atct_session_identify") {
		t.Fatalf("require_identified=true error = %v; want ErrSessionNotIdentified with hint", err)
	}

	// (f) other refusals carry the hint only for the unidentified row.
	hinted := func(id int64) error {
		params, _ := json.Marshal(map[string]any{"agent_session_id": id})
		return restarted.withIdentifyHint(ctx, rpc.Request{Method: "plan.handoff.review.receive", Params: params}, ErrRoleUnauthorized)
	}
	if err := hinted(newRow); !errors.Is(err, ErrRoleUnauthorized) || !strings.Contains(err.Error(), "atct_session_identify") {
		t.Fatalf("unidentified row error = %v; want ErrRoleUnauthorized with hint", err)
	}
	if err := hinted(fixture.requesterID); !errors.Is(err, ErrRoleUnauthorized) || strings.Contains(err.Error(), "atct_session_identify") {
		t.Fatalf("identified row error = %v; want ErrRoleUnauthorized without hint", err)
	}

	// (g) re-identifying reattaches the new row to the canonical session.
	canonical, reattached := identify(restarted, newRow, "commander-key")
	if canonical != fixture.requesterID || !reattached {
		t.Fatalf("re-identify = %d, reattached %v; want %d, true", canonical, reattached, fixture.requesterID)
	}
	if got, err := role(restarted, canonical, true); err != nil || got != "commander" {
		t.Fatalf("canonical role = %q, %v; want commander", got, err)
	}
}
