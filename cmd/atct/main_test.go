package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/michiomochi/atct/internal/daemonctl"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantListen   string
		wantExplicit bool
		wantErr      bool
	}{
		{
			name:         "daemon parses listen flag after subcommand",
			args:         []string{"daemon", "-listen", "127.0.0.1:18787"},
			wantListen:   "127.0.0.1:18787",
			wantExplicit: true,
		},
		{
			name:       "daemon uses loopback default",
			args:       []string{"daemon"},
			wantListen: defaultListenAddr,
		},
		{
			name:    "missing subcommand is rejected",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "unknown subcommand is rejected",
			args:    []string{"serve"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseArgs(%q) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.listenAddr != tt.wantListen {
				t.Fatalf("parseArgs(%q) listenAddr = %q, want %q", tt.args, got.listenAddr, tt.wantListen)
			}
			if got.listenExplicit != tt.wantExplicit {
				t.Fatalf("parseArgs(%q) listenExplicit = %v, want %v", tt.args, got.listenExplicit, tt.wantExplicit)
			}
		})
	}
}

func TestListenHTTPFallsBackToNextDefaultPort(t *testing.T) {
	listenTestTCP(t, defaultListenAddr)

	listener, err := listenHTTP(defaultListenAddr, false)
	if err != nil {
		t.Fatalf("listenHTTP() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	if got, want := listener.Addr().String(), "127.0.0.1:8788"; got != want {
		t.Fatalf("listenHTTP() address = %q, want %q", got, want)
	}
}

func TestListenHTTPExplicitAddressDoesNotFallBack(t *testing.T) {
	blocker := listenTestTCP(t, "127.0.0.1:0")

	listener, err := listenHTTP(blocker.Addr().String(), true)
	if err == nil {
		_ = listener.Close()
		t.Fatal("listenHTTP() error = nil, want bind failure for explicit address")
	}
	if !strings.Contains(err.Error(), blocker.Addr().String()) {
		t.Fatalf("listenHTTP() error = %q, want blocked address", err)
	}
}

func TestDaemonRegistryRecordsActualHTTPBindAddress(t *testing.T) {
	listener := listenTestTCP(t, "127.0.0.1:0")
	dir := shortDaemonTestDir(t)
	socketPath := filepath.Join(dir, "atct.sock")

	want := listener.Addr().String()
	registry := daemonRegistry(listener, socketPath, "0.5.0")
	if registry.HTTPAddr != want {
		t.Fatalf("daemonRegistry() HTTPAddr = %q, want %q", registry.HTTPAddr, want)
	}
	if err := daemonctl.WriteRegistry(dir, registry); err != nil {
		t.Fatalf("WriteRegistry() error = %v", err)
	}
	got, err := daemonctl.ReadRegistry(dir)
	if err != nil {
		t.Fatalf("ReadRegistry() error = %v", err)
	}
	if got.HTTPAddr != want {
		t.Fatalf("registry HTTPAddr = %q, want %q", got.HTTPAddr, want)
	}
}

func TestListenHTTPReportsDefaultPortRangeWhenAllPortsAreBusy(t *testing.T) {
	for port := 8787; port <= 8796; port++ {
		listenTestTCP(t, "127.0.0.1:"+strconv.Itoa(port))
	}

	listener, err := listenHTTP(defaultListenAddr, false)
	if err == nil {
		_ = listener.Close()
		t.Fatal("listenHTTP() error = nil, want all-candidates bind failure")
	}
	for _, want := range []string{"8787", "8796"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("listenHTTP() error = %q, want attempted range to include %s", err, want)
		}
	}
}

func listenTestTCP(t *testing.T, addr string) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		if addr == defaultListenAddr && errors.Is(err, syscall.EADDRINUSE) {
			t.Logf("using the existing listener on %s as the occupied default port", addr)
			return nil
		}
		t.Fatalf("net.Listen(%q) error = %v", addr, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

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
	if !cfg.listenExplicit {
		t.Fatal("listenExplicit = false, want true")
	}
}

func TestParseArgsAcceptsProjectAdd(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "add", "myproj"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "project" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "project")
	}
	if cfg.projectAction != "add" {
		t.Fatalf("projectAction = %q, want %q", cfg.projectAction, "add")
	}
	if cfg.projectName != "myproj" {
		t.Fatalf("projectName = %q, want %q", cfg.projectName, "myproj")
	}
}

