package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestCreateGoalIsActiveWithoutApprovalForAnyCreator(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "test-project", "/repos/atct")
	if err != nil {
		t.Fatal(err)
	}
	for _, creator := range []string{"human", "agent"} {
		goal, err := s.CreateGoal(ctx, project.ID, "goal by "+creator, creator)
		if err != nil {
			t.Fatal(err)
		}
		if goal.Status != domain.GoalActive || goal.Creator != creator {
			t.Fatalf("%s goal = status %q creator %q, want active/%s", creator, goal.Status, goal.Creator, creator)
		}
		var decisions int
		if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM decisions WHERE goal_id = ?`, goal.ID).Scan(&decisions); err != nil {
			t.Fatal(err)
		}
		if decisions != 0 {
			t.Fatalf("%s goal has %d decisions, want 0", creator, decisions)
		}
	}
}

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
		t.Fatalf("ListGoals returned %d goals, want 1 matching %d", len(goals), g.ID)
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
		t.Fatalf("child.DerivedFromGoalID = %d, want %d", child.DerivedFromGoalID, parent.ID)
	}

	got, err := s.GetGoal(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.DerivedFromGoalID != parent.ID {
		t.Fatalf("GetGoal DerivedFromGoalID = %d, want %d", got.DerivedFromGoalID, parent.ID)
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
	if got.DerivedFromGoalID != 0 {
		t.Fatalf("DerivedFromGoalID = %d, want zero represented as empty", got.DerivedFromGoalID)
	}
}

func TestCreateGoalRejectsUnknownDerivedFromGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := s.CreateGoal(ctx, ns.ID, "Orphan goal", "human", 999999); err == nil {
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
		t.Fatalf("DerivedFromGoalID after self-reference rejection = %d, want %d", got.DerivedFromGoalID, parent.ID)
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

	err = s.SetGoalDerivedFrom(ctx, child.ID, 999999)
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
	if got.DerivedFromGoalID != 0 {
		t.Fatalf("second.DerivedFromGoalID after cycle rejection = %d, want empty", got.DerivedFromGoalID)
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
	if got.DerivedFromGoalID != 0 {
		t.Fatalf("third.DerivedFromGoalID after cycle rejection = %d, want empty", got.DerivedFromGoalID)
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
	if err := s.SetGoalDerivedFrom(ctx, child.ID, 0); err != nil {
		t.Fatalf("SetGoalDerivedFrom clear: %v", err)
	}

	got, err := s.GetGoal(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.DerivedFromGoalID != 0 {
		t.Fatalf("DerivedFromGoalID after clear = %d, want empty", got.DerivedFromGoalID)
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

	addTestAgentSession(t, s, "session-1")
	claimed, err := s.ClaimGoal(ctx, g.ID, testSessionID("session-1"))
	if err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	handoff, err := s.openGoalHandoff(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("openGoalHandoff: %v", err)
	}
	if handoff == nil {
		t.Fatal("goal handoff is missing after claim")
	}
	if handoff.ReceivedBy != testSessionID("session-1") {
		t.Fatalf("goal handoff receiver = %d, want session-1 (%d)", handoff.ReceivedBy, testSessionID("session-1"))
	}
	if handoff.ReceivedAt == nil {
		t.Fatal("goal handoff has no receipt timestamp after claiming")
	}
}

func TestClaimGoalRejectsLiveClaimFromOtherSession(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)
	liveID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goal.ID, liveID); err != nil {
		t.Fatalf("ClaimGoal live: %v", err)
	}

	if _, err := s.ClaimGoal(ctx, goal.ID, testSessionID("other-run")); !errors.Is(err, ErrGoalAlreadyClaimed) {
		t.Fatalf("ClaimGoal error = %v, want ErrGoalAlreadyClaimed", err)
	}
}

func TestClaimGoalKeepsLiveClaimOwnerAfterRejection(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)
	liveID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goal.ID, liveID); err != nil {
		t.Fatalf("ClaimGoal live: %v", err)
	}

	if _, err := s.ClaimGoal(ctx, goal.ID, testSessionID("other-run")); err == nil {
		t.Fatal("ClaimGoal unexpectedly succeeded for a live claim")
	}
	got, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	handoff, err := s.openGoalHandoff(ctx, got.ID)
	if err != nil {
		t.Fatalf("openGoalHandoff: %v", err)
	}
	if handoff == nil {
		t.Fatal("goal handoff is missing after rejected claim")
	}
	if handoff.ReceivedBy != liveID {
		t.Fatalf("goal handoff receiver after rejection = %d, want live-run (%d)", handoff.ReceivedBy, liveID)
	}
}

func TestClaimGoalTakesOverDeadClaim(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)
	deadID, err := s.RegisterAgentSession(ctx, 999999)
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?
	`, 999999, "dead", deadID); err != nil {
		t.Fatalf("dead session fixture update failed: %v", err)
	}
	expireTestAgentSessionLease(t, s, deadID)
	expireTestAgentSessionLease(t, s, deadID)
	if _, err := s.ClaimGoal(ctx, goal.ID, deadID); err != nil {
		t.Fatalf("ClaimGoal dead: %v", err)
	}
	addTestAgentSession(t, s, "new-run")

	claimed, err := s.ClaimGoal(ctx, goal.ID, testSessionID("new-run"))
	if err != nil {
		t.Fatalf("ClaimGoal takeover: %v", err)
	}
	handoff, err := s.openGoalHandoff(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("openGoalHandoff: %v", err)
	}
	if handoff == nil || handoff.ReceivedBy != testSessionID("new-run") {
		if handoff == nil {
			t.Fatal("goal handoff is missing after takeover")
		}
		t.Fatalf("goal handoff receiver after takeover = %d, want new-run (%d)", handoff.ReceivedBy, testSessionID("new-run"))
	}
}

