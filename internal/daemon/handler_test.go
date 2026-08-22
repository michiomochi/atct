package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/rpc"
)

func ageAgentSessionForTest(t *testing.T, fixture goalListFixture, sessionID string) {
	t.Helper()
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := fixture.store.DB().ExecContext(context.Background(), `UPDATE agent_sessions SET registered_at = ? WHERE id = ?`, old, sessionID); err != nil {
		t.Fatalf("age agent session %q: %v", sessionID, err)
	}
}

func TestDaemonAssociationKeepsFirstSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "daemon-first-run")
	ageAgentSessionForTest(t, fixture, "daemon-first-run")
	registerLiveGoalClaimSession(t, fixture, "daemon-second-run")
	listGoalForClaimTest(t, fixture, fixture.emptyTaskGoal.ID, "daemon-first-run")
	listGoalForClaimTest(t, fixture, fixture.emptyTaskGoal.ID, "daemon-second-run")

	var count int
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM agent_sessions WHERE project_id = ?`, fixture.project.ID).Scan(&count); err != nil {
		t.Fatalf("count daemon-associated sessions: %v", err)
	}
	if count != 2 {
		t.Fatalf("daemon-associated session count = %d, want 2", count)
	}
}

func TestProjectClaimRejectsLiveOtherSessionAfterDaemonAssociation(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "daemon-project-owner-run")
	ageAgentSessionForTest(t, fixture, "daemon-project-owner-run")
	registerLiveGoalClaimSession(t, fixture, "daemon-project-other-run")
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-project-owner-run"); err != nil {
		t.Fatalf("initial project.claim: %v", err)
	}
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-project-other-run"); !errors.Is(err, ErrProjectAlreadyClaimed) {
		t.Fatalf("second project.claim error = %v, want ErrProjectAlreadyClaimed", err)
	}

	var claimedBy string
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT claimed_by FROM projects WHERE id = ?`, fixture.project.ID).Scan(&claimedBy); err != nil {
		t.Fatalf("read project claim: %v", err)
	}
	if claimedBy != "daemon-project-owner-run" {
		t.Fatalf("project claimed_by = %q, want %q", claimedBy, "daemon-project-owner-run")
	}
}

func TestProjectReleaseAllowsSecondDaemonSessionToClaim(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "daemon-release-owner-run")
	registerLiveGoalClaimSession(t, fixture, "daemon-release-next-run")
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-release-owner-run"); err != nil {
		t.Fatalf("initial project.claim: %v", err)
	}
	params, err := json.Marshal(map[string]string{"project_id": fixture.project.ID})
	if err != nil {
		t.Fatalf("marshal project.release params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.release", Params: params}); err != nil {
		t.Fatalf("project.release: %v", err)
	}
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-release-next-run"); err != nil {
		t.Fatalf("second project.claim after release: %v", err)
	}
}

func TestProjectClaimTakesOverDeadDaemonSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	deadProcess := exec.Command("sleep", "60")
	if err := deadProcess.Start(); err != nil {
		t.Fatalf("start dead-session fixture: %v", err)
	}
	t.Cleanup(func() {
		if deadProcess.ProcessState == nil {
			_ = deadProcess.Process.Kill()
			_ = deadProcess.Wait()
		}
	})
	if err := fixture.store.RegisterAgentSession(context.Background(), "daemon-dead-run", deadProcess.Process.Pid); err != nil {
		t.Fatalf("RegisterAgentSession(dead): %v", err)
	}
	if err := deadProcess.Process.Kill(); err != nil {
		t.Fatalf("kill dead-session fixture: %v", err)
	}
	_ = deadProcess.Wait()
	registerLiveGoalClaimSession(t, fixture, "daemon-live-run")

	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-dead-run"); err != nil {
		t.Fatalf("initial dead project.claim: %v", err)
	}
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-live-run"); err != nil {
		t.Fatalf("take over dead project.claim: %v", err)
	}

	var claimedBy string
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT claimed_by FROM projects WHERE id = ?`, fixture.project.ID).Scan(&claimedBy); err != nil {
		t.Fatalf("read project claim: %v", err)
	}
	if claimedBy != "daemon-live-run" {
		t.Fatalf("project claimed_by = %q, want %q", claimedBy, "daemon-live-run")
	}
}
