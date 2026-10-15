package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestToolUseBindingAttachesTransportToTheHookedSession(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()
	d := New(fixture.store)

	bind := func(d *Daemon, key, toolUseID string) bool {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"session_key": key, "tool_use_id": toolUseID})
		raw, err := d.dispatch(ctx, rpc.Request{Method: "session.tool_use.bind", Params: params})
		if err != nil {
			t.Fatalf("session.tool_use.bind: %v", err)
		}
		var out struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal session.tool_use.bind: %v", err)
		}
		return out.OK
	}
	resolve := func(d *Daemon, id int64, toolUseID string) int64 {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"agent_session_id": id, "tool_use_id": toolUseID})
		raw, err := d.dispatch(ctx, rpc.Request{Method: "session.tool_use.resolve", Params: params})
		if err != nil {
			t.Fatalf("session.tool_use.resolve: %v", err)
		}
		var out struct {
			AgentSessionID int64 `json:"agent_session_id"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal session.tool_use.resolve: %v", err)
		}
		return out.AgentSessionID
	}
	role := func(d *Daemon, id int64) string {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"agent_session_id": id, "require_identified": true})
		raw, err := d.dispatch(ctx, rpc.Request{Method: "session.role", Params: params})
		if err != nil {
			t.Fatalf("session.role: %v", err)
		}
		var out struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal session.role: %v", err)
		}
		return out.Role
	}

	// The commander's session is identified by the hook's session_key.
	params, _ := json.Marshal(map[string]any{"agent_session_id": fixture.requesterID, "session_key": "commander-key"})
	if _, err := d.dispatch(ctx, rpc.Request{Method: "session.identify", Params: params}); err != nil {
		t.Fatalf("session.identify: %v", err)
	}

	// An unregistered key is refused and records nothing.
	if bind(d, "never-identified", "toolu_unknown") {
		t.Fatal("bind with an unregistered session_key = ok, want ok=false")
	}
	if bind(d, "commander-key", "") {
		t.Fatal("bind with an empty tool_use_id = ok, want ok=false")
	}

	// The hook binds the call; the new transport row for the same call attaches.
	if !bind(d, "commander-key", "toolu_1") {
		t.Fatal("bind with a registered session_key = ok=false, want ok=true")
	}
	transport := runRegisterForTest(t, d, 0, "")
	if got := resolve(d, transport, "toolu_1"); got != fixture.requesterID {
		t.Fatalf("resolve = %d, want canonical %d", got, fixture.requesterID)
	}
	if got := role(d, fixture.requesterID); got != "commander" {
		t.Fatalf("canonical role = %q, want commander", got)
	}

	// The binding is consumed by the first resolve.
	if got := resolve(d, transport, "toolu_1"); got != 0 {
		t.Fatalf("second resolve = %d, want 0 (consumed)", got)
	}

	// A session that is already canonical resolves to itself.
	if !bind(d, "commander-key", "toolu_2") {
		t.Fatal("second bind = ok=false, want ok=true")
	}
	if got := resolve(d, fixture.requesterID, "toolu_2"); got != fixture.requesterID {
		t.Fatalf("resolve on the canonical row = %d, want %d", got, fixture.requesterID)
	}
}

func TestToolUseBindingExpiresAfterTTL(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	d := newDaemonWithClock(fixture.store, func() time.Time { return now })

	params, _ := json.Marshal(map[string]any{"agent_session_id": fixture.requesterID, "session_key": "commander-key"})
	if _, err := d.dispatch(ctx, rpc.Request{Method: "session.identify", Params: params}); err != nil {
		t.Fatalf("session.identify: %v", err)
	}
	bindParams, _ := json.Marshal(map[string]any{"session_key": "commander-key", "tool_use_id": "toolu_ttl"})
	if _, err := d.dispatch(ctx, rpc.Request{Method: "session.tool_use.bind", Params: bindParams}); err != nil {
		t.Fatalf("session.tool_use.bind: %v", err)
	}

	now = now.Add(toolUseBindingTTL + time.Second)
	transport := runRegisterForTest(t, d, 0, "")
	resolveParams, _ := json.Marshal(map[string]any{"agent_session_id": transport, "tool_use_id": "toolu_ttl"})
	raw, err := d.dispatch(ctx, rpc.Request{Method: "session.tool_use.resolve", Params: resolveParams})
	if err != nil {
		t.Fatalf("session.tool_use.resolve: %v", err)
	}
	if string(bytes.TrimSpace(raw)) != `{"agent_session_id":0}` {
		t.Fatalf("resolve after TTL = %s, want agent_session_id 0", raw)
	}
}

func TestToolUseBindingsEvictTheOldestAtTheLimit(t *testing.T) {
	var table toolUseBindings
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	for i := 0; i <= toolUseBindingLimit; i++ {
		table.add(now.Add(time.Duration(i)*time.Millisecond), fmt.Sprintf("toolu_%d", i), "key")
	}
	if _, ok := table.take(now, "toolu_0"); ok {
		t.Fatal("oldest binding survived past the limit")
	}
	if key, ok := table.take(now, fmt.Sprintf("toolu_%d", toolUseBindingLimit)); !ok || key != "key" {
		t.Fatalf("newest binding = %q, %v; want key, true", key, ok)
	}
}

// Claude Code sends server/discover on the same connection before initialize.
// Registering for it leaves an agent_sessions row that no client uses; refusing
// it lets the client go on to initialize, which registers once.
func TestHTTPHandlerDiscoverDoesNotRegisterAnAgentSession(t *testing.T) {
	fixture := newMCPHTTPTestServer(t)
	before := len(agentSessionIDs(t, fixture.store))

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`)
	request, err := http.NewRequest(http.MethodPost, fixture.server.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Method", "server/discover")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	response.Body.Close()

	if got := len(agentSessionIDs(t, fixture.store)); got != before {
		t.Fatalf("agent_sessions after discover = %d, want %d", got, before)
	}

	newMCPHTTPTestClient(fixture.server.URL + "/mcp").initialize(t)
	if got := len(agentSessionIDs(t, fixture.store)); got != before+1 {
		t.Fatalf("agent_sessions after initialize = %d, want %d", got, before+1)
	}
}

