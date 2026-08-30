package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/rpc"
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

func TestParseArgsCodexMonitorPreservesRawArguments(t *testing.T) {
	cfg, err := parseArgs([]string{"codex", "monitor", "--", "-m", "gpt-5", "--config", "a=b"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "codex" {
		t.Fatalf("subcommand = %q, want codex", cfg.subcommand)
	}
	if cfg.codexMonitorAction != "monitor" {
		t.Fatalf("codexMonitorAction = %q, want monitor", cfg.codexMonitorAction)
	}
	want := []string{"-m", "gpt-5", "--config", "a=b"}
	if !slices.Equal(cfg.codexArgs, want) {
		t.Fatalf("codexArgs = %#v, want %#v", cfg.codexArgs, want)
	}
}

func TestParseArgsCodexShimRunPreservesOpaqueArguments(t *testing.T) {
	cfg, err := parseArgs([]string{"codex", "shim", "run", "--", "resume", "abc", "--last"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.codexShimAction != "run" {
		t.Fatalf("codexShimAction = %q, want run", cfg.codexShimAction)
	}
	want := []string{"resume", "abc", "--last"}
	if !slices.Equal(cfg.codexArgs, want) {
		t.Fatalf("codexArgs = %#v, want %#v", cfg.codexArgs, want)
	}
}

func TestParseArgsCodexShimInstallRecordsProfile(t *testing.T) {
	cfg, err := parseArgs([]string{"codex", "shim", "install", "--profile", "/tmp/profile"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.codexShimAction != "install" {
		t.Fatalf("codexShimAction = %q, want install", cfg.codexShimAction)
	}
	if cfg.codexShimProfile != "/tmp/profile" {
		t.Fatalf("codexShimProfile = %q, want /tmp/profile", cfg.codexShimProfile)
	}
}

func TestParseArgsCodexShimRejectsInvalidGrammar(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing run delimiter", args: []string{"codex", "shim", "run", "resume"}},
		{name: "unknown shim action", args: []string{"codex", "shim", "unknown"}},
		{name: "trailing install arguments", args: []string{"codex", "shim", "install", "extra"}},
		{name: "short install profile option", args: []string{"codex", "shim", "install", "-profile", "/tmp/profile"}},
		{name: "empty install profile path", args: []string{"codex", "shim", "install", "--profile="}},
		{name: "role selector on shim run", args: []string{"codex", "shim", "run", "--role", "executor", "--", "resume"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseArgs(tt.args); !errors.Is(err, errInvalidArgs) {
				t.Fatalf("parseArgs(%q) error = %v, want errInvalidArgs", tt.args, err)
			}
		})
	}
}

func TestParseArgsCodexMonitorRejectsScopeBeforePassthroughDelimiter(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"legacy scope with separate value", []string{"codex", "monitor", "--scope", "task"}},
		{"legacy scope with equals value", []string{"codex", "monitor", "--scope=task"}},
		{"scope before explicit executor selectors", []string{"codex", "monitor", "--scope", "task", "--role", "executor", "--task", "846"}},
		{"scope equals after explicit executor selectors", []string{"codex", "monitor", "--role", "executor", "--task", "846", "--scope=task"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseArgs(tt.args); err == nil {
				t.Fatalf("parseArgs(%q) error = nil, want invalid arguments", tt.args)
			}
		})
	}
}

func TestParseArgsCodexMonitorPreservesScopeAfterPassthroughDelimiter(t *testing.T) {
	want := []string{"--scope", "task", "--scope=project"}
	cfg, err := parseArgs(append([]string{"codex", "monitor", "--"}, want...))
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !slices.Equal(cfg.codexArgs, want) {
		t.Fatalf("codexArgs = %#v, want %#v", cfg.codexArgs, want)
	}
}

func TestParseArgsCodexMonitorRoleSelectorMatrix(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantErr  bool
		wantArgs []string
	}{
		{"commander needs no selector", []string{"codex", "monitor", "--role", "commander", "--", "-m", "gpt-5"}, false, []string{"-m", "gpt-5"}},
		{"subcommander needs goal", []string{"codex", "monitor", "--role", "subcommander", "--goal", "206"}, false, nil},
		{"executor needs task", []string{"codex", "monitor", "--role", "executor", "--task", "846"}, false, nil},
		{"unknown role is rejected", []string{"codex", "monitor", "--role", "observer"}, true, nil},
		{"commander rejects goal", []string{"codex", "monitor", "--role", "commander", "--goal", "206"}, true, nil},
		{"subcommander rejects missing goal", []string{"codex", "monitor", "--role", "subcommander"}, true, nil},
		{"subcommander rejects task", []string{"codex", "monitor", "--role", "subcommander", "--goal", "206", "--task", "846"}, true, nil},
		{"executor rejects missing task", []string{"codex", "monitor", "--role", "executor"}, true, nil},
		{"executor rejects goal", []string{"codex", "monitor", "--role", "executor", "--task", "846", "--goal", "206"}, true, nil},
		{"duplicate role is rejected", []string{"codex", "monitor", "--role", "executor", "--role", "executor", "--task", "846"}, true, nil},
		{"duplicate task is rejected", []string{"codex", "monitor", "--role", "executor", "--task", "846", "--task", "847"}, true, nil},
		{"unknown monitor option is rejected", []string{"codex", "monitor", "--role", "executor", "--task", "846", "--unexpected"}, true, nil},
		{"explicit role rejects noninteractive pass through", []string{"codex", "monitor", "--role", "executor", "--task", "846", "--", "exec", "--help"}, true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseArgs(%q) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if err == nil && !slices.Equal(cfg.codexArgs, tt.wantArgs) {
				t.Fatalf("codexArgs = %#v, want %#v; monitor role flags must not reach Codex", cfg.codexArgs, tt.wantArgs)
			}
		})
	}
}

func TestParseArgsCodexMonitorStop(t *testing.T) {
	cfg, err := parseArgs([]string{"codex", "monitor", "stop"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.codexMonitorAction != "stop" {
		t.Fatalf("codexMonitorAction = %q, want stop", cfg.codexMonitorAction)
	}
	if len(cfg.codexArgs) != 0 {
		t.Fatalf("codexArgs = %#v, want empty", cfg.codexArgs)
	}
}

func TestParseArgsCodexMonitorPassesThroughExec(t *testing.T) {
	cfg, err := parseArgs([]string{"codex", "monitor", "exec", "--help"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.codexMonitorPassthrough {
		t.Fatal("codexMonitorPassthrough = false, want true")
	}
	want := []string{"exec", "--help"}
	if !slices.Equal(cfg.codexArgs, want) {
		t.Fatalf("codexArgs = %#v, want %#v", cfg.codexArgs, want)
	}
}

func TestParseArgsCodexMonitorPassesThroughExecAlias(t *testing.T) {
	cfg, err := parseArgs([]string{"codex", "monitor", "e", "--help"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.codexMonitorPassthrough {
		t.Fatal("codexMonitorPassthrough = false, want true for exec alias")
	}
	if want := []string{"e", "--help"}; !slices.Equal(cfg.codexArgs, want) {
		t.Fatalf("codexArgs = %#v, want %#v", cfg.codexArgs, want)
	}
}

func TestParseArgsCodexMonitorPassesThroughNonInteractiveCommands(t *testing.T) {
	commands := []string{
		"app-server", "apply", "archive", "cloud", "completion", "debug", "delete", "doctor",
		"exec-server", "features", "help", "login", "logout", "mcp", "mcp-server", "plugin",
		"remote-control", "review", "sandbox", "unarchive", "update",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			cfg, err := parseArgs([]string{"codex", "monitor", command, "--help"})
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			if !cfg.codexMonitorPassthrough {
				t.Fatalf("codexMonitorPassthrough = false for %q, want true", command)
			}
		})
	}
}

func TestParseArgsCodexMonitorStopRejectsExtraArguments(t *testing.T) {
	output := captureStderr(t, func() {
		if _, err := parseArgs([]string{"codex", "monitor", "stop", "extra"}); err == nil {
			t.Fatal("parseArgs(codex monitor stop extra) returned nil error")
		}
	})
	if !strings.Contains(output, "codex monitor stop") {
		t.Fatalf("output = %q, want monitor stop usage", output)
	}
}

func TestListenHTTPDefaultAddressBindsOnceWhenAvailable(t *testing.T) {
	listener := &fakeNetListener{addr: fakeNetAddr(defaultListenAddr)}
	var calls []string
	replaceListenTCP(t, func(network, addr string) (net.Listener, error) {
		calls = append(calls, addr)
		return listener, nil
	})

	got, err := listenHTTP(defaultListenAddr, false)
	if err != nil {
		t.Fatalf("listenHTTP() error = %v", err)
	}
	if got != listener {
		t.Fatalf("listenHTTP() listener = %v, want fake listener", got)
	}
	if len(calls) != 1 || calls[0] != defaultListenAddr {
		t.Fatalf("listenHTTP() calls = %v, want one call to %q", calls, defaultListenAddr)
	}
}

func TestListenHTTPDefaultAddressFailureDoesNotFallback(t *testing.T) {
	var calls []string
	replaceListenTCP(t, func(network, addr string) (net.Listener, error) {
		calls = append(calls, addr)
		return nil, syscall.EADDRINUSE
	})

	listener, err := listenHTTP(defaultListenAddr, false)
	if err == nil {
		t.Fatal("listenHTTP() error = nil, want default bind failure")
	}
	if listener != nil {
		t.Fatalf("listenHTTP() listener = %v, want nil", listener)
	}
	if !strings.Contains(err.Error(), defaultListenAddr) {
		t.Fatalf("listenHTTP() error = %q, want blocked address", err)
	}
	if len(calls) != 1 || calls[0] != defaultListenAddr {
		t.Fatalf("listenHTTP() calls = %v, want one call to %q", calls, defaultListenAddr)
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

type fakeNetListener struct {
	addr net.Addr
}

func (l *fakeNetListener) Accept() (net.Conn, error) { return nil, io.EOF }
func (l *fakeNetListener) Close() error              { return nil }
func (l *fakeNetListener) Addr() net.Addr            { return l.addr }

type fakeNetAddr string

func (a fakeNetAddr) Network() string { return "tcp" }
func (a fakeNetAddr) String() string  { return string(a) }

func replaceListenTCP(t *testing.T, replacement func(string, string) (net.Listener, error)) {
	t.Helper()
	original := listenTCP
	listenTCP = replacement
	t.Cleanup(func() { listenTCP = original })
}

func TestParseArgsRejectsRemovedCommandsAsUnknown(t *testing.T) {
	for _, command := range []string{"ensure", "stop"} {
		t.Run(command, func(t *testing.T) {
			var cfg cliConfig
			var err error
			output := captureStderr(t, func() {
				cfg, err = parseArgs([]string{command})
			})
			if err == nil {
				t.Fatalf("parseArgs(%q) returned nil error with config %#v", command, cfg)
			}
			if !strings.Contains(output, `unknown subcommand "`+command+`"`) {
				t.Fatalf("parseArgs(%q) output = %q, want unknown subcommand error", command, output)
			}
			if strings.Contains(output, "deprecated") {
				t.Fatalf("parseArgs(%q) output = %q, must not mention deprecation", command, output)
			}
		})
	}
}

func TestParseArgsAcceptsDaemonActions(t *testing.T) {
	for _, action := range []string{"start", "stop"} {
		t.Run(action, func(t *testing.T) {
			cfg, err := parseArgs([]string{"daemon", action})
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			if cfg.subcommand != "daemon" {
				t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "daemon")
			}
			if cfg.daemonAction != action {
				t.Fatalf("daemonAction = %q, want %q", cfg.daemonAction, action)
			}
		})
	}
}

func TestParseArgsRejectsUnknownDaemonAction(t *testing.T) {
	output := captureStderr(t, func() {
		if _, err := parseArgs([]string{"daemon", "restart"}); err == nil {
			t.Fatal("parseArgs(daemon restart) returned nil error")
		}
	})
	if !strings.Contains(output, "unknown daemon action") {
		t.Fatalf("output = %q, want unknown daemon action", output)
	}
	if !strings.Contains(output, "start or stop") {
		t.Fatalf("output = %q, want available daemon actions", output)
	}
}

func TestUsageListsPublicDaemonActionsNotForegroundEntry(t *testing.T) {
	output := captureStderr(t, func() {
		if _, err := parseArgs(nil); err == nil {
			t.Fatal("parseArgs(nil) returned nil error")
		}
	})
	for _, want := range []string{"daemon start", "daemon stop"} {
		if !strings.Contains(output, want) {
			t.Fatalf("usage = %q, want %q", output, want)
		}
	}
	if strings.Contains(output, "daemon    Run the ATCT daemon in the foreground") {
		t.Fatalf("usage = %q, must omit foreground daemon entry", output)
	}
}

func TestDaemonCommandLifecycle(t *testing.T) {
	binary := buildAtctTestBinary(t)
	home, err := os.MkdirTemp("/tmp", "atct-daemon-test-")
	if err != nil {
		t.Fatalf("create daemon test HOME: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	dir := filepath.Join(home, ".atct")
	listenAddr := daemonTestListenAddr(t)
	t.Cleanup(func() {
		_, _ = daemonctl.StopWithWatchWarning(daemonctl.Config{Dir: dir, Version: version}, io.Discard)
	})

	output, err := runAtctCommand(binary, home, "daemon", "start", "-listen", listenAddr)
	if err != nil {
		t.Fatalf("atct daemon start: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(string(output), "atct daemon ready") {
		t.Fatalf("atct daemon start output = %q, want ready message", output)
	}
	started := waitForHealthyRegistry(t, dir)
	if started.PID == os.Getpid() {
		t.Fatalf("daemon PID = %d, command unexpectedly ran in the test process", started.PID)
	}

	output, err = runAtctCommand(binary, home, "daemon", "stop")
	if err != nil {
		t.Fatalf("atct daemon stop: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(string(output), "atct daemon stopped") {
		t.Fatalf("atct daemon stop output = %q, want stopped message", output)
	}
	waitForRegistryRemoval(t, dir)

	foreground := exec.Command(binary, "daemon", "-listen", listenAddr)
	foreground.Env = testEnvWithHome(home)
	foreground.Stdout = io.Discard
	foreground.Stderr = io.Discard
	if err := foreground.Start(); err != nil {
		t.Fatalf("start bare atct daemon: %v", err)
	}
	foregroundDone := false
	t.Cleanup(func() {
		if foregroundDone {
			return
		}
		_ = foreground.Process.Signal(syscall.SIGTERM)
		_ = foreground.Wait()
	})
	waitForHealthyRegistry(t, dir)
	if err := foreground.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("stop bare atct daemon: %v", err)
	}
	if err := foreground.Wait(); err != nil {
		t.Fatalf("wait for bare atct daemon: %v", err)
	}
	foregroundDone = true
	waitForRegistryRemoval(t, dir)
}

func daemonTestListenAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve daemon test address: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release daemon test address: %v", err)
	}
	return addr
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	previous := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = previous
		_ = r.Close()
		_ = w.Close()
	}()

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stderr pipe: %v", err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}
	return string(output)
}

func buildAtctTestBinary(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	binary := filepath.Join(t.TempDir(), "atct")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = filepath.Dir(source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build atct test binary: %v\noutput:\n%s", err, output)
	}
	return binary
}

func runAtctCommand(binary, home string, args ...string) ([]byte, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = testEnvWithHome(home)
	return cmd.CombinedOutput()
}

func testEnvWithHome(home string) []string {
	env := os.Environ()
	for i, entry := range env {
		if strings.HasPrefix(entry, "HOME=") {
			env[i] = "HOME=" + home
			return env
		}
	}
	return append(env, "HOME="+home)
}

func waitForHealthyRegistry(t *testing.T, dir string) daemonctl.Registry {
	t.Helper()
	deadline := time.Now().Add(daemonctl.StartTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		reg, err := daemonctl.ReadRegistry(dir)
		if err == nil && reg.Healthy() {
			return reg
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon did not become healthy: %v", lastErr)
	return daemonctl.Registry{}
}

func waitForRegistryRemoval(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(daemonctl.StartTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := daemonctl.ReadRegistry(dir)
		if errors.Is(err, daemonctl.ErrNoRegistry) {
			return
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon registry was not removed: %v", lastErr)
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
	for _, want := range []string{"pid", "127.0.0.1:8787", "atct daemon stop"} {
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

func TestParseHandoffComplete(t *testing.T) {
	cfg, err := parseArgs([]string{"handoff", "complete", "handoff-1", "task-1"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "handoff" || cfg.handoffAction != "complete" {
		t.Fatalf("handoff command = %#v, want complete", cfg)
	}
	if cfg.handoffID != "handoff-1" || cfg.handoffTaskID != "task-1" {
		t.Fatalf("handoff identifiers = %q, %q; want handoff-1, task-1", cfg.handoffID, cfg.handoffTaskID)
	}
}

func TestParseHandoffYielded(t *testing.T) {
	cfg, err := parseArgs([]string{"handoff", "yielded", "task-1"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "handoff" || cfg.handoffAction != "yielded" {
		t.Fatalf("handoff command = %#v, want yielded", cfg)
	}
	if cfg.handoffID != "" || cfg.handoffTaskID != "task-1" {
		t.Fatalf("handoff identifiers = %q, %q; want empty handoff ID and task-1", cfg.handoffID, cfg.handoffTaskID)
	}
}

func TestRunHandoffYieldedUsesExistingDaemonWithoutStartingOne(t *testing.T) {
	dir := shortDaemonTestDir(t)
	var err error
	listener, err := net.Listen("unix", filepath.Join(dir, "atct.sock"))
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := daemonctl.WriteRegistry(dir, daemonctl.Registry{
		PID:        os.Getpid(),
		SocketPath: listener.Addr().String(),
		Version:    version,
	}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	requests := make(chan rpc.Request, 1)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var req rpc.Request
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					return
				}
				requests <- req
				_, _ = conn.Write([]byte(`{"result":null}` + "\n"))
			}()
		}
	}()

	if err := runHandoff(cliConfig{handoffAction: "yielded", handoffTaskID: "task-1"}, dir, filepath.Join(dir, "atct-not-started")); err != nil {
		t.Fatalf("runHandoff: %v", err)
	}
	select {
	case req := <-requests:
		if req.Method != "handoff.yielded" {
			t.Fatalf("RPC method = %q, want handoff.yielded", req.Method)
		}
		var params map[string]string
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("RPC params: %v", err)
		}
		if params["task_id"] != "task-1" || len(params) != 1 {
			t.Fatalf("RPC params = %#v, want only task_id=task-1", params)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handoff.yielded RPC")
	}
}

func TestRunHandoffYieldedIsSilentWhenDaemonIsAbsent(t *testing.T) {
	dir := shortDaemonTestDir(t)
	if err := runHandoff(cliConfig{handoffAction: "yielded", handoffTaskID: "task-1"}, dir, filepath.Join(dir, "atct-not-started")); err != nil {
		t.Fatalf("runHandoff without daemon: %v", err)
	}
}
