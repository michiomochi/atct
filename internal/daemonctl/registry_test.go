package daemonctl

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// socketDir returns a temporary directory short enough to hold a Unix domain
// socket path. macOS caps sun_path at 104 bytes and t.TempDir() embeds the
// test function's name, which overflows that limit for the longer names in
// this package. Every test in internal/daemonctl that listens on a socket must
// use this instead of t.TempDir().
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestRegistryRoundTrip(t *testing.T) {
	dir := socketDir(t)
	want := Registry{
		PID:        4242,
		HTTPAddr:   "127.0.0.1:8787",
		SocketPath: filepath.Join(dir, "atct.sock"),
		Version:    "test-version",
		StartedAt:  "2026-08-15T00:00:00Z",
	}
	if err := WriteRegistry(dir, want); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	got, err := ReadRegistry(dir)
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestReadRegistryReportsMissingFile(t *testing.T) {
	if _, err := ReadRegistry(t.TempDir()); !errors.Is(err, ErrNoRegistry) {
		t.Fatalf("err = %v, want ErrNoRegistry", err)
	}
}

func TestRemoveRegistryIsIdempotent(t *testing.T) {
	dir := socketDir(t)
	if err := RemoveRegistry(dir); err != nil {
		t.Fatalf("RemoveRegistry on absent file: %v", err)
	}
	if err := WriteRegistry(dir, Registry{PID: 1}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	if err := RemoveRegistry(dir); err != nil {
		t.Fatalf("RemoveRegistry: %v", err)
	}
	if _, err := os.Stat(RegistryPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("registry still present after removal")
	}
}

func TestProcessAliveDetectsCurrentProcess(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("ProcessAlive(self) = false, want true")
	}
}

func TestProcessAliveRejectsUnusedPID(t *testing.T) {
	// PID 0 is never a normal user process on macOS or Linux.
	if ProcessAlive(0) {
		t.Fatal("ProcessAlive(0) = true, want false")
	}
}

func TestSocketAnswersDistinguishesListeningFromAbsent(t *testing.T) {
	dir := socketDir(t)
	sock := filepath.Join(dir, "atct.sock")

	if SocketAnswers(sock) {
		t.Fatal("SocketAnswers on absent socket = true, want false")
	}

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer l.Close()

	if !SocketAnswers(sock) {
		t.Fatal("SocketAnswers on listening socket = false, want true")
	}
}
