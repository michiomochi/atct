package daemonctl

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The message here decides what the reader does next. A subcommander read
// "the recorded daemon process is alive but not answering" about a socket that
// had just answered, went looking for the process behind it, and killed its
// own executor and that executor's parent shell before trying to stop the
// daemon every other space shares.
func TestStopNamesAnUnrecordedDaemonWithoutBlamingAProcess(t *testing.T) {
	dir := t.TempDir()
	socket := SocketPath(dir)
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("skip: cannot listen on %s: %v", socket, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	err = clearStale(dir)
	if err == nil {
		t.Fatal("clearStale removed a socket that is still answering")
	}
	if errors.Is(err, ErrUnresponsive) {
		t.Errorf("a socket that answers was reported as an unanswering process: %v", err)
	}
	if !errors.Is(err, ErrUnrecorded) {
		t.Fatalf("error = %v, want ErrUnrecorded", err)
	}
	if !strings.Contains(err.Error(), "do not go looking for processes to kill") {
		t.Errorf("the message does not warn against a process hunt: %v", err)
	}
	if _, statErr := os.Stat(socket); statErr != nil {
		t.Errorf("the answering socket was removed anyway: %v", statErr)
	}
}
