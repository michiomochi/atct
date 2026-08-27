package daemonctl

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStopWarnsForActiveWatchAndStillStopsDaemon(t *testing.T) {
	dir := socketDir(t)
	cfg := stubConfig(t, dir)

	if _, err := Ensure(cfg); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	cleanup, err := RegisterWatch(dir)
	if err != nil {
		t.Fatalf("RegisterWatch: %v", err)
	}
	defer cleanup()

	var stderr bytes.Buffer
	stopped, err := StopWithWatchWarning(cfg, &stderr)
	if err != nil {
		t.Fatalf("StopWithWatchWarning: %v", err)
	}
	if !stopped {
		t.Fatal("StopWithWatchWarning reported no daemon, want stopped = true")
	}
	wantWarning := watchActiveWarning + " (1: project-wide), so the daemon will start again shortly. To keep it stopped, run /atct:stop first.\n"
	if got := stderr.String(); got != wantWarning {
		t.Fatalf("stop warning = %q, want %q", got, wantWarning)
	}
}

func TestStopDoesNotWarnWithoutActiveWatch(t *testing.T) {
	dir := socketDir(t)
	cfg := stubConfig(t, dir)

	var stderr bytes.Buffer
	stopped, err := StopWithWatchWarning(cfg, &stderr)
	if err != nil {
		t.Fatalf("StopWithWatchWarning with no daemon: %v", err)
	}
	if stopped {
		t.Fatal("StopWithWatchWarning reported a daemon was stopped, want false")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stop warning without watch = %q, want empty", got)
	}
}

func TestStopTerminatesRunningDaemon(t *testing.T) {
	dir := socketDir(t)
	cfg := stubConfig(t, dir)

	reg, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	stopped, err := Stop(cfg)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !stopped {
		t.Fatal("Stop reported no daemon, want stopped = true")
	}
	if ProcessAlive(reg.PID) {
		t.Fatalf("daemon %d still alive after Stop", reg.PID)
	}
	if _, err := os.Stat(RegistryPath(dir)); !os.IsNotExist(err) {
		t.Fatal("registry survived Stop")
	}
	if _, err := os.Stat(SocketPath(dir)); !os.IsNotExist(err) {
		t.Fatal("socket file survived Stop")
	}
}

func TestStopWithNoDaemonIsNotAnError(t *testing.T) {
	dir := socketDir(t)
	cfg := stubConfig(t, dir)

	stopped, err := Stop(cfg)
	if err != nil {
		t.Fatalf("Stop with no daemon: %v", err)
	}
	if stopped {
		t.Fatal("Stop reported a daemon was stopped, want false")
	}
}

func TestStopIsIdempotent(t *testing.T) {
	dir := socketDir(t)
	cfg := stubConfig(t, dir)

	if _, err := Ensure(cfg); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := Stop(cfg); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if _, err := Stop(cfg); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestRegisterWatchWritesScopedJSON(t *testing.T) {
	dir := socketDir(t)

	cleanup, err := RegisterWatch(dir)
	if err != nil {
		t.Fatalf("RegisterWatch: %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(filepath.Join(dir, watchRegistryDir, strconv.Itoa(os.Getpid())))
	if err != nil {
		t.Fatalf("read watch registration: %v", err)
	}
	var registration map[string]any
	if err := json.Unmarshal(raw, &registration); err != nil {
		t.Fatalf("registration is not JSON: %v", err)
	}
	if got := registration["pid"]; got != float64(os.Getpid()) {
		t.Fatalf("registration pid = %v, want %d", got, os.Getpid())
	}
}

func TestReapWatchesRemovesDeadRegistration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, watchRegistryDir, "2147483647")
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:   2147483647,
		Scope: WatchScope{ProjectID: "1"},
	})

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 1 {
		t.Fatalf("removed stale = %d, want 1", result.RemovedStale)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale registration stat error = %v, want not exist", err)
	}
}

func TestReapWatchesStopsLiveDuplicateProcess(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	selfPath := filepath.Join(dir, watchRegistryDir, strconv.Itoa(os.Getpid()))
	writeWatchRegistrationFile(t, selfPath, WatchRegistration{
		PID:       os.Getpid(),
		Scope:     WatchScope{ProjectID: "1", GoalID: "180"},
		StartedAt: "2026-08-27T02:00:00Z",
	})
	defer os.Remove(selfPath)
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(cmd.Process.Pid))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:       cmd.Process.Pid,
		Scope:     WatchScope{ProjectID: "1", GoalID: "180"},
		StartedAt: "2026-08-27T01:00:00Z",
	})

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1", GoalID: "180"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if len(result.Stopped) != 1 || result.Stopped[0].PID != cmd.Process.Pid {
		t.Fatalf("stopped = %#v, want pid %d", result.Stopped, cmd.Process.Pid)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("failed pids = %#v, want empty", result.Failed)
	}
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		_ = cmd.Process.Signal(syscall.SIGTERM)
		t.Fatal("duplicate process did not exit")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("duplicate registration stat error = %v, want not exist", err)
	}
}