// A transport that no binding attached stays unidentified, and a binding for a
// different tool_use_id does not attach it either.
func TestToolUseBindingLeavesAnUnboundTransportUnidentified(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()
	d := New(fixture.store)

	params, _ := json.Marshal(map[string]any{"agent_session_id": fixture.requesterID, "session_key": "commander-key"})
	if _, err := d.dispatch(ctx, rpc.Request{Method: "session.identify", Params: params}); err != nil {
		t.Fatalf("session.identify: %v", err)
	}
	if !d.bindToolUse(ctx, "commander-key", "toolu_bound").OK {
		t.Fatal("bind for the identified key = ok=false, want ok=true")
	}

	stranger := runRegisterForTest(t, d, 0, "")
	resolveParams, _ := json.Marshal(map[string]any{"agent_session_id": stranger, "tool_use_id": "toolu_unbound"})
	raw, err := d.dispatch(ctx, rpc.Request{Method: "session.tool_use.resolve", Params: resolveParams})
	if err != nil {
		t.Fatalf("session.tool_use.resolve: %v", err)
	}
	if string(bytes.TrimSpace(raw)) != `{"agent_session_id":0}` {
		t.Fatalf("resolve with an unbound tool_use_id = %s, want agent_session_id 0", raw)
	}

	if err := d.authorizeRole(ctx, []string{"commander"}, 0, fixture.claimedGoalID, 0, stranger, "test"); !errors.Is(err, ErrRoleUnauthorized) {
		t.Fatalf("unbound transport as commander error = %v, want ErrRoleUnauthorized", err)
	}
	if err := d.authorizeRole(ctx, []string{"commander"}, 0, fixture.claimedGoalID, 0, fixture.requesterID, "test"); err != nil {
		t.Fatalf("identified commander: %v", err)
	}
}

// A binding must not carry a transport into a session that was discarded after
// the hook bound the call.
func TestToolUseBindingRefusesAResolveToADiscardedSession(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()
	s := fixture.store
	d := New(s)

	params, _ := json.Marshal(map[string]any{"agent_session_id": fixture.requesterID, "session_key": "commander-key"})
	if _, err := d.dispatch(ctx, rpc.Request{Method: "session.identify", Params: params}); err != nil {
		t.Fatalf("session.identify: %v", err)
	}
	var projectID int64
	if err := s.DB().QueryRowContext(ctx, "SELECT project_id FROM goals WHERE id = ?", fixture.claimedGoalID).Scan(&projectID); err != nil {
		t.Fatalf("project of claimed goal: %v", err)
	}

	target, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(target): %v", err)
	}
	if _, _, err := s.IdentifyAgentSession(ctx, target, "target-key"); err != nil {
		t.Fatalf("IdentifyAgentSession(target): %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, target, projectID); err != nil {
		t.Fatalf("Associate target: %v", err)
	}
	request, err := s.RequestSessionDiscard(ctx, store.SessionDiscardRequest{
		ProjectID:       projectID,
		GoalID:          fixture.claimedGoalID,
		TargetSessionID: target,
		RequestedBy:     fixture.requesterID,
		Reason:          "the target session must stay revoked",
	})
	if err != nil {
		t.Fatalf("RequestSessionDiscard: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{DecisionID: request.ID, AnswerLabel: "approve"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	if _, err := s.PollDecisions(ctx, fixture.requesterID, request.ID); err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if err := s.DiscardSession(ctx, projectID, target, request.ID, fixture.requesterID); err != nil {
		t.Fatalf("DiscardSession: %v", err)
	}

	if !d.bindToolUse(ctx, "target-key", "toolu_discarded").OK {
		t.Fatal("bind for the discarded key = ok=false; the binding should be recorded and then refused at resolve")
	}
	transport := runRegisterForTest(t, d, 0, "")
	resolveParams, _ := json.Marshal(map[string]any{"agent_session_id": transport, "tool_use_id": "toolu_discarded"})
	if _, err := d.dispatch(ctx, rpc.Request{Method: "session.tool_use.resolve", Params: resolveParams}); !errors.Is(err, store.ErrSessionDiscarded) {
		t.Fatalf("resolve to a discarded session error = %v, want ErrSessionDiscarded", err)
	}
	if err := d.authorizeRole(ctx, []string{"commander"}, 0, fixture.claimedGoalID, 0, transport, "test"); !errors.Is(err, ErrRoleUnauthorized) {
		t.Fatalf("transport after a refused resolve as commander error = %v, want ErrRoleUnauthorized", err)
	}
}
