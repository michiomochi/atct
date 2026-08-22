package store

import (
	"context"
	"errors"
	"os"
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

func TestClaimGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	g, err := s.CreateGoal(ctx, ns.ID, "Claimable goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	claimed, err := s.ClaimGoal(ctx, g.ID, "session-1")
	if err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	if claimed.ClaimedBy != "session-1" {
		t.Fatalf("ClaimedBy = %q, want session-1", claimed.ClaimedBy)
	}
	if claimed.ClaimedAt.IsZero() {
		t.Fatal("ClaimedAt is zero after claiming goal")
	}
}

func TestClaimGoalRejectsLiveClaimFromOtherSession(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)
	if err := s.RegisterAgentSession(ctx, "live-run", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goal.ID, "live-run"); err != nil {
		t.Fatalf("ClaimGoal live: %v", err)
	}

	if _, err := s.ClaimGoal(ctx, goal.ID, "other-run"); !errors.Is(err, ErrGoalAlreadyClaimed) {
		t.Fatalf("ClaimGoal error = %v, want ErrGoalAlreadyClaimed", err)
	}
}

func TestClaimGoalKeepsLiveClaimOwnerAfterRejection(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)
	if err := s.RegisterAgentSession(ctx, "live-run", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goal.ID, "live-run"); err != nil {
		t.Fatalf("ClaimGoal live: %v", err)
	}

	if _, err := s.ClaimGoal(ctx, goal.ID, "other-run"); err == nil {
		t.Fatal("ClaimGoal unexpectedly succeeded for a live claim")
	}
	got, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.ClaimedBy != "live-run" {
		t.Fatalf("ClaimedBy after rejection = %q, want live-run", got.ClaimedBy)
	}
}

func TestClaimGoalTakesOverDeadClaim(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)
	if err := s.RegisterAgentSession(ctx, "dead-run", 999999); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goal.ID, "dead-run"); err != nil {
		t.Fatalf("ClaimGoal dead: %v", err)
	}

	claimed, err := s.ClaimGoal(ctx, goal.ID, "new-run")
	if err != nil {
		t.Fatalf("ClaimGoal takeover: %v", err)
	}
	if claimed.ClaimedBy != "new-run" {
		t.Fatalf("ClaimedBy after takeover = %q, want new-run", claimed.ClaimedBy)
	}
}

func TestClaimGoalAllowsUnclaimedGoal(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)

	claimed, err := s.ClaimGoal(ctx, goal.ID, "new-run")
	if err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	if claimed.ClaimedBy != "new-run" {
		t.Fatalf("ClaimedBy = %q, want new-run", claimed.ClaimedBy)
	}
}

func TestClaimGoalAllowsSameSessionToReclaim(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)
	if err := s.RegisterAgentSession(ctx, "same-run", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goal.ID, "same-run"); err != nil {
		t.Fatalf("ClaimGoal initial: %v", err)
	}

	claimed, err := s.ClaimGoal(ctx, goal.ID, "same-run")
	if err != nil {
		t.Fatalf("ClaimGoal retry: %v", err)
	}
	if claimed.ClaimedBy != "same-run" {
		t.Fatalf("ClaimedBy after retry = %q, want same-run", claimed.ClaimedBy)
	}
}

func TestClaimGoalRejectsMissingGoalBeforeClaimLivenessCheck(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.ClaimGoal(ctx, "missing-goal-id", "new-run"); !errors.Is(err, ErrGoalNotFound) {
		t.Fatalf("ClaimGoal error = %v, want ErrGoalNotFound", err)
	}
}

func newGoalClaimFixture(t *testing.T) (*Store, domain.Goal) {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Claimable goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	return s, goal
}

func TestUnclaimedGoalHasEmptyClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	g, err := s.CreateGoal(ctx, ns.ID, "Unclaimed goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	got, err := s.GetGoal(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.ClaimedBy != "" {
		t.Fatalf("ClaimedBy = %q, want empty", got.ClaimedBy)
	}
}

func TestReleaseGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	g, err := s.CreateGoal(ctx, ns.ID, "Releasable goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, g.ID, "session-1"); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}

	if err := s.ReleaseGoal(ctx, g.ID); err != nil {
		t.Fatalf("ReleaseGoal: %v", err)
	}
	got, err := s.GetGoal(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.ClaimedBy != "" {
		t.Fatalf("ClaimedBy after release = %q, want empty", got.ClaimedBy)
	}
	if got.ClaimedAt != nil {
		t.Fatalf("ClaimedAt after release = %v, want nil", got.ClaimedAt)
	}
}

func TestClaimGoalRejectsUnknownGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.ClaimGoal(ctx, "missing-goal-id", "session-1"); !errors.Is(err, ErrGoalNotFound) {
		t.Fatalf("ClaimGoal error = %v, want ErrGoalNotFound", err)
	}
}

func TestGoalClaimLivenessSeparatesLiveAndDeadSessions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	liveGoal, err := s.CreateGoal(ctx, ns.ID, "Live goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal live: %v", err)
	}
	deadGoal, err := s.CreateGoal(ctx, ns.ID, "Dead goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal dead: %v", err)
	}

	if err := s.RegisterAgentSession(ctx, "goal-live-session", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession live: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "goal-dead-session", 999999); err != nil {
		t.Fatalf("RegisterAgentSession dead: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, liveGoal.ID, "goal-live-session"); err != nil {
		t.Fatalf("ClaimGoal live: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, deadGoal.ID, "goal-dead-session"); err != nil {
		t.Fatalf("ClaimGoal dead: %v", err)
	}

	running, stale, err := GoalClaimLiveness(ctx, s, ns.ID)
	if err != nil {
		t.Fatalf("GoalClaimLiveness: %v", err)
	}
	if len(running) != 1 || running[0].ID != liveGoal.ID {
		t.Fatalf("running goals = %#v, want [%s]", running, liveGoal.ID)
	}
	if len(stale) != 1 || stale[0].ID != deadGoal.ID {
		t.Fatalf("stale goals = %#v, want [%s]", stale, deadGoal.ID)
	}
}