func TestParseArgsAcceptsProjectAddWithoutName(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "add"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.projectName != "" {
		t.Fatalf("projectName = %q, want empty", cfg.projectName)
	}
}

func TestParseArgsAcceptsProjectList(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "list"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "project" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "project")
	}
	if cfg.projectAction != "list" {
		t.Fatalf("projectAction = %q, want %q", cfg.projectAction, "list")
	}
}

func TestParseArgsAcceptsGoalAdd(t *testing.T) {
	cfg, err := parseArgs([]string{"goal", "add", "Build the next release", "-d", "Coordinate the release work"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "goal" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "goal")
	}
	if cfg.goalAction != "add" {
		t.Fatalf("goalAction = %q, want %q", cfg.goalAction, "add")
	}
	if cfg.goalTitle != "Build the next release" {
		t.Fatalf("goalTitle = %q, want %q", cfg.goalTitle, "Build the next release")
	}
	if cfg.goalDescription != "Coordinate the release work" {
		t.Fatalf("goalDescription = %q, want %q", cfg.goalDescription, "Coordinate the release work")
	}
}

func TestParseArgsAcceptsGoalList(t *testing.T) {
	cfg, err := parseArgs([]string{"goal", "list"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "goal" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "goal")
	}
	if cfg.goalAction != "list" {
		t.Fatalf("goalAction = %q, want %q", cfg.goalAction, "list")
	}
}

func TestParseArgsRejectsUnknownProjectAction(t *testing.T) {
	if _, err := parseArgs([]string{"project", "remove"}); err == nil {
		t.Fatal("parseArgs(project remove) returned nil error")
	}
}

func TestParseArgsRejectsProjectWithoutAction(t *testing.T) {
	if _, err := parseArgs([]string{"project"}); err == nil {
		t.Fatal("parseArgs(project) returned nil error")
	}
}

func TestPrepareDaemonStartRejectsHealthyDaemon(t *testing.T) {
	dir := shortDaemonTestDir(t)
	socketPath := filepath.Join(dir, "atct.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go acceptDaemonTestConnections(listener)

	if err := daemonctl.WriteRegistry(dir, daemonctl.Registry{
		PID:        os.Getpid(),
		HTTPAddr:   "127.0.0.1:8787",
		SocketPath: socketPath,
		Version:    version,
	}); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	err = prepareDaemonStart(dir)
	if err == nil {
		t.Fatal("prepareDaemonStart returned nil for a healthy daemon")
	}
	for _, want := range []string{"pid", "127.0.0.1:8787", "atct stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
	reg, err := daemonctl.ReadRegistry(dir)
	if err != nil {
		t.Fatalf("read registry after healthy rejection: %v", err)
	}
	if reg.PID != os.Getpid() {
		t.Fatalf("registry PID after healthy rejection = %d, want %d", reg.PID, os.Getpid())
	}
}

func TestPrepareDaemonStartAllowsDeadPID(t *testing.T) {
	dir := shortDaemonTestDir(t)
	socketPath := filepath.Join(dir, "atct.sock")
	if err := os.WriteFile(socketPath, []byte("stale socket"), 0o600); err != nil {
		t.Fatalf("write stale socket: %v", err)
	}
	if err := daemonctl.WriteRegistry(dir, daemonctl.Registry{
		PID:        -1,
		HTTPAddr:   "127.0.0.1:8787",
		SocketPath: socketPath,
		Version:    version,
	}); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	if err := prepareDaemonStart(dir); err != nil {
		t.Fatalf("prepareDaemonStart: %v", err)
	}
	if _, err := os.Stat(daemonctl.RegistryPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("registry after stale cleanup: err = %v, want not exist", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket after stale cleanup: err = %v, want not exist", err)
	}
}

func TestPrepareDaemonStartAllowsMissingRegistry(t *testing.T) {
	dir := shortDaemonTestDir(t)
	if err := prepareDaemonStart(dir); err != nil {
		t.Fatalf("prepareDaemonStart: %v", err)
	}
}

func shortDaemonTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct")
	if err != nil {
		t.Fatalf("mkdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func acceptDaemonTestConnections(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}
