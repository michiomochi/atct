# ATCT Plugin Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ATCT install once and work, by having the MCP shim start the daemon itself, and by packaging that as a Claude Code plugin.

**Architecture:** A new `internal/daemonctl` package owns the daemon lifecycle: a registry file records the running daemon, a lock file serializes concurrent starts, and `Ensure` reuses a healthy daemon or starts exactly one. `atct` gains `ensure` and `stop` subcommands, and `atct-mcp` calls the same `Ensure` before serving. A Claude Code plugin declares the MCP server and ships a skill that teaches an agent when to use the eight tools.

**Tech Stack:** Go 1.26 / standard library only for lifecycle management (`os`, `os/exec`, `syscall`, `net`, `encoding/json`) / the standard `testing` package / GoReleaser for release artifacts.

**Spec:** `doc/specs/2026-08-15-atct-plugin-distribution.md`

## Global Constraints

- The Go module path is `github.com/michiomochi/atct`. The minimum Go version is `1.26.0`.
- Use only the standard `testing` package; do not add an assertion library.
- **Do not add any third-party dependency for locking, process management, or PID files.** The standard library covers all of it.
- **There is exactly one daemon per user.** Do not shard by working directory, branch, or session.
- The daemon does not stop when a session ends. Only `atct stop` stops it.
- The socket path stays fixed at `~/.atct/atct.sock`. Do not add a flag or environment variable for it.
- **`ensure` writes human-readable output to stderr, never stdout.** `atct-mcp` speaks the MCP protocol on stdout and a single stray line corrupts the session.
- Lock acquisition times out after **5 seconds**. Daemon readiness times out after **10 seconds**. Both are fixed constants; neither is configurable in v1.
- Version comparison is exact string equality, not semantic version ordering.
- Platform support is macOS and Linux. **Windows is out of scope** because the transport is a Unix domain socket.
- Do not modify `internal/store`, `internal/domain`, `internal/httpapi`, `internal/rpc`, `internal/mcpshim`, or `web/`. This plan adds lifecycle management around them.
- **Do not run `git push`, create a GitHub Release, create a Homebrew tap, or register a marketplace entry.** Those are separate authorized actions outside this plan.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/daemonctl/registry.go` | The `~/.atct/daemon.json` record and its liveness checks |
| `internal/daemonctl/lock.go` | The `~/.atct/daemon.lock` mutual exclusion |
| `internal/daemonctl/ensure.go` | Start-or-reuse, and the errors it can return |
| `internal/daemonctl/stop.go` | Explicit shutdown |
| `cmd/atct/main.go` | `daemon`, `ensure`, and `stop` subcommands; writes the registry once listening |
| `cmd/atct-mcp/main.go` | Calls `Ensure` before serving MCP |
| `.claude-plugin/plugin.json` | Plugin identity |
| `.mcp.json` | MCP server declaration |
| `skills/atct/SKILL.md` | Teaches an agent when to use the eight tools |
| `.goreleaser.yaml` | Release artifacts for macOS and Linux |

---

### Task 1: The daemon registry and its liveness checks

**Files:**
- Create: `internal/daemonctl/registry.go`
- Test: `internal/daemonctl/registry_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `daemonctl.Registry` struct with fields `PID int`, `HTTPAddr string`, `SocketPath string`, `Version string`, `StartedAt string`; `daemonctl.RegistryPath(dir string) string`; `daemonctl.SocketPath(dir string) string`; `daemonctl.LockPath(dir string) string`; `daemonctl.ReadRegistry(dir string) (Registry, error)`; `daemonctl.WriteRegistry(dir string, r Registry) error`; `daemonctl.RemoveRegistry(dir string) error`; `daemonctl.ErrNoRegistry`; `daemonctl.ProcessAlive(pid int) bool`; `daemonctl.SocketAnswers(path string) bool`

- [ ] **Step 1: Write the failing test**

`internal/daemonctl/registry_test.go`:

