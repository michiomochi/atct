package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
)

func TestGoalListPreservesCoexistingAgentSessions(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	aID := daemonTestSessionID(t, fixture.store, "coexist-a")
	bID := daemonTestSessionID(t, fixture.store, "coexist-b")

	for _, sessionID := range []int64{aID, bID, aID} {
		params, err := json.Marshal(map[string]any{
			"cwd":              fixture.project.RootPath,
			"agent_session_id": sessionID,
		})
		if err != nil {
			t.Fatalf("marshal goal.list params for agent session %d: %v", sessionID, err)
		}
		if _, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "goal.list", Params: params}); err != nil {
			t.Fatalf("goal.list for agent session %d: %v", sessionID, err)
		}
	}

	rows, err := fixture.store.DB().QueryContext(ctx, `
		SELECT id, project_id
		FROM agent_sessions
		WHERE id IN (?, ?)
		ORDER BY id`, aID, bID)
	if err != nil {
		t.Fatalf("query coexisting agent sessions: %v", err)
	}
	defer rows.Close()

	gotIDs := make([]int64, 0, 2)
	projectIDs := make(map[int64]int64, 2)
	for rows.Next() {
		var id, projectID int64
		if err := rows.Scan(&id, &projectID); err != nil {
			t.Fatalf("scan agent session: %v", err)
		}
		gotIDs = append(gotIDs, id)
		projectIDs[id] = projectID
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate agent sessions: %v", err)
	}

	wantIDs := []int64{aID, bID}
	if len(gotIDs) != len(wantIDs) || gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Fatalf("agent session IDs after A→B→A: got %v, want %v", gotIDs, wantIDs)
	}
	for _, id := range wantIDs {
		if gotProjectID := projectIDs[id]; gotProjectID != fixture.project.ID {
			t.Errorf("agent session %d has project_id %d, want %d", id, gotProjectID, fixture.project.ID)
		}
	}
}