func TestClaimGoalAllowsUnclaimedGoal(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)

	addTestAgentSession(t, s, "new-run")
	claimed, err := s.ClaimGoal(ctx, goal.ID, testSessionID("new-run"))
	if err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	handoff, err := s.openGoalHandoff(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("openGoalHandoff: %v", err)
	}
	if handoff == nil {
		t.Fatal("goal handoff is missing after claim")
	}
	if handoff.ReceivedBy != testSessionID("new-run") {
		t.Fatalf("goal handoff receiver = %d, want new-run (%d)", handoff.ReceivedBy, testSessionID("new-run"))
	}
}

func TestClaimGoalAllowsSameSessionToReclaim(t *testing.T) {
	ctx := context.Background()
	s, goal := newGoalClaimFixture(t)
	sameID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goal.ID, sameID); err != nil {
		t.Fatalf("ClaimGoal initial: %v", err)
	}

	claimed, err := s.ClaimGoal(ctx, goal.ID, sameID)
	if err != nil {
		t.Fatalf("ClaimGoal retry: %v", err)
	}
	handoff, err := s.openGoalHandoff(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("openGoalHandoff: %v", err)
	}
	if handoff == nil {
		t.Fatal("goal handoff is missing after retry")
	}
	if handoff.ReceivedBy != sameID {
		t.Fatalf("goal handoff receiver after retry = %d, want same-run (%d)", handoff.ReceivedBy, sameID)
	}
}

func TestClaimGoalRejectsMissingGoalBeforeClaimLivenessCheck(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.ClaimGoal(ctx, 0, testSessionID("new-run")); !errors.Is(err, ErrGoalNotFound) {
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
	handoff, err := s.openGoalHandoff(ctx, got.ID)
	if err != nil {
		t.Fatalf("openGoalHandoff: %v", err)
	}
	if handoff != nil {
		t.Fatalf("goal handoff = %#v, want nil", handoff)
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
	addTestAgentSession(t, s, "session-1")
	if _, err := s.ClaimGoal(ctx, g.ID, testSessionID("session-1")); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}

	if err := s.ReleaseGoal(ctx, g.ID); err != nil {
		t.Fatalf("ReleaseGoal: %v", err)
	}
	got, err := s.GetGoal(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	handoff, err := s.openGoalHandoff(ctx, got.ID)
	if err != nil {
		t.Fatalf("openGoalHandoff after release: %v", err)
	}
	if handoff != nil {
		t.Fatalf("goal handoff after release = %#v, want nil", handoff)
	}
}

func TestClaimGoalRejectsUnknownGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.ClaimGoal(ctx, 0, testSessionID("session-1")); !errors.Is(err, ErrGoalNotFound) {
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

	liveID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession live: %v", err)
	}
	deadID, err := s.RegisterAgentSession(ctx, 999999)
	if err != nil {
		t.Fatalf("RegisterAgentSession dead: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, liveGoal.ID, liveID); err != nil {
		t.Fatalf("ClaimGoal live: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, deadGoal.ID, deadID); err != nil {
		t.Fatalf("ClaimGoal dead: %v", err)
	}
	// It held the goal while it was running; then its monitor stopped.
	expireTestAgentSessionLease(t, s, deadID)

	running, stale, err := GoalClaimLiveness(ctx, s, ns.ID)
	if err != nil {
		t.Fatalf("GoalClaimLiveness: %v", err)
	}
	if len(running) != 1 || running[0].ID != liveGoal.ID {
		t.Fatalf("running goals = %#v, want [%d]", running, liveGoal.ID)
	}
	if len(stale) != 1 || stale[0].ID != deadGoal.ID {
		t.Fatalf("stale goals = %#v, want [%d]", stale, deadGoal.ID)
	}
}