```go
package daemonctl

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
```

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./internal/daemonctl/ -v`
Expected: FAIL (the package does not exist yet)

- [ ] **Step 3: Write the implementation**

`internal/daemonctl/registry.go`:

```go
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
```

- [ ] **Step 4: Confirm that the tests pass**

Run: `go test ./internal/daemonctl/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemonctl/registry.go internal/daemonctl/registry_test.go
git commit -m "feat(daemonctl): add daemon registry and liveness checks"
```

---

### Task 2: Mutual exclusion for concurrent starts

**Files:**
- Create: `internal/daemonctl/lock.go`
- Test: `internal/daemonctl/lock_test.go`

**Interfaces:**
- Consumes: `daemonctl.LockPath`
- Produces: `daemonctl.AcquireLock(dir string, timeout time.Duration) (*Lock, error)`; `(*Lock) Release() error`; `daemonctl.ErrLockTimeout`

- [ ] **Step 1: Write the failing test**

`internal/daemonctl/lock_test.go`:

```go
package daemonctl

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAcquireLockExcludesSecondHolder(t *testing.T) {
	dir := t.TempDir()

	first, err := AcquireLock(dir, time.Second)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}

	_, err = AcquireLock(dir, 200*time.Millisecond)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("second AcquireLock err = %v, want ErrLockTimeout", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := AcquireLock(dir, time.Second)
	if err != nil {
		t.Fatalf("AcquireLock after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release second: %v", err)
	}
}

func TestAcquireLockSerializesConcurrentHolders(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var concurrent, maxConcurrent int

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := AcquireLock(dir, 5*time.Second)
			if err != nil {
				t.Errorf("AcquireLock: %v", err)
				return
			}
			mu.Lock()
			concurrent++
			if concurrent > maxConcurrent {
				maxConcurrent = concurrent
			}
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			concurrent--
			mu.Unlock()
			if err := l.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		}()
	}
	wg.Wait()

	if maxConcurrent != 1 {
		t.Fatalf("max concurrent lock holders = %d, want 1", maxConcurrent)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, err := AcquireLock(dir, time.Second)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}
```

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./internal/daemonctl/ -run TestAcquireLock -v`
Expected: FAIL (`undefined: AcquireLock`)

- [ ] **Step 3: Write the implementation**

`internal/daemonctl/lock.go`:

```go
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
```

- [ ] **Step 4: Confirm that the tests pass**

Run: `go test ./internal/daemonctl/ -race -count=5 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemonctl/lock.go internal/daemonctl/lock_test.go
git commit -m "feat(daemonctl): serialize concurrent daemon starts with a file lock"
```

---

### Task 3: Start-or-reuse

**Files:**
- Create: `internal/daemonctl/ensure.go`
- Test: `internal/daemonctl/ensure_test.go`

**Interfaces:**
- Consumes: `daemonctl.Registry`, `daemonctl.AcquireLock`, `daemonctl.ReadRegistry`, `daemonctl.RemoveRegistry`, `daemonctl.SocketAnswers`, `daemonctl.ProcessAlive`
- Produces: `daemonctl.Config` struct with fields `Dir string`, `Version string`, `Executable string`, `ListenAddr string`; `daemonctl.Ensure(cfg Config) (Registry, error)`; `daemonctl.ErrVersionMismatch`; `daemonctl.ErrStartTimeout`; `daemonctl.ErrUnresponsive`; constants `LockTimeout` and `StartTimeout`

- [ ] **Step 1: Write the failing test**

The test starts a real child process, so it uses the standard Go technique of
re-executing the test binary itself under an environment flag. `TestMain`
turns the binary into a stub daemon when `ATCT_TEST_STUB_DAEMON` is set.

`internal/daemonctl/ensure_test.go`:

