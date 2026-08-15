package daemonctl

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

const stopTimeout = 10 * time.Second

// Stop terminates the recorded daemon and clears its files. Finding no daemon
// is a normal outcome, not an error: `atct stop` twice in a row is something a
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
