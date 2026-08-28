package daemonctl

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	stopTimeout        = 10 * time.Second
	watchRegistryDir   = "watchers"
	watchActiveWarning = "atct watch is running"
)

// RegisterWatch records a project-wide watch for compatibility with callers
// that do not provide a scope.
func RegisterWatch(dir string) (func(), error) {
	return RegisterWatchScoped(dir, WatchScope{})
}

// HasActiveWatch reports whether a watch registration exists. It deliberately
// does not validate the recorded process: stale registrations only cause an
// extra warning and must not change daemon restart behavior.
func HasActiveWatch(dir string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(dir, watchRegistryDir))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read watch registry: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return true, nil
		}
	}
	return false, nil
}

// StopWithWatchWarning is the user-facing stop operation. The warning is
// advisory only; the daemon is stopped regardless of whether a watch exists.
func StopWithWatchWarning(cfg Config, stderr io.Writer) (bool, error) {
	registrations, err := ListWatches(cfg.Dir)
	if err != nil {
		return false, err
	}
	activeScopes := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		if !ProcessAlive(registration.PID) {
			continue
		}
		activeScopes = append(activeScopes, registration.ScopeLabel())
	}
	if len(activeScopes) > 0 {
		warning := fmt.Sprintf("%s (%d: %s), so the daemon will start again shortly. To keep it stopped, run /atct:stop first.", watchActiveWarning, len(activeScopes), strings.Join(activeScopes, ", "))
		if _, err := fmt.Fprintln(stderr, warning); err != nil {
			return false, fmt.Errorf("write watch warning: %w", err)
		}
	}
	return Stop(cfg)
}

// Stop terminates the recorded daemon and clears its files. Finding no daemon
// is a normal outcome, not an error: `atct daemon stop` twice in a row is something a
// user does, and the second call should say so plainly.
func Stop(cfg Config) (bool, error) {
	lock, err := AcquireLock(cfg.Dir, LockTimeout)
	if err != nil {
		return false, err
	}
	defer lock.Release()

	reg, err := ReadRegistry(cfg.Dir)
	if err != nil {
		if errors.Is(err, ErrNoRegistry) {
			return false, clearStale(cfg.Dir)
		}
		return false, err
	}

	if !ProcessAlive(reg.PID) {
		return false, clearStale(cfg.Dir)
	}

	p, err := os.FindProcess(reg.PID)
	if err != nil {
		return false, fmt.Errorf("find daemon %d: %w", reg.PID, err)
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return false, fmt.Errorf("signal daemon %d: %w", reg.PID, err)
	}

	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		if !ProcessAlive(reg.PID) {
			return true, clearStale(cfg.Dir)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false, fmt.Errorf("daemon %d did not exit within %s", reg.PID, stopTimeout)
}