```go
package daemonctl

import (
	"errors"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestMain lets this test binary impersonate `atct daemon`. Ensure starts
// whatever Config.Executable points at, so pointing it at os.Args[0] with
// this variable set gives a real child process without building a fixture.
func TestMain(m *testing.M) {
	if dir := os.Getenv("ATCT_TEST_STUB_DAEMON"); dir != "" {
		runStubDaemon(dir)
		return
	}
	os.Exit(m.Run())
}

func runStubDaemon(dir string) {
	l, err := net.Listen("unix", SocketPath(dir))
	if err != nil {
		os.Exit(1)
	}
	defer l.Close()

	reg := Registry{
		PID:        os.Getpid(),
		HTTPAddr:   "127.0.0.1:8787",
		SocketPath: SocketPath(dir),
		Version:    os.Getenv("ATCT_TEST_STUB_VERSION"),
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteRegistry(dir, reg); err != nil {
		os.Exit(1)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	<-stop
	os.Exit(0)
}

func stubConfig(t *testing.T, dir string) Config {
	t.Helper()
	t.Setenv("ATCT_TEST_STUB_DAEMON", dir)
	t.Setenv("ATCT_TEST_STUB_VERSION", "v-test")
	return Config{
		Dir:        dir,
		Version:    "v-test",
		Executable: os.Args[0],
		ListenAddr: "127.0.0.1:8787",
	}
}

func stopDaemon(t *testing.T, reg Registry) {
	t.Helper()
	if reg.PID == 0 {
		return
	}
	p, err := os.FindProcess(reg.PID)
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGTERM)
	for i := 0; i < 100; i++ {
		if !ProcessAlive(reg.PID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stub daemon %d did not exit", reg.PID)
}

func TestEnsureStartsDaemonWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := stubConfig(t, dir)

	reg, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	defer stopDaemon(t, reg)

	if reg.PID == 0 {
		t.Fatalf("registry has no PID: %+v", reg)
	}
	if !reg.Healthy() {
		t.Fatalf("daemon is not healthy after Ensure: %+v", reg)
	}
}

func TestEnsureReusesHealthyDaemon(t *testing.T) {
	dir := t.TempDir()
	cfg := stubConfig(t, dir)

	first, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	defer stopDaemon(t, first)

	second, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if second.PID != first.PID {
		t.Fatalf("Ensure started a second daemon: %d then %d", first.PID, second.PID)
	}
}

func TestEnsureRepairsStaleRegistry(t *testing.T) {
	dir := t.TempDir()
	cfg := stubConfig(t, dir)

	stale := Registry{
		PID:        0,
		SocketPath: SocketPath(dir),
		Version:    "v-test",
	}
	if err := WriteRegistry(dir, stale); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	if err := os.WriteFile(SocketPath(dir), []byte("not a socket"), 0o644); err != nil {
		t.Fatalf("write stale socket file: %v", err)
	}

	reg, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	defer stopDaemon(t, reg)

	if reg.PID == 0 {
		t.Fatalf("stale registry was not repaired: %+v", reg)
	}
}

func TestConcurrentEnsureStartsExactlyOneDaemon(t *testing.T) {
	dir := t.TempDir()
	cfg := stubConfig(t, dir)

	var wg sync.WaitGroup
	results := make(chan Registry, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg, err := Ensure(cfg)
			if err != nil {
				t.Errorf("Ensure: %v", err)
				return
			}
			results <- reg
		}()
	}
	wg.Wait()
	close(results)

	pids := map[int]bool{}
	var any Registry
	for reg := range results {
		pids[reg.PID] = true
		any = reg
	}
	defer stopDaemon(t, any)

	if len(pids) != 1 {
		t.Fatalf("concurrent Ensure produced %d distinct daemons, want 1: %v", len(pids), pids)
	}
}

func TestEnsureReportsVersionMismatchWithoutRestarting(t *testing.T) {
	dir := t.TempDir()
	cfg := stubConfig(t, dir)

	reg, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	defer stopDaemon(t, reg)

	other := cfg
	other.Version = "v-different"
	if _, err := Ensure(other); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
	if !ProcessAlive(reg.PID) {
		t.Fatal("version mismatch killed the running daemon")
	}
}

func TestEnsureReportsAlivePIDWithSilentSocket(t *testing.T) {
	dir := t.TempDir()
	cfg := stubConfig(t, dir)
	cfg.Executable = filepath.Join(dir, "does-not-exist")

	// The current test process is alive but is not listening on the socket.
	if err := WriteRegistry(dir, Registry{
		PID:        os.Getpid(),
		SocketPath: SocketPath(dir),
		Version:    "v-test",
	}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	_, err := Ensure(cfg)
	if !errors.Is(err, ErrUnresponsive) {
		t.Fatalf("err = %v, want ErrUnresponsive", err)
	}
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("the test process was signalled")
	}
}
```

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./internal/daemonctl/ -run TestEnsure -v`
Expected: FAIL (`undefined: Ensure`)

- [ ] **Step 3: Write the implementation**

`internal/daemonctl/ensure.go`:

```go
package daemonctl

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
```

- [ ] **Step 4: Confirm that the tests pass**

Run: `go test ./internal/daemonctl/ -race -count=3 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemonctl/ensure.go internal/daemonctl/ensure_test.go
git commit -m "feat(daemonctl): start or reuse exactly one daemon"
```

---

### Task 4: Explicit shutdown

**Files:**
- Create: `internal/daemonctl/stop.go`
- Test: `internal/daemonctl/stop_test.go`

**Interfaces:**
- Consumes: `daemonctl.Config`, `daemonctl.ReadRegistry`, `daemonctl.ProcessAlive`
- Produces: `daemonctl.Stop(cfg Config) (bool, error)`; the boolean reports whether a daemon was actually stopped

- [ ] **Step 1: Write the failing test**

`internal/daemonctl/stop_test.go`:

```go
package daemonctl

