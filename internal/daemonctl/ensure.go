package daemonctl

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	// LockTimeout is shorter than StartTimeout because waiting on the lock
	// means another caller is already inside the start it protects.
	LockTimeout  = 5 * time.Second
	StartTimeout = 10 * time.Second
)

var (
	ErrVersionMismatch = errors.New("a daemon of a different version is already running")
	ErrStartTimeout    = errors.New("the daemon did not become ready in time")
	ErrUnresponsive    = errors.New("the recorded daemon process is alive but not answering")
)

type Config struct {
	Dir        string // the ~/.atct directory
	Version    string // this build's version, compared verbatim
	Executable string // the atct binary to start
	ListenAddr string // HTTP listen address passed to the daemon
}

// Ensure returns a healthy daemon, starting one only if none exists.
//
// The lock is held across the health check and the start. Checking outside it
// would let two callers both observe "no daemon" and both start one, and with
// a single fixed socket the loser's daemon is orphaned.
func Ensure(cfg Config) (Registry, error) {
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return Registry{}, fmt.Errorf("create %s: %w", cfg.Dir, err)
	}

	lock, err := AcquireLock(cfg.Dir, LockTimeout)
	if err != nil {
		return Registry{}, err
	}
	defer lock.Release()

	reg, err := ReadRegistry(cfg.Dir)
	switch {
	case err == nil:
		if reg.Healthy() {
			if reg.Version != cfg.Version {
				return Registry{}, fmt.Errorf(
					"%w: running %q, this build is %q; run `atct stop` first",
					ErrVersionMismatch, reg.Version, cfg.Version)
			}
			return reg, nil
		}
		if ProcessAlive(reg.PID) {
			return Registry{}, fmt.Errorf(
				"%w: pid %d holds no socket at %s; run `atct stop` or terminate it",
				ErrUnresponsive, reg.PID, reg.SocketPath)
		}
	case errors.Is(err, ErrNoRegistry):
		// Nothing recorded; fall through to start one.
	default:
		return Registry{}, err
	}

	if err := clearStale(cfg.Dir); err != nil {
		return Registry{}, err
	}
	return start(cfg)
}

func clearStale(dir string) error {
	if err := os.Remove(SocketPath(dir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return RemoveRegistry(dir)
}

func start(cfg Config) (Registry, error) {
	log, err := os.OpenFile(LogPath(cfg.Dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Registry{}, fmt.Errorf("open daemon log: %w", err)
	}
	defer log.Close()

	cmd := exec.Command(cfg.Executable, "daemon", "-listen", cfg.ListenAddr)
	cmd.Stdout = log
	cmd.Stderr = log
	// Setsid detaches the daemon from the caller's process group so it
	// outlives the shim or shell that started it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			searchedDir := filepath.Dir(os.Args[0])
			if executable, executableErr := os.Executable(); executableErr == nil {
				searchedDir = filepath.Dir(executable)
			}
			return Registry{}, fmt.Errorf(
				// No Homebrew tap exists yet, so naming one here would send the
				// user to a command that fails. Add it once the tap is published.
				"start daemon: %q was not found; searched for it in %s (beside atct-mcp) and in PATH. Put atct next to atct-mcp, or install it with `go install github.com/michiomochi/atct/cmd/atct@latest`: %w",
				cfg.Executable, searchedDir, err)
		}
		return Registry{}, fmt.Errorf("start daemon: %w", err)
	}
	// The daemon is detached; release the caller's handle on it so no zombie
	// remains if this process is long-lived.
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(StartTimeout)
	for time.Now().Before(deadline) {
		reg, err := ReadRegistry(cfg.Dir)
		if err == nil && reg.Healthy() {
			return reg, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return Registry{}, fmt.Errorf("%w after %s; see %s",
		ErrStartTimeout, StartTimeout, LogPath(cfg.Dir))
}
