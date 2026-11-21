package daemonctl

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

var ErrLockTimeout = errors.New("timed out waiting for the daemon lock")

// Lock is an advisory file lock held for the duration of a start-or-reuse
// cycle. It is held across the liveness check as well as the start: checking
// outside the lock and starting inside it reintroduces the race it exists to
// prevent.
type Lock struct {
	file     *os.File
	released bool
}

// AcquireLock blocks until it holds the lock or the timeout elapses.
//
// flock is used rather than an O_EXCL lock file because flock is released
// automatically when the process dies. A crashed process holding an O_EXCL
// file would deadlock every future start.
func AcquireLock(dir string, timeout time.Duration) (*Lock, error) {
	f, err := os.OpenFile(LockPath(dir), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{file: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("flock: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w after %s", ErrLockTimeout, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (l *Lock) Release() error {
	if l == nil || l.released {
		return nil
	}
	l.released = true
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		l.file.Close()
		return fmt.Errorf("funlock: %w", err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close lock file: %w", err)
	}
	return nil
}