import (
	"os"
	"testing"
)

func TestStopTerminatesRunningDaemon(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
```

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./internal/daemonctl/ -run TestStop -v`
Expected: FAIL (`undefined: Stop`)

- [ ] **Step 3: Write the implementation**

`internal/daemonctl/stop.go`:

```go
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
```

- [ ] **Step 4: Confirm that the tests pass**

Run: `go test ./internal/daemonctl/ -race -count=3 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemonctl/stop.go internal/daemonctl/stop_test.go
git commit -m "feat(daemonctl): stop the daemon and clear its files"
```

---

### Task 5: Wire the subcommands and write the registry from the daemon

**Files:**
- Modify: `cmd/atct/main.go`
- Modify: `cmd/atct/main_test.go`

**Interfaces:**
- Consumes: `daemonctl.Ensure`, `daemonctl.Stop`, `daemonctl.Config`, `daemonctl.WriteRegistry`, `daemonctl.RemoveRegistry`
- Produces: the `ensure` and `stop` subcommands; a package-level `version` variable overridable with `-ldflags`; the daemon writing `~/.atct/daemon.json` after it is listening

- [ ] **Step 1: Extend the argument-parsing test**

Append to `cmd/atct/main_test.go`. Match the existing test's naming for the
parse function and config type; this plan does not rename them.

```go
func TestParseArgsAcceptsEnsure(t *testing.T) {
	cfg, err := parseArgs([]string{"ensure"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "ensure" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "ensure")
	}
}

func TestParseArgsAcceptsStop(t *testing.T) {
	cfg, err := parseArgs([]string{"stop"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "stop" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "stop")
	}
}

func TestParseArgsAcceptsListenOnEnsure(t *testing.T) {
	cfg, err := parseArgs([]string{"ensure", "-listen", "127.0.0.1:19999"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.listenAddr != "127.0.0.1:19999" {
		t.Fatalf("listenAddr = %q, want %q", cfg.listenAddr, "127.0.0.1:19999")
	}
}
```

If the existing `cliConfig` has no `subcommand` field, add one and keep the
existing fields unchanged.

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./cmd/atct/ -run TestParseArgs -v`
Expected: FAIL (`ensure` is rejected as an unknown subcommand)

- [ ] **Step 3: Accept the new subcommands**

Replace the existing `cliConfig`, `printUsage`, and `parseArgs` with these. The
current versions accept only `daemon` and record no subcommand.

```go
type cliConfig struct {
	subcommand string
	listenAddr string
}

var validSubcommands = map[string]bool{"daemon": true, "ensure": true, "stop": true}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: atct <command> [options]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  daemon    Run the ATCT daemon in the foreground")
	fmt.Fprintln(os.Stderr, "  ensure    Start the daemon if it is not already running")
	fmt.Fprintln(os.Stderr, "  stop      Stop the running daemon")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -listen string   HTTP listen address (default \"127.0.0.1:8787\")")
}

