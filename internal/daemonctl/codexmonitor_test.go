package daemonctl

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCodexMonitorRegistrationIsAtomicAndOwnCleanupIsScoped(t *testing.T) {
	dir := t.TempDir()
	record := CodexMonitorRecord{
		SupervisorPID: os.Getpid(),
		AppServerPID:  0,
		SocketPath:    filepath.Join(CodexMonitorRegistryDir(dir), "123.sock"),
		ProjectPath:   filepath.Join(dir, "project"),
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	cleanup, err := RegisterCodexMonitor(dir, record)
	if err != nil {
		t.Fatalf("RegisterCodexMonitor: %v", err)
	}
	other := record
	other.SupervisorPID++
	if _, err := RegisterCodexMonitor(dir, other); err != nil {
		t.Fatalf("RegisterCodexMonitor(other): %v", err)
	}

	entries, err := os.ReadDir(CodexMonitorRegistryDir(dir))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("registry entries = %d, want 2", len(entries))
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Fatalf("temporary registration file left behind: %q", entry.Name())
		}
	}

	cleanup()
	if _, err := os.Stat(CodexMonitorRecordPath(dir, record.SupervisorPID)); !os.IsNotExist(err) {
		t.Fatalf("own record stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(CodexMonitorRecordPath(dir, other.SupervisorPID)); err != nil {
		t.Fatalf("other record was removed: %v", err)
	}

	record.SocketPath = filepath.Join(dir, "outside.sock")
	if _, err := RegisterCodexMonitor(dir, record); err == nil {
		t.Fatal("RegisterCodexMonitor accepted socket outside managed directory")
	}
}

