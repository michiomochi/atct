package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
)

func TestSessionStopCheckUsesIdentifiedSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	const sessionKey = "stop-check-session-key"
	commanderID := daemonTestSessionID(t, fixture.store, "stop-check-commander")
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, commanderID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	params, err := json.Marshal(map[string]any{"session_key": sessionKey})
	if err != nil {
		t.Fatalf("marshal session.stop_check params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "session.stop_check", Params: params})
	if err != nil {
		t.Fatalf("session.stop_check: %v", err)
	}
	var response struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode session.stop_check response: %v", err)
	}
	if response.Decision != "block" || response.Reason == "" {
		t.Fatalf("session.stop_check response = %+v, want blocking work", response)
	}
}

func TestStopCheckSubcommander(t *testing.T) {
	fixture := newGoalListFixture(t)
	t.Cleanup(func() {
		if err := fixture.store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})

	ctx := context.Background()
	const handoffID = "stop-check-subcommander-task-handoff"
	subcommanderID := daemonTestSessionID(t, fixture.store, "stop-check-subcommander")
	executorID := daemonTestSessionID(t, fixture.store, "stop-check-subcommander-executor")
	addTaskHandoffDirect(t, fixture.store, handoffID, fixture.tasks[0].ID, subcommanderID, 0)

	detail, err := fixture.daemon.stopCheckSubcommander(ctx, subcommanderID, fixture.taskGoal.ID)
	if err != nil {
		t.Fatalf("stopCheckSubcommander(request-only): %v", err)
	}
	if detail == "" {
		t.Fatal("stopCheckSubcommander(request-only) returned no blocking detail")
	}

	if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoffID, fixture.tasks[0].ID, executorID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	detail, err = fixture.daemon.stopCheckSubcommander(ctx, subcommanderID, fixture.taskGoal.ID)
	if err != nil {
		t.Fatalf("stopCheckSubcommander(received): %v", err)
	}
	if detail != "" {
		t.Fatalf("stopCheckSubcommander(received) = %q, want no block", detail)
	}

	if _, err := fixture.store.RequestTaskHandoffReview(ctx, handoffID, fixture.tasks[0].ID, executorID, "ready"); err != nil {
		t.Fatalf("RequestTaskHandoffReview: %v", err)
	}
	detail, err = fixture.daemon.stopCheckSubcommander(ctx, subcommanderID, fixture.taskGoal.ID)
	if err != nil {
		t.Fatalf("stopCheckSubcommander(review): %v", err)
	}
	if detail == "" {
		t.Fatal("stopCheckSubcommander(review) returned no blocking detail")
	}
}
