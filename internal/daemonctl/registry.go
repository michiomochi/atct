// Package daemonctl manages the lifetime of the single per-user atct daemon.
//
// ATCT deliberately runs one daemon per user rather than one per working
// directory, because the inbox spans every namespace. That choice means a
// fixed socket path, which in turn means concurrent starts must be
// serialized; see lock.go.
package daemonctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrNoRegistry = errors.New("no daemon registry")

// Registry is the record a running daemon writes after it is listening.
type Registry struct {
	PID        int    `json:"pid"`
	HTTPAddr   string `json:"http_addr"`
	SocketPath string `json:"socket_path"`
	Version    string `json:"version"`
	StartedAt  string `json:"started_at"`
}

func RegistryPath(dir string) string { return filepath.Join(dir, "daemon.json") }
func SocketPath(dir string) string   { return filepath.Join(dir, "atct.sock") }
func LockPath(dir string) string     { return filepath.Join(dir, "daemon.lock") }
func LogPath(dir string) string      { return filepath.Join(dir, "daemon.log") }

func ReadRegistry(dir string) (Registry, error) {
	raw, err := os.ReadFile(RegistryPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return Registry{}, fmt.Errorf("%w: %s", ErrNoRegistry, RegistryPath(dir))
		}
		return Registry{}, fmt.Errorf("read registry: %w", err)
	}
	var r Registry
	if err := json.Unmarshal(raw, &r); err != nil {
		// A corrupt registry is treated as absent so that Ensure can repair it
		// rather than refusing to start forever.
		return Registry{}, fmt.Errorf("%w: %s is not valid JSON", ErrNoRegistry, RegistryPath(dir))
	}
	return r, nil
}

func WriteRegistry(dir string, r Registry) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	if err := os.WriteFile(RegistryPath(dir), append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

func RemoveRegistry(dir string) error {
	if err := os.Remove(RegistryPath(dir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove registry: %w", err)
	}
	return nil
}

// ProcessAlive reports whether a process with this PID exists. Signal 0
// performs the permission and existence checks without delivering a signal.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// SocketAnswers reports whether something accepts connections on the socket.
// A live PID is not sufficient evidence on its own: PIDs get recycled, so the
// number in the registry may belong to an unrelated process.
func SocketAnswers(path string) bool {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Healthy requires both the process and the socket. Either alone can lie.
func (r Registry) Healthy() bool {
	return ProcessAlive(r.PID) && SocketAnswers(r.SocketPath)
}
