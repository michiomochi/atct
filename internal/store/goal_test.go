package store

import (
	"context"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestCreateGoalStartsActive(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	g, err := s.CreateGoal(ctx, ns.ID, "Build an MCP server", "Implement seven tools")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if g.Status != domain.GoalActive {
		t.Fatalf("status = %q, want %q", g.Status, domain.GoalActive)
	}

	goals, err := s.ListGoals(ctx, ns.ID)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(goals) != 1 || goals[0].ID != g.ID {
		t.Fatalf("ListGoals returned %d goals, want 1 matching %s", len(goals), g.ID)
	}
}