func TestReapWatchesKeepsNewerLiveDuplicateProcess(t *testing.T) {
	dir := t.TempDir()
	cmd, _ := startReapTestProcess(t)
	writeReapSelfRegistration(t, dir, os.Getpid(), WatchScope{ProjectID: "1", GoalID: "180"}, "2026-08-27T02:00:00Z")
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(cmd.Process.Pid))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:       cmd.Process.Pid,
		Scope:     WatchScope{ProjectID: "1", GoalID: "180"},
		StartedAt: "2026-08-27T00:30:00-03:00",
	})

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1", GoalID: "180"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no action for newer registration", result)
	}
	if !ProcessAlive(cmd.Process.Pid) {
		t.Fatal("newer duplicate process was stopped")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("newer duplicate registration was changed: %v", err)
	}
}

func TestReapWatchesStopsOlderLiveDuplicateProcess(t *testing.T) {
	dir := t.TempDir()
	cmd, waitDone := startReapTestProcess(t)
	writeReapSelfRegistration(t, dir, os.Getpid(), WatchScope{ProjectID: "1", GoalID: "180"}, "2026-08-27T02:00:00Z")
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(cmd.Process.Pid))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:       cmd.Process.Pid,
		Scope:     WatchScope{ProjectID: "1", GoalID: "180"},
		StartedAt: "2026-08-27T03:30:00+02:00",
	})

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1", GoalID: "180"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if len(result.Stopped) != 1 || result.Stopped[0].PID != cmd.Process.Pid {
		t.Fatalf("stopped = %#v, want pid %d", result.Stopped, cmd.Process.Pid)
	}
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("older duplicate process did not exit")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("older duplicate registration stat error = %v, want not exist", err)
	}
}

func TestReapWatchesTieBreaksEqualStartedAtByPID(t *testing.T) {
	tests := []struct {
		name           string
		selfFirst      bool
		wantSelfToStop bool
	}{
		{name: "self pid smaller", selfFirst: true, wantSelfToStop: false},
		{name: "self pid larger", selfFirst: false, wantSelfToStop: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var selfProcess, candidateProcess *exec.Cmd
			var candidateWait <-chan error
			if tc.selfFirst {
				selfProcess, _ = startReapTestProcess(t)
				candidateProcess, candidateWait = startReapTestProcess(t)
			} else {
				candidateProcess, candidateWait = startReapTestProcess(t)
				selfProcess, _ = startReapTestProcess(t)
			}
			startedAt := "2026-08-27T02:00:00Z"
			writeReapSelfRegistration(t, dir, selfProcess.Process.Pid, WatchScope{ProjectID: "1", GoalID: "180"}, startedAt)
			candidatePath := filepath.Join(dir, watchRegistryDir, strconv.Itoa(candidateProcess.Process.Pid))
			writeWatchRegistrationFile(t, candidatePath, WatchRegistration{
				PID:       candidateProcess.Process.Pid,
				Scope:     WatchScope{ProjectID: "1", GoalID: "180"},
				StartedAt: startedAt,
			})

			result, err := ReapWatches(dir, WatchScope{ProjectID: "1", GoalID: "180"}, selfProcess.Process.Pid)
			if err != nil {
				t.Fatalf("ReapWatches: %v", err)
			}
			if tc.wantSelfToStop {
				if len(result.Stopped) != 1 || result.Stopped[0].PID != candidateProcess.Process.Pid {
					t.Fatalf("stopped = %#v, want pid %d", result.Stopped, candidateProcess.Process.Pid)
				}
				select {
				case <-candidateWait:
				case <-time.After(time.Second):
					t.Fatal("lower-pid candidate process did not exit")
				}
				return
			}
			if len(result.Stopped) != 0 || len(result.Failed) != 0 {
				t.Fatalf("reap result = %#v, want no action for higher-pid candidate", result)
			}
			if _, err := os.Stat(candidatePath); err != nil {
				t.Fatalf("higher-pid candidate registration was changed: %v", err)
			}
		})
	}
}

