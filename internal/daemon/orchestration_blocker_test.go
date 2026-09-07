package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestBlockerDispatchReportsIdempotentlyAndResolvesByOwner(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()
	ctx := context.Background()
	ownerID := daemonTestSessionID(t, fixture.store, "blocker-dispatch-owner")
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, ownerID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	params := map[string]any{
		"project_id":       fixture.project.ID,
		"scope_key":        store.ProjectOrchestrationScopeKey(fixture.project.ID),
		"kind":             store.OrchestrationBlockerDependencyMerge,
		"source_id":        "main:commit-1:goal-17",
		"generation":       "merge-generation-1",
		"owner_role":       store.OrchestrationBlockerOwnerCommander,
		"instruction":      "integrate commit-1 before resuming goal 17",
		"agent_session_id": ownerID,
	}
	firstRaw, err := dispatchBlockerTestRequest(t, fixture.daemon, "blocker.report", params)
	if err != nil {
		t.Fatalf("blocker.report: %v", err)
	}
	var first struct {
		Blocker store.OrchestrationBlocker `json:"blocker"`
		Created bool                       `json:"created"`
	}
	if err := json.Unmarshal(firstRaw, &first); err != nil {
		t.Fatalf("decode blocker.report: %v", err)
	}
	if !first.Created || first.Blocker.BlockerID == "" {
		t.Fatalf("first blocker response = %#v, want created blocker", first)
	}

	params["instruction"] = "integrate commit-1 before goal 17"
	secondRaw, err := dispatchBlockerTestRequest(t, fixture.daemon, "blocker.report", params)
	if err != nil {
		t.Fatalf("blocker.report retry: %v", err)
	}
	var second struct {
		Blocker store.OrchestrationBlocker `json:"blocker"`
		Created bool                       `json:"created"`
	}
	if err := json.Unmarshal(secondRaw, &second); err != nil {
		t.Fatalf("decode blocker.report retry: %v", err)
	}
	if second.Created || second.Blocker.BlockerID != first.Blocker.BlockerID || second.Blocker.Instruction != params["instruction"] {
		t.Fatalf("retry blocker response = %#v, want same updated blocker", second)
	}

	foreignID := daemonTestSessionID(t, fixture.store, "blocker-dispatch-foreign")
	params["agent_session_id"] = foreignID
	if _, err := dispatchBlockerTestRequest(t, fixture.daemon, "blocker.report", params); !errors.Is(err, store.ErrOrchestrationBlockerUnauthorized) {
		t.Fatalf("foreign blocker.report error = %v, want unauthorized", err)
	}

	resolveRaw, err := dispatchBlockerTestRequest(t, fixture.daemon, "blocker.resolve", map[string]any{
		"blocker_id":       first.Blocker.BlockerID,
		"agent_session_id": ownerID,
	})
	if err != nil {
		t.Fatalf("blocker.resolve: %v", err)
	}
	var resolved store.OrchestrationBlocker
	if err := json.Unmarshal(resolveRaw, &resolved); err != nil {
		t.Fatalf("decode blocker.resolve: %v", err)
	}
	if resolved.BlockerID != first.Blocker.BlockerID || resolved.ResolvedAt == nil {
		t.Fatalf("resolved blocker = %#v, want settled first blocker", resolved)
	}
}

func dispatchBlockerTestRequest(t *testing.T, d *Daemon, method string, params map[string]any) (json.RawMessage, error) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	return d.dispatch(context.Background(), rpc.Request{Method: method, Params: raw})
}
