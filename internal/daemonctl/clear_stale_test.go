package daemonctl

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// shortTempDir keeps the socket path under the sun_path limit; t.TempDir() is
// already too long on macOS.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "atct")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// A daemon can lose its registry entry while it keeps serving. clearStale must
// not delete a socket that still answers: the owner keeps the HTTP port, so
// every later start fails to bind and the socket is never recreated.
func TestClearStaleKeepsAnsweringSocket(t *testing.T) {
	dir := shortTempDir(t)
	ln, err := net.Listen("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	err = clearStale(dir)
	if !errors.Is(err, ErrUnresponsive) {
		t.Fatalf("clearStale() error = %v, want ErrUnresponsive", err)
	}
	if _, statErr := os.Stat(SocketPath(dir)); statErr != nil {
		t.Fatalf("clearStale removed a socket that still answers: %v", statErr)
	}
}

func TestClearStaleRemovesDeadSocket(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(SocketPath(dir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearStale(dir); err != nil {
		t.Fatalf("clearStale() error = %v, want nil", err)
	}
	if _, err := os.Stat(SocketPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("clearStale kept a dead socket: %v", err)
	}
}

func TestClearStaleAcceptsMissingSocket(t *testing.T) {
	dir := t.TempDir()
	if err := clearStale(filepath.Clean(dir)); err != nil {
		t.Fatalf("clearStale() error = %v, want nil", err)
	}
}

// A daemon that outlives a newer one must not remove the newer one's registry.
func TestRegistryOwnedByRejectsForeignPID(t *testing.T) {
	dir := shortTempDir(t)
	reg := Registry{PID: os.Getpid(), SocketPath: SocketPath(dir), Version: "test"}
	if err := WriteRegistry(dir, reg); err != nil {
		t.Fatal(err)
	}
	if !RegistryOwnedBy(dir, os.Getpid()) {
		t.Fatal("RegistryOwnedBy = false for the recorded pid, want true")
	}
	if RegistryOwnedBy(dir, os.Getpid()+1) {
		t.Fatal("RegistryOwnedBy = true for a different pid, want false")
	}
}

func TestRegistryOwnedByRejectsMissingRegistry(t *testing.T) {
	if RegistryOwnedBy(shortTempDir(t), os.Getpid()) {
		t.Fatal("RegistryOwnedBy = true with no registry, want false")
	}
}