func TestReapWatchesKeepsLiveDuplicateWithEmptyStartedAt(t *testing.T) {
	dir := t.TempDir()
	cmd, _ := startReapTestProcess(t)
	writeReapSelfRegistration(t, dir, os.Getpid(), WatchScope{ProjectID: "1", GoalID: "180"}, "2026-08-27T02:00:00Z")
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(cmd.Process.Pid))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:       cmd.Process.Pid,
		Scope:     WatchScope{ProjectID: "1", GoalID: "180"},
		StartedAt: "",
	})

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1", GoalID: "180"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no action for empty started_at", result)
	}
	if !ProcessAlive(cmd.Process.Pid) {
		t.Fatal("empty-started_at duplicate process was stopped")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("empty-started_at registration was changed: %v", err)
	}
}

func TestReapWatchesKeepsLiveDuplicateWhenSelfRegistrationMissing(t *testing.T) {
	dir := t.TempDir()
	cmd, _ := startReapTestProcess(t)
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(cmd.Process.Pid))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:       cmd.Process.Pid,
		Scope:     WatchScope{ProjectID: "1", GoalID: "180"},
		StartedAt: "2026-08-27T01:00:00Z",
	})

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1", GoalID: "180"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no action when self registration is missing", result)
	}
	if !ProcessAlive(cmd.Process.Pid) {
		t.Fatal("duplicate process was stopped without self registration")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("duplicate registration was changed: %v", err)
	}
}

func TestReapWatchesKeepsGoalRegistrationForProjectScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(os.Getpid()))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:   os.Getpid(),
		Scope: WatchScope{ProjectID: "1", GoalID: "180"},
	})
	defer os.Remove(path)

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1"}, -1)
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 0 || len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no action", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("goal registration was changed: %v", err)
	}
}

func TestReapWatchesKeepsDifferentProjectRegistration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(os.Getpid()))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:   os.Getpid(),
		Scope: WatchScope{ProjectID: "2"},
	})
	defer os.Remove(path)

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1"}, -1)
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 0 || len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no action", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("different-project registration was changed: %v", err)
	}
}

func TestReapWatchesKeepsSelfRegistration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(os.Getpid()))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:   os.Getpid(),
		Scope: WatchScope{ProjectID: "1"},
	})
	defer os.Remove(path)

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 0 || len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no action", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("self registration was changed: %v", err)
	}
}

func TestReapWatchesSkipsUnknownProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(os.Getpid()))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:   os.Getpid(),
		Scope: WatchScope{ProjectID: "1"},
	})
	defer os.Remove(path)

	result, err := ReapWatches(dir, WatchScope{}, os.Getpid()+1)
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 0 || len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no action", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unknown-project registration was changed: %v", err)
	}
}

func TestReapWatchesLeavesLegacyRegistration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir watch registry: %v", err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy registration: %v", err)
	}
	defer os.Remove(path)

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1"}, os.Getpid()+1)
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 0 || len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no action", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy registration was changed: %v", err)
	}
}

func TestListWatchesReadsLegacyAndScopedRegistrations(t *testing.T) {
	dir := t.TempDir()
	scopedPath := filepath.Join(dir, watchRegistryDir, "1001")
	legacyPath := filepath.Join(dir, watchRegistryDir, "1002")
	writeWatchRegistrationFile(t, scopedPath, WatchRegistration{
		PID:       os.Getpid(),
		Scope:     WatchScope{ProjectID: "1", GoalID: "180"},
		StartedAt: "2026-08-28T09:00:00Z",
	})
	if err := os.WriteFile(legacyPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy registration: %v", err)
	}

	registrations, err := ListWatches(dir)
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(registrations) != 2 {
		t.Fatalf("registrations = %#v, want 2 entries", registrations)
	}
	if registrations[0].Legacy || registrations[0].Scope != (WatchScope{ProjectID: "1", GoalID: "180"}) {
		t.Fatalf("scoped registration = %#v", registrations[0])
	}
	if !registrations[1].Legacy || registrations[1].PID != os.Getpid() {
		t.Fatalf("legacy registration = %#v", registrations[1])
	}
}

func TestStopWithWatchWarningListsLiveWatchScopes(t *testing.T) {
	dir := t.TempDir()
	writeWatchRegistrationFile(t, filepath.Join(dir, watchRegistryDir, "1001"), WatchRegistration{
		PID: os.Getpid(), Scope: WatchScope{ProjectID: "1"},
	})
	writeWatchRegistrationFile(t, filepath.Join(dir, watchRegistryDir, "1002"), WatchRegistration{
		PID: os.Getpid(), Scope: WatchScope{ProjectID: "1", GoalID: "180"},
	})
	writeWatchRegistrationFile(t, filepath.Join(dir, watchRegistryDir, "1003"), WatchRegistration{
		PID: os.Getpid(), Scope: WatchScope{ProjectID: "1", GoalID: "181"},
	})
	legacyPath := filepath.Join(dir, watchRegistryDir, "1004")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir watch registry: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy registration: %v", err)
	}

	var stderr bytes.Buffer
	if _, err := StopWithWatchWarning(Config{Dir: dir}, &stderr); err != nil {
		t.Fatalf("StopWithWatchWarning: %v", err)
	}
	got := stderr.String()
	for _, want := range []string{
		"atct watch is running (4:",
		"project-wide",
		"goal 180",
		"goal 181",
		"unknown",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning = %q, want %q", got, want)
		}
	}
}

