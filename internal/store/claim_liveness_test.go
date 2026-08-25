package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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

func TestProcessStartedAtMatchesPSOutput(t *testing.T) {
	want, err := exec.Command("ps", "-p", fmt.Sprintf("%d", os.Getpid()), "-o", "lstart=").Output()
	if err != nil {
		t.Skipf("skip: ps is unavailable: %v", err)
	}

	got, err := realProcessStartedAt(os.Getpid())
	if err != nil {
		t.Fatalf("real process start time: %v", err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Fatalf("realProcessStartedAt = %q; ps = %q", got, want)
	}
}

func TestFormatProcessStartedAtUsesFixedPSLayout(t *testing.T) {
	tests := []struct {
		name string
		when time.Time
		want string
	}{
		{
			name: "single digit day",
			when: time.Date(2026, time.August, 5, 9, 3, 1, 0, time.Local),
			want: "Wed Aug  5 09:03:01 2026    \n",
		},
		{
			name: "two digit day",
			when: time.Date(2026, time.August, 25, 18, 7, 20, 0, time.Local),
			want: "Tue Aug 25 18:07:20 2026    \n",
		},
		{
			name: "single digit day in December",
			when: time.Date(2026, time.December, 1, 0, 0, 0, 0, time.Local),
			want: "Tue Dec  1 00:00:00 2026    \n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatProcessStartedAt(tt.when.Unix(), 0)
			if got != tt.want {
				t.Fatalf("formatProcessStartedAt = %q; want %q", got, tt.want)
			}
			if len(got) != 29 {
				t.Fatalf("formatProcessStartedAt length = %d; want 29", len(got))
			}
		})
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

func TestProcessStartedAtRejectsNonPositivePID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := realProcessStartedAt(pid); err == nil {
			t.Errorf("realProcessStartedAt(%d) returned nil error", pid)
		}
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
	insertClaimLivenessHandoff(t, s, tasks[0].ID, "live-claim-handoff", "live-run")

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
		requestedBy := any(nil)
		if claim.id != "missing-run" {
			requestedBy = claim.id
		}
		insertClaimLivenessHandoff(t, s, tasks[i].ID, "claim-"+claim.id, requestedBy)
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

func TestClaimLivenessTreatsSharedDeadPIDClaimsAsStale(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Shared dead PID claims", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "shared-dead-pid", []string{
		"First space claim",
		"Second space claim",
		"Third space claim",
	}, []string{
		"Treat the first dead PID claim as stale.",
		"Treat the second dead PID claim as stale.",
		"Treat the third dead PID claim as stale.",
	})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	for i, task := range tasks {
		sessionID := fmt.Sprintf("shared-dead-run-%d", i)
		insertClaimLivenessSession(t, s, sessionID, project.ID, 999999, "daemon-before-restart")
		insertClaimLivenessHandoff(t, s, task.ID, fmt.Sprintf("shared-dead-handoff-%d", i), sessionID)
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

func TestClaimLivenessKeepsSharedLivePIDClaimsRunning(t *testing.T) {
	useRealProcessStartedAt(t)
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Shared live PID claims", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "shared-live-pid", []string{
		"First live space claim",
		"Second live space claim",
		"Third live space claim",
	}, []string{
		"Keep the first live PID claim running.",
		"Keep the second live PID claim running.",
		"Keep the third live PID claim running.",
	})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	startedAt, err := realProcessStartedAt(os.Getpid())
	if err != nil {
		t.Fatalf("realProcessStartedAt: %v", err)
	}
	for i, task := range tasks {
		sessionID := fmt.Sprintf("shared-live-run-%d", i)
		insertClaimLivenessSession(t, s, sessionID, project.ID, os.Getpid(), startedAt)
		insertClaimLivenessHandoff(t, s, task.ID, fmt.Sprintf("shared-live-handoff-%d", i), sessionID)
	}

	running, stale, err := ClaimLiveness(ctx, s, project.ID)
	if err != nil {
		t.Fatalf("ClaimLiveness: %v", err)
	}
	if len(running) != len(tasks) {
		t.Fatalf("running claims = %#v, want %d tasks", running, len(tasks))
	}
	if len(stale) != 0 {
		t.Fatalf("stale claims = %#v, want empty", stale)
	}
}

func TestClaimLivenessTreatsUnreadableProcessStartAsStale(t *testing.T) {
	saved := processStartedAt
	processStartedAt = func(pid int) (string, error) {
		return "", fmt.Errorf("process start time unavailable for pid %d", pid)
	}
	t.Cleanup(func() { processStartedAt = saved })

	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Unreadable process start", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "unreadable-process-start", []string{"Unreadable start"}, []string{"Treat an unreadable process start as stale."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	insertClaimLivenessSession(t, s, "unreadable-start-run", project.ID, os.Getpid(), "")
	insertClaimLivenessHandoff(t, s, tasks[0].ID, "unreadable-start-handoff", "unreadable-start-run")

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
	insertClaimLivenessHandoff(t, s, tasks[0].ID, "reused-pid-handoff", "reused-pid-run")

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
	insertClaimLivenessHandoff(t, s, tasks[0].ID, "other-project-handoff", nil)

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

func insertClaimLivenessHandoff(t *testing.T, s *Store, taskID, handoffID string, requestedBy any) {
	t.Helper()
	if _, err := s.DB().ExecContext(context.Background(), `
		INSERT INTO task_handoffs (id, task_id, requested_by, requested_at)
		VALUES (?, ?, ?, ?)`, handoffID, taskID, requestedBy, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert task handoff %s: %v", handoffID, err)
	}
}
