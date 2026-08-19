package daemonctl

import (
	"bytes"
	"os"
	"strings"
	"testing"
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
	if !strings.Contains(stderr.String(), watchActiveWarning) {
		t.Fatalf("stop warning = %q, want %q", stderr.String(), watchActiveWarning)
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