func TestRegisterWatchScopedCleanupRemovesOnlyOwnRegistration(t *testing.T) {
	dir := t.TempDir()
	cleanup, err := RegisterWatchScoped(dir, WatchScope{ProjectID: "1", GoalID: "180"})
	if err != nil {
		t.Fatalf("RegisterWatchScoped: %v", err)
	}
	otherPath := filepath.Join(dir, watchRegistryDir, "9999")
	writeWatchRegistrationFile(t, otherPath, WatchRegistration{
		PID:   9999,
		Scope: WatchScope{ProjectID: "2"},
	})

	cleanup()
	ownPath := filepath.Join(dir, watchRegistryDir, strconv.Itoa(os.Getpid()))
	if _, err := os.Stat(ownPath); !os.IsNotExist(err) {
		t.Fatalf("own registration stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatalf("other registration was removed: %v", err)
	}
}

func TestReapWatchesHandlesMissingRegistryDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	result, err := ReapWatches(dir, WatchScope{ProjectID: "1"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 0 || len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want empty", result)
	}
}

func TestListWatchesSkipsUnreadableRegistration(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, watchRegistryDir, "999999")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("mkdir watch registry: %v", err)
	}
	if err := os.WriteFile(badPath, nil, 0o644); err != nil {
		t.Fatalf("write unreadable registration: %v", err)
	}
	writeWatchRegistrationFile(t, filepath.Join(dir, watchRegistryDir, "1001"), WatchRegistration{
		PID:   os.Getpid(),
		Scope: WatchScope{ProjectID: "1"},
	})

	registrations, err := ListWatches(dir)
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(registrations) != 1 || registrations[0].PID != os.Getpid() {
		t.Fatalf("registrations = %#v, want only readable registration", registrations)
	}
	if _, err := os.Stat(badPath); err != nil {
		t.Fatalf("unreadable registration was changed: %v", err)
	}
}

func TestReapWatchesSkipsUnreadableRegistration(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, watchRegistryDir, "999999")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("mkdir watch registry: %v", err)
	}
	if err := os.WriteFile(badPath, nil, 0o644); err != nil {
		t.Fatalf("write unreadable registration: %v", err)
	}
	deadPath := filepath.Join(dir, watchRegistryDir, "2147483647")
	writeWatchRegistrationFile(t, deadPath, WatchRegistration{
		PID:   2147483647,
		Scope: WatchScope{ProjectID: "1"},
	})

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 1 {
		t.Fatalf("removed stale = %d, want 1", result.RemovedStale)
	}
	if _, err := os.Stat(badPath); err != nil {
		t.Fatalf("unreadable registration was changed: %v", err)
	}
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Fatalf("dead registration stat error = %v, want not exist", err)
	}
}

func TestReapWatchesRemovesDeadLegacyRegistration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, watchRegistryDir, "2147483647")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir watch registry: %v", err)
	}
	if err := os.WriteFile(path, []byte("2147483647\n"), 0o644); err != nil {
		t.Fatalf("write legacy registration: %v", err)
	}

	result, err := ReapWatches(dir, WatchScope{ProjectID: "1"}, os.Getpid())
	if err != nil {
		t.Fatalf("ReapWatches: %v", err)
	}
	if result.RemovedStale != 1 {
		t.Fatalf("removed stale = %d, want 1", result.RemovedStale)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dead legacy registration stat error = %v, want not exist", err)
	}
}

func writeWatchRegistrationFile(t *testing.T, path string, registration WatchRegistration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir watch registry: %v", err)
	}
	raw, err := json.Marshal(registration)
	if err != nil {
		t.Fatalf("marshal watch registration: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write watch registration: %v", err)
	}
}

func startReapTestProcess(t *testing.T) (*exec.Cmd, <-chan error) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	waitDone := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		waitDone <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Errorf("test process %d did not exit", cmd.Process.Pid)
		}
	})
	return cmd, waitDone
}

func writeReapSelfRegistration(t *testing.T, dir string, pid int, scope WatchScope, startedAt string) {
	t.Helper()
	path := filepath.Join(dir, watchRegistryDir, strconv.Itoa(pid))
	writeWatchRegistrationFile(t, path, WatchRegistration{
		PID:       pid,
		Scope:     scope,
		StartedAt: startedAt,
	})
	t.Cleanup(func() { _ = os.Remove(path) })
}