func TestCodexMonitorReapsDeadSupervisorAndItsAppServer(t *testing.T) {
	dir := t.TempDir()
	appServer := startCodexMonitorSleepProcess(t)
	record := CodexMonitorRecord{
		SupervisorPID: 2147483647,
		AppServerPID:  appServer.Process.Pid,
		SocketPath:    filepath.Join(CodexMonitorRegistryDir(dir), "dead.sock"),
		ProjectPath:   filepath.Join(dir, "project"),
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	registerCodexMonitorForTest(t, dir, record)
	if err := os.WriteFile(record.SocketPath, nil, 0o600); err != nil {
		t.Fatalf("write stale socket: %v", err)
	}

	result, err := ReapCodexMonitors(dir)
	if err != nil {
		t.Fatalf("ReapCodexMonitors: %v", err)
	}
	if result.RemovedStale != 1 || len(result.Stopped) != 1 {
		t.Fatalf("reap result = %#v, want one removed and one stopped", result)
	}
	if processAliveForCodexMonitorTest(appServer.Process.Pid) {
		t.Fatalf("app server process %d is still alive", appServer.Process.Pid)
	}
	if _, err := os.Stat(CodexMonitorRecordPath(dir, record.SupervisorPID)); !os.IsNotExist(err) {
		t.Fatalf("dead record stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(record.SocketPath); !os.IsNotExist(err) {
		t.Fatalf("stale socket stat error = %v, want not exist", err)
	}
}

func TestCodexMonitorReapKeepsLiveSupervisorAndOtherProject(t *testing.T) {
	dir := t.TempDir()
	appServer := startCodexMonitorSleepProcess(t)
	record := CodexMonitorRecord{
		SupervisorPID: os.Getpid(),
		AppServerPID:  appServer.Process.Pid,
		SocketPath:    filepath.Join(CodexMonitorRegistryDir(dir), "live.sock"),
		ProjectPath:   filepath.Join(dir, "project-a"),
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	registerCodexMonitorForTest(t, dir, record)

	result, err := ReapCodexMonitors(dir)
	if err != nil {
		t.Fatalf("ReapCodexMonitors: %v", err)
	}
	if result.RemovedStale != 0 || len(result.Stopped) != 0 || len(result.Failed) != 0 {
		t.Fatalf("reap result = %#v, want no changes", result)
	}
	if _, err := os.Stat(CodexMonitorRecordPath(dir, record.SupervisorPID)); err != nil {
		t.Fatalf("live record was removed: %v", err)
	}
	if !processAliveForCodexMonitorTest(appServer.Process.Pid) {
		t.Fatalf("app server process %d was stopped with live supervisor", appServer.Process.Pid)
	}
}

func TestCodexMonitorStopMatchesExactProject(t *testing.T) {
	dir := t.TempDir()
	first := startCodexMonitorSleepProcess(t)
	second := startCodexMonitorSleepProcess(t)
	firstRecord := CodexMonitorRecord{
		SupervisorPID: first.Process.Pid,
		SocketPath:    filepath.Join(CodexMonitorRegistryDir(dir), "first.sock"),
		ProjectPath:   filepath.Join(dir, "project"),
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	secondRecord := firstRecord
	secondRecord.SupervisorPID = second.Process.Pid
	secondRecord.SocketPath = filepath.Join(CodexMonitorRegistryDir(dir), "second.sock")
	secondRecord.ProjectPath = filepath.Join(dir, "project-child")
	registerCodexMonitorForTest(t, dir, firstRecord)
	registerCodexMonitorForTest(t, dir, secondRecord)
	if err := os.WriteFile(firstRecord.SocketPath, nil, 0o600); err != nil {
		t.Fatalf("write first socket: %v", err)
	}
	if err := os.WriteFile(secondRecord.SocketPath, nil, 0o600); err != nil {
		t.Fatalf("write second socket: %v", err)
	}

	result, err := StopCodexMonitorsForProject(dir, firstRecord.ProjectPath)
	if err != nil {
		t.Fatalf("StopCodexMonitorsForProject: %v", err)
	}
	if len(result.Stopped) != 1 || result.Stopped[0].SupervisorPID != firstRecord.SupervisorPID {
		t.Fatalf("stop result = %#v, want first project only", result)
	}
	if processAliveForCodexMonitorTest(first.Process.Pid) {
		t.Fatalf("first supervisor %d is still alive", first.Process.Pid)
	}
	if !processAliveForCodexMonitorTest(second.Process.Pid) {
		t.Fatalf("different-project supervisor %d was stopped", second.Process.Pid)
	}
	if _, err := os.Stat(CodexMonitorRecordPath(dir, secondRecord.SupervisorPID)); err != nil {
		t.Fatalf("different-project record was removed: %v", err)
	}
	if _, err := os.Stat(firstRecord.SocketPath); !os.IsNotExist(err) {
		t.Fatalf("first socket stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(secondRecord.SocketPath); err != nil {
		t.Fatalf("different-project socket was removed: %v", err)
	}
}

func TestCodexMonitorStopSkipsPIDWithMismatchedStartTime(t *testing.T) {
	dir := t.TempDir()
	process := startCodexMonitorSleepProcess(t)
	record := CodexMonitorRecord{
		SupervisorPID: process.Process.Pid,
		SocketPath:    filepath.Join(CodexMonitorRegistryDir(dir), "mismatch.sock"),
		ProjectPath:   filepath.Join(dir, "project"),
		StartedAt:     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
	}
	registerCodexMonitorForTest(t, dir, record)

	result, err := StopCodexMonitorsForProject(dir, record.ProjectPath)
	if err != nil {
		t.Fatalf("StopCodexMonitorsForProject: %v", err)
	}
	if len(result.Stopped) != 0 || len(result.Failed) != 1 || result.Failed[0] != record.SupervisorPID {
		t.Fatalf("stop result = %#v, want one failed mismatched PID", result)
	}
	if !processAliveForCodexMonitorTest(process.Process.Pid) {
		t.Fatalf("mismatched supervisor %d was stopped", process.Process.Pid)
	}
}

func TestCodexMonitorStopRetainsRecordWhenProcessDoesNotExit(t *testing.T) {
	dir := t.TempDir()
	process := startCodexMonitorIgnoringTermProcess(t)
	record := CodexMonitorRecord{
		SupervisorPID: process.Process.Pid,
		SocketPath:    filepath.Join(CodexMonitorRegistryDir(dir), "stuck.sock"),
		ProjectPath:   filepath.Join(dir, "project"),
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	registerCodexMonitorForTest(t, dir, record)

	previousTimeout := codexMonitorStopTimeout
	previousInterval := codexMonitorPollInterval
	codexMonitorStopTimeout = 50 * time.Millisecond
	codexMonitorPollInterval = time.Millisecond
	t.Cleanup(func() {
		codexMonitorStopTimeout = previousTimeout
		codexMonitorPollInterval = previousInterval
	})

	result, err := StopCodexMonitorsForProject(dir, record.ProjectPath)
	if err != nil {
		t.Fatalf("StopCodexMonitorsForProject: %v", err)
	}
	if len(result.Failed) != 1 || result.Failed[0] != record.SupervisorPID {
		t.Fatalf("stop result = %#v, want one failed PID", result)
	}
	if _, err := os.Stat(CodexMonitorRecordPath(dir, record.SupervisorPID)); err != nil {
		t.Fatalf("stuck record was removed: %v", err)
	}
}

func TestCodexMonitorReapRemovesMalformedRecord(t *testing.T) {
	dir := t.TempDir()
	path := CodexMonitorRecordPath(dir, 999999)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir registry: %v", err)
	}
	if err := os.WriteFile(path, []byte("not-json\n"), 0o600); err != nil {
		t.Fatalf("write malformed record: %v", err)
	}

	result, err := ReapCodexMonitors(dir)
	if err != nil {
		t.Fatalf("ReapCodexMonitors: %v", err)
	}
	if result.RemovedMalformed != 1 {
		t.Fatalf("removed malformed = %d, want 1", result.RemovedMalformed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("malformed record stat error = %v, want not exist", err)
	}
}

func TestCodexMonitorReapLeavesAtomicTemporaryRecord(t *testing.T) {
	dir := t.TempDir()
	registryDir := CodexMonitorRegistryDir(dir)
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		t.Fatalf("mkdir registry: %v", err)
	}
	temporaryPath := filepath.Join(registryDir, ".codex-monitor-in-progress.tmp")
	if err := os.WriteFile(temporaryPath, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write temporary record: %v", err)
	}

	result, err := ReapCodexMonitors(dir)
	if err != nil {
		t.Fatalf("ReapCodexMonitors: %v", err)
	}
	if result.RemovedMalformed != 0 {
		t.Fatalf("removed malformed = %d, want 0 for temporary record", result.RemovedMalformed)
	}
	if _, err := os.Stat(temporaryPath); err != nil {
		t.Fatalf("temporary record stat error = %v, want preserved", err)
	}
}

func TestCodexMonitorStopReportsOrphanCleanupFailure(t *testing.T) {
	dir := t.TempDir()
	process := startCodexMonitorIgnoringTermProcess(t)
	record := CodexMonitorRecord{
		SupervisorPID: 2147483647,
		AppServerPID:  process.Process.Pid,
		SocketPath:    filepath.Join(CodexMonitorRegistryDir(dir), "stuck-orphan.sock"),
		ProjectPath:   filepath.Join(dir, "project"),
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	registerCodexMonitorForTest(t, dir, record)

	previousTimeout := codexMonitorStopTimeout
	previousInterval := codexMonitorPollInterval
	codexMonitorStopTimeout = 50 * time.Millisecond
	codexMonitorPollInterval = time.Millisecond
	t.Cleanup(func() {
		codexMonitorStopTimeout = previousTimeout
		codexMonitorPollInterval = previousInterval
	})

	result, err := StopCodexMonitorsForProject(dir, record.ProjectPath)
	if err != nil {
		t.Fatalf("StopCodexMonitorsForProject: %v", err)
	}
	if len(result.Failed) != 1 || result.Failed[0] != record.SupervisorPID {
		t.Fatalf("stop result = %#v, want failed supervisor %d", result, record.SupervisorPID)
	}
	if _, err := os.Stat(CodexMonitorRecordPath(dir, record.SupervisorPID)); err != nil {
		t.Fatalf("orphan record stat error = %v, want retained", err)
	}
}

func registerCodexMonitorForTest(t *testing.T, dir string, record CodexMonitorRecord) {
	t.Helper()
	if _, err := RegisterCodexMonitor(dir, record); err != nil {
		t.Fatalf("RegisterCodexMonitor: %v", err)
	}
}

func startCodexMonitorSleepProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	process := exec.Command("sleep", "60")
	if err := process.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = process.Process, process.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		_ = process.Process.Signal(syscall.SIGTERM)
		<-done
	})
	return process
}

func startCodexMonitorIgnoringTermProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create readiness pipe: %v", err)
	}
	process := exec.Command("sh", "-c", "trap '' TERM; printf ready >&3; exec sleep 60")
	process.ExtraFiles = []*os.File{readyWriter}
	if err := process.Start(); err != nil {
		_ = readyReader.Close()
		_ = readyWriter.Close()
		t.Fatalf("start shell: %v", err)
	}
	_ = readyWriter.Close()
	var ready [len("ready")]byte
	if _, err := io.ReadFull(readyReader, ready[:]); err != nil {
		_ = readyReader.Close()
		_ = process.Process.Kill()
		t.Fatalf("wait for shell readiness: %v", err)
	}
	if string(ready[:]) != "ready" {
		_ = readyReader.Close()
		_ = process.Process.Kill()
		t.Fatalf("shell readiness = %q, want ready", ready[:])
	}
	_ = readyReader.Close()
	done := make(chan struct{})
	go func() {
		_, _ = process.Process, process.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		_ = process.Process.Kill()
		<-done
	})
	return process
}

func processAliveForCodexMonitorTest(pid int) bool {
	return ProcessAlive(pid)
}