func parseArgs(args []string) (cliConfig, error) {
	if len(args) < 1 {
		printUsage()
		return cliConfig{}, errInvalidArgs
	}
	sub := args[0]
	if !validSubcommands[sub] {
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", sub)
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	flags := flag.NewFlagSet(sub, flag.ExitOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printUsage
	listenAddr := flags.String("listen", defaultListenAddr, "HTTP listen address")
	flags.Parse(args[1:])
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flags.Args()[0])
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	return cliConfig{subcommand: sub, listenAddr: *listenAddr}, nil
}
```

The existing tests for `daemon`, for a missing subcommand, and for an unknown
subcommand must keep passing unchanged. If any of them assert on the exact
usage text, update that assertion rather than reverting the text.

- [ ] **Step 4: Add a package-level version**

```go
// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"
```

- [ ] **Step 5: Write the registry from the daemon**

In the `daemon` path, after `httpServer.ListenAndServe` is running and the
Unix socket is being served, write the registry:

```go
if err := daemonctl.WriteRegistry(dir, daemonctl.Registry{
	PID:        os.Getpid(),
	HTTPAddr:   cfg.listenAddr,
	SocketPath: sock,
	Version:    version,
	StartedAt:  time.Now().UTC().Format(time.RFC3339),
}); err != nil {
	log.Fatalf("write registry: %v", err)
}
```

Remove the registry and the socket file during shutdown, in the same place the
existing signal handler shuts the HTTP server down. **The daemon writes the
registry itself** — a caller cannot honestly record readiness it has not
observed.

- [ ] **Step 6: Route `ensure` and `stop`**

```go
switch cfg.subcommand {
case "ensure":
	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir: dir, Version: version, Executable: exePath, ListenAddr: cfg.listenAddr,
	})
	if err != nil {
		log.Fatalf("ensure: %v", err)
	}
	fmt.Fprintf(os.Stderr, "atct daemon ready: pid %d, http %s\n", reg.PID, reg.HTTPAddr)
	return
case "stop":
	stopped, err := daemonctl.Stop(daemonctl.Config{Dir: dir, Version: version})
	if err != nil {
		log.Fatalf("stop: %v", err)
	}
	if stopped {
		fmt.Fprintln(os.Stderr, "atct daemon stopped")
	} else {
		fmt.Fprintln(os.Stderr, "no atct daemon was running")
	}
	return
}
```

Resolve `exePath` with `os.Executable()`. **All of this output goes to stderr**,
per the global constraints.

- [ ] **Step 7: Verify**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add cmd/atct/
git commit -m "feat(cli): add ensure and stop subcommands"
```

---

### Task 6: The MCP shim starts the daemon

**Files:**
- Modify: `cmd/atct-mcp/main.go`
- Test: `cmd/atct-mcp/main_test.go`

**Interfaces:**
- Consumes: `daemonctl.Ensure`, `daemonctl.Config`
- Produces: `atct-mcp` calling `Ensure` before serving; a `resolveAtctPath()` helper that finds the `atct` binary

- [ ] **Step 1: Write the failing test**

