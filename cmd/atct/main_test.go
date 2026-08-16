package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/daemonctl"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantListen string
		wantErr    bool
	}{
		{
			name:       "daemon parses listen flag after subcommand",
			args:       []string{"daemon", "-listen", "127.0.0.1:18787"},
			wantListen: "127.0.0.1:18787",
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
		})
	}
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
