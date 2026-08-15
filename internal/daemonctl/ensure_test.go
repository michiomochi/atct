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
	dir := socketDir(t)
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
	dir := socketDir(t)
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
	dir := socketDir(t)
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
	dir := socketDir(t)
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
	dir := socketDir(t)
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
	dir := socketDir(t)
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