`cmd/atct-mcp/main_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAtctPathPrefersSiblingBinary(t *testing.T) {
	dir := t.TempDir()
	sibling := filepath.Join(dir, "atct")
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write sibling: %v", err)
	}

	got := resolveAtctPath(filepath.Join(dir, "atct-mcp"))
	if got != sibling {
		t.Fatalf("resolveAtctPath = %q, want %q", got, sibling)
	}
}

func TestResolveAtctPathFallsBackToBareName(t *testing.T) {
	got := resolveAtctPath(filepath.Join(t.TempDir(), "atct-mcp"))
	if got != "atct" {
		t.Fatalf("resolveAtctPath = %q, want %q for PATH lookup", got, "atct")
	}
}
```

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./cmd/atct-mcp/ -v`
Expected: FAIL (`undefined: resolveAtctPath`)

- [ ] **Step 3: Write the resolver**

```go
// resolveAtctPath prefers an atct binary installed next to this one, so a
// Homebrew install and a `go install` build do not get mixed. Falling back to
// the bare name defers to PATH.
func resolveAtctPath(self string) string {
	candidate := filepath.Join(filepath.Dir(self), "atct")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return "atct"
}
```

- [ ] **Step 4: Call Ensure before serving**

In `main`, before the MCP server is created:

```go
self, err := os.Executable()
if err != nil {
	self = os.Args[0]
}
if _, err := daemonctl.Ensure(daemonctl.Config{
	Dir:        filepath.Join(home, ".atct"),
	Version:    version,
	Executable: resolveAtctPath(self),
	ListenAddr: defaultListenAddr,
}); err != nil {
	// Stderr only. Stdout carries the MCP protocol.
	fmt.Fprintf(os.Stderr, "atct-mcp: %v\n", err)
	os.Exit(1)
}
```

Add the same `var version = "dev"` here so both binaries report the same value
under identical `-ldflags`, and define `defaultListenAddr = "127.0.0.1:8787"`
so the two binaries agree. **Nothing may be written to stdout before the MCP
handshake.**

- [ ] **Step 5: Verify**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: PASS

- [ ] **Step 6: Verify by hand that stdout stays clean**

```bash
go build -o /tmp/atct-mcp ./cmd/atct-mcp
ATCT_HOME_PROBE=1 /tmp/atct-mcp < /dev/null > /tmp/mcp-stdout.txt 2> /tmp/mcp-stderr.txt
wc -c /tmp/mcp-stdout.txt   # informational; any bytes must be MCP protocol only
cat /tmp/mcp-stderr.txt
```

Then stop anything it started: `go run ./cmd/atct stop`.

- [ ] **Step 7: Commit**

```bash
git add cmd/atct-mcp/
git commit -m "feat(mcp): start the daemon before serving"
```

---

### Task 7: The Claude Code plugin

**Files:**
- Create: `.claude-plugin/plugin.json`
- Create: `.mcp.json`
- Create: `skills/atct/SKILL.md`

**Interfaces:**
- Consumes: the `atct-mcp` binary on `PATH`
- Produces: a plugin directory loadable with `claude --plugin-dir .`

- [ ] **Step 1: Write the manifest**

`.claude-plugin/plugin.json`:

```json
{
  "name": "atct",
  "description": "Declare tasks, claim work, and ask a human for decisions without leaving the session.",
  "version": "0.1.0"
}
```

- [ ] **Step 2: Write the MCP declaration**

`.mcp.json`:

```json
{
  "mcpServers": {
    "atct": {
      "command": "atct-mcp",
      "args": []
    }
  }
}
```

The binary comes from Homebrew or `go install`, not from the plugin, so this
does not reference `${CLAUDE_PLUGIN_ROOT}`.

- [ ] **Step 3: Write the skill**

`skills/atct/SKILL.md`:

```markdown
---
name: atct
description: Use when working on a goal that a human is tracking - declaring the tasks you plan to do, claiming one before you start, and asking the human for a decision instead of guessing. Also use when you are about to finish and need approval.
---

# ATCT

ATCT records what you are working on and routes your questions to a human's
inbox. Registering tools is not enough; the value comes from calling them at
the right moments.

## Declare before you work

Call `atct_task_declare` with the tasks you intend to do, before doing them.
Pass a stable `idempotency_key` for the batch. Re-declaring the same batch does
not create duplicates, so it is safe after a retry or a context compaction.

## Claim before you start

Call `atct_task_claim` before working on a task. Exactly one run wins a claim.
If the claim fails the task is already owned, so pick another one rather than
working on it anyway.

Release a task by setting it back to `todo` with `atct_task_update`. There is
no separate release tool.

