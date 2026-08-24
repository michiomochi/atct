package store

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	processStartedAt = fakeProcessStartedAt
	os.Exit(m.Run())
}

func fakeProcessStartedAt(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pid must be positive: %d", pid)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return "", fmt.Errorf("get process start time for pid %d: %w", pid, err)
	}
	return fmt.Sprintf("fake-start-%d", pid), nil
}

func useRealProcessStartedAt(t *testing.T) {
	t.Helper()
	if _, err := realProcessStartedAt(os.Getpid()); err != nil {
		t.Skipf("skip: real process start time requires ps, which is unavailable: %v", err)
	}
	saved := processStartedAt
	processStartedAt = realProcessStartedAt
	t.Cleanup(func() { processStartedAt = saved })
}

func TestProcessStartedAtReturnsKernelStartTimeForCurrentProcess(t *testing.T) {
	startedAt, err := realProcessStartedAt(os.Getpid())
	if err != nil {
		t.Skipf("skip: real process start time requires ps, which is unavailable: %v", err)
	}
	t.Logf("realProcessStartedAt self pid=%d: %q", os.Getpid(), startedAt)
	if startedAt == "" {
		t.Fatal("processStartedAt returned an empty start time")
	}
}

func TestProcessStartedAtRejectsMissingProcess(t *testing.T) {
	useRealProcessStartedAt(t)
	if _, err := processStartedAt(999999); err == nil {
		t.Fatal("processStartedAt(missing pid) returned nil error")
	} else {
		t.Logf("processStartedAt missing pid=999999: %v", err)
	}
}

func TestRegisterAgentSessionStoresProcessIdentity(t *testing.T) {
	useRealProcessStartedAt(t)
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.RegisterAgentSession(ctx, "registered-run", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}

	var pid int
	var startedAt string
	if err := s.DB().QueryRowContext(ctx, `
		SELECT pid, started_at FROM agent_sessions WHERE id = ?`, "registered-run").Scan(&pid, &startedAt); err != nil {
		t.Fatalf("read registered agent session: %v", err)
	}
	wantStartedAt, err := realProcessStartedAt(os.Getpid())
	if err != nil {
		t.Skipf("skip: real process start time requires ps, which is unavailable: %v", err)
	}
	if pid != os.Getpid() || startedAt != wantStartedAt {
		t.Fatalf("stored process identity = pid %d, started_at %q; want pid %d, started_at %q", pid, startedAt, os.Getpid(), wantStartedAt)
	}
}

func TestRegisterAgentSessionStoresZeroIdentityWhenProcessLookupFails(t *testing.T) {
	useRealProcessStartedAt(t)
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.RegisterAgentSession(ctx, "unavailable-run", 999999); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}

	var pid int
	var startedAt string
	if err := s.DB().QueryRowContext(ctx, `
		SELECT pid, started_at FROM agent_sessions WHERE id = ?`, "unavailable-run").Scan(&pid, &startedAt); err != nil {
		t.Fatalf("read registered agent session: %v", err)
	}
	if pid != 0 || startedAt != "" {
		t.Fatalf("stored unavailable process identity = pid %d, started_at %q; want pid 0, empty started_at", pid, startedAt)
	}
}

func TestClaimLivenessReportsCurrentProcessAsRunning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Live claim", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "live-claim", []string{"Live task"}, []string{"Verify the live process claim."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "live-run", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "live-run", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "live-run"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	running, stale, err := ClaimLiveness(ctx, s, project.ID)
	if err != nil {
		t.Fatalf("ClaimLiveness: %v", err)
	}
	if len(running) != 1 || running[0].ID != tasks[0].ID {
		t.Fatalf("running claims = %#v, want task %s", running, tasks[0].ID)
	}
	if len(stale) != 0 {
		t.Fatalf("stale claims = %#v, want empty", stale)
	}
}

func TestClaimLivenessTreatsUnknownAndUnverifiableClaimsAsStale(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Stale claims", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "stale-claims", []string{"Unknown", "Zero pid", "Dead pid", "Mismatched start"}, []string{"Unknown session.", "Zero pid session.", "Dead pid session.", "Mismatched process start."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	claims := []struct {
		id        string
		pid       int
		startedAt string
	}{
		{id: "missing-run"},
		{id: "zero-run", pid: 0},
		{id: "dead-run", pid: 999999, startedAt: "unreachable-process-start"},
		{id: "mismatched-run", pid: os.Getpid(), startedAt: "not-the-process-start"},
	}
	for i, claim := range claims {
		if claim.id != "missing-run" {
			insertClaimLivenessSession(t, s, claim.id, project.ID, claim.pid, claim.startedAt)
		}
		if _, err := s.ClaimTask(ctx, tasks[i].ID, claim.id); err != nil {
			t.Fatalf("ClaimTask(%s): %v", claim.id, err)
		}
	}

	running, stale, err := ClaimLiveness(ctx, s, project.ID)
	if err != nil {
		t.Fatalf("ClaimLiveness: %v", err)
	}
	if len(running) != 0 {
		t.Fatalf("running claims = %#v, want empty", running)
	}
	if len(stale) != len(tasks) {
		t.Fatalf("stale claims = %#v, want %d tasks", stale, len(tasks))
	}
}

func TestClaimLivenessTreatsPIDReuseAsStale(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "PID reuse", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "pid-reuse", []string{"Reused pid"}, []string{"Do not treat a reused pid as the original process."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	insertClaimLivenessSession(t, s, "reused-pid-run", project.ID, os.Getpid(), "not-the-process-start")
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "reused-pid-run"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	running, stale, err := ClaimLiveness(ctx, s, project.ID)
	if err != nil {
		t.Fatalf("ClaimLiveness: %v", err)
	}
	if len(running) != 0 {
		t.Fatalf("running claims = %#v, want empty", running)
	}
	if len(stale) != 1 || stale[0].ID != tasks[0].ID {
		t.Fatalf("stale claims = %#v, want task %s", stale, tasks[0].ID)
	}
}

func TestClaimLivenessFiltersClaimsByProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	otherProject, err := s.CreateProject(ctx, "other", "/repos/other")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	goal, err := s.CreateGoal(ctx, otherProject.ID, "Other project", "human")
	if err != nil {
		t.Fatalf("CreateGoal other: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "other-project-claim", []string{"Other project task"}, []string{"Do not report this in the selected project."})
	if err != nil {
		t.Fatalf("DeclareTasks other: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "other-run"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	running, stale, err := ClaimLiveness(ctx, s, project.ID)
	if err != nil {
		t.Fatalf("ClaimLiveness: %v", err)
	}
	if len(running) != 0 || len(stale) != 0 {
		t.Fatalf("claims for selected project = running %#v, stale %#v; want both empty", running, stale)
	}
}

func insertClaimLivenessSession(t *testing.T, s *Store, id, projectID string, pid int, startedAt string) {
	t.Helper()
	if _, err := s.DB().ExecContext(context.Background(), `
		INSERT INTO agent_sessions (id, project_id, pid, started_at, registered_at)
		VALUES (?, ?, ?, ?, ?)`, id, projectID, pid, startedAt, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert agent session %s: %v", id, err)
	}
}
