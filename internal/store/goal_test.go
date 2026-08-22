package store

import (
	"context"
	"errors"
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

	g, err := s.CreateGoal(ctx, ns.ID, "Build an MCP server\n\nImplement seven tools", "human")
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

func TestCreateGoalWithDerivedFromGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	parent, err := s.CreateGoal(ctx, ns.ID, "Parent goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal parent: %v", err)
	}
	child, err := s.CreateGoal(ctx, ns.ID, "Child goal", "human", parent.ID)
	if err != nil {
		t.Fatalf("CreateGoal child: %v", err)
	}
	if child.DerivedFromGoalID != parent.ID {
		t.Fatalf("child.DerivedFromGoalID = %q, want %q", child.DerivedFromGoalID, parent.ID)
	}

	got, err := s.GetGoal(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.DerivedFromGoalID != parent.ID {
		t.Fatalf("GetGoal DerivedFromGoalID = %q, want %q", got.DerivedFromGoalID, parent.ID)
	}
}

func TestCreateGoalWithoutDerivedFromGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	g, err := s.CreateGoal(ctx, ns.ID, "Standalone goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	got, err := s.GetGoal(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.DerivedFromGoalID != "" {
		t.Fatalf("DerivedFromGoalID = %q, want NULL represented as empty", got.DerivedFromGoalID)
	}
}

func TestCreateGoalRejectsUnknownDerivedFromGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := s.CreateGoal(ctx, ns.ID, "Orphan goal", "human", "missing-goal-id"); err == nil {
		t.Fatal("CreateGoal succeeded with a nonexistent derived-from goal")
	}
	goals, err := s.ListGoals(ctx, ns.ID)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(goals) != 0 {
		t.Fatalf("ListGoals returned %d goals after rejected insert, want 0", len(goals))
	}
}

func TestSetGoalDerivedFromAndRejectsSelfReference(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	parent, err := s.CreateGoal(ctx, ns.ID, "Parent goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal parent: %v", err)
	}
	child, err := s.CreateGoal(ctx, ns.ID, "Child goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal child: %v", err)
	}
	if err := s.SetGoalDerivedFrom(ctx, child.ID, parent.ID); err != nil {
		t.Fatalf("SetGoalDerivedFrom: %v", err)
	}
	if err := s.SetGoalDerivedFrom(ctx, child.ID, child.ID); err == nil {
		t.Fatal("SetGoalDerivedFrom accepted a self-reference")
	}

	got, err := s.GetGoal(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.DerivedFromGoalID != parent.ID {
		t.Fatalf("DerivedFromGoalID after self-reference rejection = %q, want %q", got.DerivedFromGoalID, parent.ID)
	}
}

func TestSetGoalDerivedFromRejectsUnknownParent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	child, err := s.CreateGoal(ctx, ns.ID, "Child goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	err = s.SetGoalDerivedFrom(ctx, child.ID, "missing-goal-id")
	if !errors.Is(err, ErrGoalNotFound) {
		t.Fatalf("SetGoalDerivedFrom error = %v, want ErrGoalNotFound", err)
	}
}

func TestSetGoalDerivedFromRejectsTwoNodeCycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	first, err := s.CreateGoal(ctx, ns.ID, "First goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal first: %v", err)
	}
	second, err := s.CreateGoal(ctx, ns.ID, "Second goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal second: %v", err)
	}
	if err := s.SetGoalDerivedFrom(ctx, first.ID, second.ID); err != nil {
		t.Fatalf("SetGoalDerivedFrom first: %v", err)
	}

	err = s.SetGoalDerivedFrom(ctx, second.ID, first.ID)
	if !errors.Is(err, ErrGoalDerivationCycle) {
		t.Fatalf("SetGoalDerivedFrom cycle error = %v, want ErrGoalDerivationCycle", err)
	}
	got, err := s.GetGoal(ctx, second.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.DerivedFromGoalID != "" {
		t.Fatalf("second.DerivedFromGoalID after cycle rejection = %q, want empty", got.DerivedFromGoalID)
	}
}

func TestSetGoalDerivedFromRejectsThreeNodeCycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	first, err := s.CreateGoal(ctx, ns.ID, "First goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal first: %v", err)
	}
	second, err := s.CreateGoal(ctx, ns.ID, "Second goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal second: %v", err)
	}
	third, err := s.CreateGoal(ctx, ns.ID, "Third goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal third: %v", err)
	}
	if err := s.SetGoalDerivedFrom(ctx, first.ID, second.ID); err != nil {
		t.Fatalf("SetGoalDerivedFrom first: %v", err)
	}
	if err := s.SetGoalDerivedFrom(ctx, second.ID, third.ID); err != nil {
		t.Fatalf("SetGoalDerivedFrom second: %v", err)
	}

	err = s.SetGoalDerivedFrom(ctx, third.ID, first.ID)
	if !errors.Is(err, ErrGoalDerivationCycle) {
		t.Fatalf("SetGoalDerivedFrom cycle error = %v, want ErrGoalDerivationCycle", err)
	}
	got, err := s.GetGoal(ctx, third.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.DerivedFromGoalID != "" {
		t.Fatalf("third.DerivedFromGoalID after cycle rejection = %q, want empty", got.DerivedFromGoalID)
	}
}

func TestSetGoalDerivedFromClearsParent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	parent, err := s.CreateGoal(ctx, ns.ID, "Parent goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal parent: %v", err)
	}
	child, err := s.CreateGoal(ctx, ns.ID, "Child goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal child: %v", err)
	}
	if err := s.SetGoalDerivedFrom(ctx, child.ID, parent.ID); err != nil {
		t.Fatalf("SetGoalDerivedFrom parent: %v", err)
	}
	if err := s.SetGoalDerivedFrom(ctx, child.ID, ""); err != nil {
		t.Fatalf("SetGoalDerivedFrom clear: %v", err)
	}

	got, err := s.GetGoal(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.DerivedFromGoalID != "" {
		t.Fatalf("DerivedFromGoalID after clear = %q, want empty", got.DerivedFromGoalID)
	}
}