## Ask instead of guessing

Call `atct_decision_ask` when a choice would change the shape of the work and
you cannot settle it from the code. Supply options with a label, a description,
and the consequence of choosing it. `wait_ms` blocks for an answer and parks
when none arrives, so asking does not force you to stall.

Do not ask about things you can determine yourself. An inbox full of trivia
stops being read.

## Apply what you were told

Answers reach you through `atct_decision_poll`. Polling marks the decision
applied, which is how the human can tell their answer landed rather than
hanging. Poll before continuing work that depended on the question.

If a question stopped being relevant, call `atct_decision_withdraw` rather than
leaving it open.

## Finishing

A task cannot become `done` while a decision on it is open. Answer it or
withdraw it first.

Call `atct_goal_complete` when the work is done. It creates a completion
decision for the human to approve or reject; approval closes the goal, and
rejection returns a reason for you to act on.
```

- [ ] **Step 4: Verify the JSON parses**

```bash
python3 -m json.tool .claude-plugin/plugin.json > /dev/null && echo "plugin.json OK"
python3 -m json.tool .mcp.json > /dev/null && echo "mcp.json OK"
```

- [ ] **Step 5: Load the plugin locally**

```bash
claude --plugin-dir . -p "list the atct tools you can see" 2>&1 | head -30
```

Confirm the eight `atct_*` tools appear. Then stop the daemon it started:
`go run ./cmd/atct stop`.

- [ ] **Step 6: Commit**

```bash
git add .claude-plugin/ .mcp.json skills/
git commit -m "feat(plugin): add the Claude Code plugin and agent skill"
```

---

### Task 8: Release configuration

**Files:**
- Create: `.goreleaser.yaml`

**Interfaces:**
- Consumes: the `version` variables in both `main` packages
- Produces: a validated GoReleaser configuration; **no published release**

- [ ] **Step 1: Write the configuration**

`.goreleaser.yaml`:

```yaml
version: 2

builds:
  - id: atct
    main: ./cmd/atct
    binary: atct
    env:
      - CGO_ENABLED=0
    goos: [darwin, linux]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w -X main.version={{.Version}}
  - id: atct-mcp
    main: ./cmd/atct-mcp
    binary: atct-mcp
    env:
      - CGO_ENABLED=0
    goos: [darwin, linux]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w -X main.version={{.Version}}

archives:
  - formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"

checksum:
  name_template: "checksums.txt"

release:
  draft: true
```

`CGO_ENABLED=0` is safe because the SQLite driver is `modernc.org/sqlite`,
which is pure Go. Windows is absent deliberately: the transport is a Unix
domain socket.

- [ ] **Step 2: Validate without releasing**

```bash
goreleaser check
goreleaser build --snapshot --clean --single-target
```

If `goreleaser` is not installed, record that and skip to Step 4. **Do not
install it system-wide as part of this task.**

- [ ] **Step 3: Confirm the version is embedded**

```bash
./dist/atct_*/atct --help 2>&1 | head -3
```

- [ ] **Step 4: Ignore the build output**

Add `dist/` to `.gitignore`.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml .gitignore
git commit -m "build: add goreleaser configuration for macOS and Linux"
```

**Do not run `goreleaser release`.** Publishing is a separate authorized action.

---

## Verification for the whole plan

```bash
go build ./...
go vet ./...
go test ./... -race -count=3
```

Then confirm the success criteria from the spec by hand:

```bash
# 1. From nothing, the shim brings up exactly one daemon
go run ./cmd/atct stop
go build -o /tmp/atct-mcp ./cmd/atct-mcp && /tmp/atct-mcp < /dev/null &
sleep 3
cat ~/.atct/daemon.json
pgrep -fl 'atct daemon' | wc -l    # exactly 1

# 2. A second shim reuses it
/tmp/atct-mcp < /dev/null &
sleep 2
pgrep -fl 'atct daemon' | wc -l    # still exactly 1

# 3. Cleanup
go run ./cmd/atct stop
pgrep -fl 'atct daemon' | wc -l    # 0
```
