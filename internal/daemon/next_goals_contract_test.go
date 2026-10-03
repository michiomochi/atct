package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
)

func TestGoalGetReturnsShallowNextGoalsByAscendingID(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()
	ctx := context.Background()
	source := fixture.active[0]
	first := fixture.active[2]
	second := fixture.active[3]
	grandchild := fixture.agentCreated[0]
	if _, err := fixture.store.DB().ExecContext(ctx, `
INSERT INTO next_goals (goal_id, next_goal_id, created_at)
VALUES (?, ?, '2026-09-20T00:00:00Z'), (?, ?, '2026-09-20T00:00:00Z'), (?, ?, '2026-09-20T00:00:00Z')`,
		source.ID, second.ID, source.ID, first.ID, first.ID, grandchild.ID); err != nil {
		t.Fatalf("insert next goals: %v", err)
	}

	params, err := json.Marshal(map[string]any{"goal_id": source.ID})
	if err != nil {
		t.Fatalf("marshal goal.get params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "goal.get", Params: params})
	if err != nil {
		t.Fatalf("goal.get: %v", err)
	}
	var response struct {
		Goal struct {
			NextGoals []json.RawMessage `json:"next_goals"`
		} `json:"goal"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode goal.get response: %v", err)
	}
	if len(response.Goal.NextGoals) != 2 {
		t.Fatalf("goal.get next_goals count = %d, want 2", len(response.Goal.NextGoals))
	}
	for index, wantID := range []int64{first.ID, second.ID} {
		var summary map[string]json.RawMessage
		if err := json.Unmarshal(response.Goal.NextGoals[index], &summary); err != nil {
			t.Fatalf("decode next_goals[%d]: %v", index, err)
		}
		var gotID int64
		if err := json.Unmarshal(summary["id"], &gotID); err != nil {
			t.Fatalf("decode next_goals[%d].id: %v", index, err)
		}
		if gotID != wantID {
			t.Errorf("next_goals[%d].id = %d, want %d", index, gotID, wantID)
		}
		if _, ok := summary["next_goals"]; ok {
			t.Errorf("next_goals[%d] recursively expanded", index)
		}
	}
	if strings.Contains(string(response.Goal.NextGoals[1]), grandchild.Content) {
		t.Fatal("goal.get nested successor leaked into shallow summary")
	}
}

func TestGoalCompleteRejectsLegacyNextStepsInput(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()
	params, err := json.Marshal(map[string]any{
		"goal_id":   fixture.active[0].ID,
		"work_done": "work", "now_possible": "result", "how_to_verify": "verify",
		"surprises": "none", "needs_review": "none", "next_steps": "legacy",
		"agent_session_id": daemonTestSessionID(t, fixture.store, "legacy-next-steps-complete"),
	})
	if err != nil {
		t.Fatalf("marshal goal.complete params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.complete", Params: params}); err == nil || !strings.Contains(err.Error(), "next_steps") {
		t.Fatalf("goal.complete legacy next_steps error = %v, want explicit unknown-field error", err)
	}
}
