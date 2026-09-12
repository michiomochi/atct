package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/michiomochi/atct/internal/daemonctl"
)

const (
	codexMonitorSetupTimeout = 10 * time.Second
	codexMonitorProcessWait  = 10 * time.Second
)

type codexMonitorProcessKind int

const (
	codexMonitorAppServer codexMonitorProcessKind = iota
	codexMonitorTUI
)

type codexMonitorProcess interface {
	PID() int
	Wait() error
	Signal(os.Signal) error
	ExitCode() int
}

type codexMonitorDeps struct {
	resolveCodex     func() (string, error)
	runNormal        func(string, []string) (int, error)
	startProcess     func(codexMonitorProcessKind, string, []string, []string) (codexMonitorProcess, error)
	atctExecutable   func() (string, error)
	connectAppServer func(context.Context, string) (codexMonitorApp, error)
	runBoundWatch    func(context.Context, string, string, *codexMonitorBridge) error
	projectPath      func() (string, error)
	reap             func(string) (daemonctl.CodexMonitorReapResult, error)
	register         func(string, daemonctl.CodexMonitorRecord) (func(), error)
	stopMonitors     func(string, string) (daemonctl.CodexMonitorStopResult, error)
	now              func() time.Time
	newMonitorToken  func() (string, error)
	stderr           io.Writer
}

// codexMonitorWatchOutput discards watcher diagnostics emitted while the watch
// loop reconnects or recovers the daemon. Agent actions use the typed sink and
// are not written through this diagnostic output. Diagnostics are nonfatal to
// the Codex monitor.
type codexMonitorWatchOutput struct{}

func (codexMonitorWatchOutput) Write(p []byte) (int, error) {
	// The watcher sends typed actions through its action sink. This writer is
	// diagnostics-only and must never classify raw text as an agent action.
	return len(p), nil
}

func runCodexMonitor(config cliConfig, dir string) (int, error) {
	return runCodexMonitorWithDeps(config, dir, codexMonitorDeps{})
}

func runCodexMonitorWithDeps(config cliConfig, dir string, deps codexMonitorDeps) (int, error) {
	deps = codexMonitorDepsWithDefaults(dir, deps)
	args := append([]string(nil), config.codexArgs...)

	if config.codexMonitorAction == "stop" {
		return runCodexMonitorStopWithDeps(dir, deps)
	}
	if config.codexMonitorAction != "monitor" {
		return 1, fmt.Errorf("unsupported Codex monitor action %q", config.codexMonitorAction)
	}
	if !config.codexMonitorAutomatic && len(args) > 0 && args[0] == "resume" {
		return deps.runNormal("codex", args)
	}
	if config.codexMonitorPassthrough {
		executable, err := deps.resolveCodex()
		if err != nil {
			return deps.runNormal("codex", args)
		}
		return deps.runNormal(executable, args)
	}

	projectPath, err := deps.projectPath()
	if err != nil {
		return codexMonitorSetupFailure(deps, "resolve project directory: "+err.Error(), "codex", args)
	}
	if _, err := deps.reap(dir); err != nil {
		return codexMonitorSetupFailure(deps, "reap monitor records: "+err.Error(), "codex", args)
	}
	monitorToken, err := deps.newMonitorToken()
	if err != nil {
		return codexMonitorSetupFailure(deps, "generate monitor token: "+err.Error(), "codex", args)
	}

	executable, err := deps.resolveCodex()
	if err != nil {
		return codexMonitorSetupFailure(deps, err.Error(), "codex", args)
	}
	childEnv, err := codexMonitorEnvironment(monitorToken, deps.atctExecutable)
	if err != nil {
		return 1, fmt.Errorf("prepare Codex hooks: %w", err)
	}

	monitorDir := daemonctl.CodexMonitorRegistryDir(dir)
	socketPath := filepath.Join(monitorDir, fmt.Sprintf("%d.sock", os.Getpid()))
	if err := os.MkdirAll(monitorDir, 0o700); err != nil {
		return codexMonitorSetupFailure(deps, "create monitor directory: "+err.Error(), executable, args)
	}
	if err := os.Chmod(monitorDir, 0o700); err != nil {
		return codexMonitorSetupFailure(deps, "protect monitor directory: "+err.Error(), executable, args)
	}
	appArgs := []string{"app-server", "--listen", "unix://" + socketPath}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return codexMonitorSetupFailure(deps, "remove stale monitor socket: "+err.Error(), executable, args)
	}
	appProcess, err := deps.startProcess(codexMonitorAppServer, executable, appArgs, childEnv)
	if err != nil {
		return codexMonitorSetupFailure(deps, "start App Server: "+err.Error(), executable, args)
	}
	appWait := waitCodexMonitorProcess(appProcess)

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), codexMonitorSetupTimeout)
	app, err := deps.connectAppServer(setupCtx, socketPath)
	cancelSetup()
	if err != nil {
		if app != nil {
			_ = app.Close()
		}
		if cleanupErr := stopCodexMonitorChild(appProcess, appWait); cleanupErr != nil {
			fmt.Fprintf(deps.stderr, "atct codex monitor cleanup: %v\n", cleanupErr)
		}
		_ = os.Remove(socketPath)
		return codexMonitorSetupFailure(deps, "connect App Server: "+err.Error(), executable, args)
	}

	// The monitor owns this fresh App Server socket. Let the remote TUI create
	// its session, then attach to that TUI's thread/started notification. A
	// thread/start response cannot be resumed by a separate remote TUI client.
	bridge := newCodexMonitorBridge(app, "")
	lifecycleCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	monitorCtx, cancelMonitor := context.WithCancel(lifecycleCtx)
	watchCtx, cancelWatch := context.WithCancel(monitorCtx)
	defer cancelMonitor()
	defer cancelWatch()

	startedAt := deps.now().UTC().Format(time.RFC3339Nano)
	if processStartedAt, err := daemonctl.CodexMonitorProcessStartTime(os.Getpid()); err == nil {
		startedAt = processStartedAt.UTC().Format(time.RFC3339Nano)
	}
	record := daemonctl.CodexMonitorRecord{
		SupervisorPID: os.Getpid(),
		AppServerPID:  appProcess.PID(),
		SocketPath:    socketPath,
		ProjectPath:   projectPath,
		StartedAt:     startedAt,
	}
	recordCleanup, err := deps.register(dir, record)
	if err != nil {
		_ = app.Close()
		if cleanupErr := stopCodexMonitorChild(appProcess, appWait); cleanupErr != nil {
			fmt.Fprintf(deps.stderr, "atct codex monitor cleanup: %v\n", cleanupErr)
		}
		_ = os.Remove(socketPath)
		return codexMonitorSetupFailure(deps, "register monitor: "+err.Error(), executable, args)
	}

	bridgeDone := make(chan error, 1)
	go func() { bridgeDone <- bridge.Run(monitorCtx) }()
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- deps.runBoundWatch(watchCtx, projectPath, monitorToken, bridge)
	}()

	remoteArgs := make([]string, 0, len(args)+2)
	remoteArgs = append(remoteArgs, "--remote", "unix://"+socketPath)
	remoteArgs = append(remoteArgs, args...)
	tuiProcess, err := deps.startProcess(codexMonitorTUI, executable, remoteArgs, childEnv)
	if err != nil {
		cancelWatch()
		cancelMonitor()
		_ = app.Close()
		waitCodexMonitorDone(bridgeDone)
		waitCodexMonitorDone(watchDone)
		if cleanupErr := stopCodexMonitorChild(appProcess, appWait); cleanupErr != nil {
			fmt.Fprintf(deps.stderr, "atct codex monitor cleanup: %v\n", cleanupErr)
		}
		if recordCleanup != nil {
			recordCleanup()
		}
		_ = os.Remove(socketPath)
		return codexMonitorSetupFailure(deps, "start Codex TUI: "+err.Error(), executable, args)
	}
	tuiDone := waitCodexMonitorProcess(tuiProcess)

	monitorDisabled := false
	disableMonitor := func(err error) {
		if monitorDisabled || err == nil {
			return
		}
		monitorDisabled = true
		fmt.Fprintf(deps.stderr, "atct codex monitor disabled: %s; Codex session remains active\n", err)
		cancelWatch()
	}

	var cleanupOnce bool
	cleanup := func() {
		if cleanupOnce {
			return
		}
		cleanupOnce = true
		cancelWatch()
		cancelMonitor()
		_ = app.Close()
		waitCodexMonitorDone(bridgeDone)
		waitCodexMonitorDone(watchDone)
		if cleanupErr := stopCodexMonitorChild(appProcess, appWait); cleanupErr != nil {
			fmt.Fprintf(deps.stderr, "atct codex monitor cleanup: %v\n", cleanupErr)
		}
		if recordCleanup != nil {
			recordCleanup()
		}
		if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(deps.stderr, "atct codex monitor cleanup: remove socket: %v\n", err)
		}
	}

	for {
		select {
		case waitErr := <-tuiDone:
			cleanup()
			return codexMonitorExitCode(tuiProcess, waitErr), nil
		case <-lifecycleCtx.Done():
			if cleanupErr := stopCodexMonitorChild(tuiProcess, tuiDone); cleanupErr != nil {
				fmt.Fprintf(deps.stderr, "atct codex monitor cleanup: %v\n", cleanupErr)
			}
			cleanup()
			return 0, nil
		case err := <-bridgeDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				disableMonitor(err)
			}
			bridgeDone = nil
		case err := <-watchDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				if codexMonitorWatchErrorIsTerminal(err) {
					disableMonitor(err)
				} else {
					fmt.Fprintf(deps.stderr, "atct codex monitor watcher recovering: %s\n", err)
				}
			}
			watchDone = nil
		}
	}
}

func codexMonitorWatchErrorIsTerminal(err error) bool {
	var sinkErr *watchSinkError
	return errors.As(err, &sinkErr)
}

func codexMonitorThreadMatches(thread codexThread, cwd string) bool {
	exactCWD, err := codexExactCWD(cwd)
	if err != nil {
		return false
	}
	return strings.TrimSpace(thread.ID) != "" &&
		thread.CWD == exactCWD &&
		thread.Source == "cli" &&
		(thread.SourceKind == "" || thread.SourceKind == "cli") &&
		strings.TrimSpace(thread.Status.Type) != ""
}

func codexMonitorSetupFailure(deps codexMonitorDeps, reason, executable string, args []string) (int, error) {
	return codexMonitorFallback(deps, reason, executable, args)
}

func codexMonitorFallback(deps codexMonitorDeps, reason, executable string, args []string) (int, error) {
	fmt.Fprintf(deps.stderr, "atct codex monitor disabled: %s; running normal codex\n", reason)
	if strings.TrimSpace(executable) == "" {
		executable = "codex"
	}
	return deps.runNormal(executable, args)
}

func runCodexMonitorStopWithDeps(dir string, deps codexMonitorDeps) (int, error) {
	projectPath, err := deps.projectPath()
	if err != nil {
		return 1, err
	}
	result, err := deps.stopMonitors(dir, projectPath)
	if err != nil {
		return 1, err
	}
	for _, pid := range result.Failed {
		fmt.Fprintf(deps.stderr, "atct codex monitor: failed to stop supervisor %d\n", pid)
	}
	if len(result.Failed) > 0 {
		return 1, fmt.Errorf("%d Codex monitor supervisor(s) did not exit", len(result.Failed))
	}
	if len(result.Stopped) == 0 {
		fmt.Fprintf(deps.stderr, "no atct codex monitor was running for %s\n", projectPath)
		return 0, nil
	}
	fmt.Fprintf(deps.stderr, "atct codex monitor stopped %d monitor(s) for %s\n", len(result.Stopped), projectPath)
	return 0, nil
}

func codexMonitorDepsWithDefaults(dir string, deps codexMonitorDeps) codexMonitorDeps {
	if deps.resolveCodex == nil {
		deps.resolveCodex = resolveCodexExecutable
	}
	if deps.runNormal == nil {
		deps.runNormal = runCodexProcess
	}
	if deps.startProcess == nil {
		deps.startProcess = startCodexMonitorProcess
	}
	if deps.atctExecutable == nil {
		deps.atctExecutable = os.Executable
	}
	if deps.connectAppServer == nil {
		deps.connectAppServer = connectCodexAppServer
	}
	if deps.projectPath == nil {
		deps.projectPath = codexMonitorProjectPath
	}
	if deps.reap == nil {
		deps.reap = daemonctl.ReapCodexMonitors
	}
	if deps.register == nil {
		deps.register = daemonctl.RegisterCodexMonitor
	}
	if deps.stopMonitors == nil {
		deps.stopMonitors = daemonctl.StopCodexMonitorsForProject
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.newMonitorToken == nil {
		deps.newMonitorToken = newMonitorToken
	}
	if deps.stderr == nil {
		deps.stderr = os.Stderr
	}
	if deps.runBoundWatch == nil {
		deps.runBoundWatch = func(ctx context.Context, projectPath, token string, bridge *codexMonitorBridge) error {
			if err := ensureWatchDaemon(dir); err != nil {
				return err
			}
			client := &http.Client{}
			urls := watchBaseURLs(dir)
			return runMonitorBindingLoop(ctx, client, urls, token, func(scopeCtx context.Context, scope watchScope) error {
				return runCodexMonitorWatchScoped(scopeCtx, client, urls, projectPath, scope, bridge, func() error {
					return ensureWatchDaemon(dir)
				})
			})
		}
	}
	return deps
}

func resolveCodexExecutable() (string, error) {
	return resolveRealCodex(os.Getenv("PATH"))
}

func runCodexProcess(executable string, args []string) (int, error) {
	if executable == "codex" {
		resolved, err := resolveCodexExecutable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "codex: command not found")
			return 127, nil
		}
		executable = resolved
	}
	cmd := exec.Command(executable, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	if err := cmd.Wait(); err != nil {
		return codexCommandExitCode(cmd, err), nil
	}
	return codexCommandExitCode(cmd, nil), nil
}

func codexMonitorExitCode(process codexMonitorProcess, waitErr error) int {
	if code := process.ExitCode(); code >= 0 {
		return code
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok && exitErr.ProcessState != nil {
		if status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	return 1
}

func codexCommandExitCode(cmd *exec.Cmd, waitErr error) int {
	if cmd != nil && cmd.ProcessState != nil {
		if code := cmd.ProcessState.ExitCode(); code >= 0 {
			return code
		}
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	if waitErr != nil {
		return 1
	}
	return 0
}

func codexMonitorProjectPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = filepath.Clean(resolved)
	}
	return absolute, nil
}

func codexMonitorEnvironment(monitorToken string, atctExecutable func() (string, error)) ([]string, error) {
	if atctExecutable == nil {
		return nil, errors.New("resolve atct executable")
	}
	executable, err := atctExecutable()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(executable) == "" {
		return nil, errors.New("resolve atct executable")
	}
	if strings.TrimSpace(monitorToken) == "" {
		return nil, errors.New("monitor token is empty")
	}
	return []string{"ATCT_BIN=" + executable, "ATCT_MONITOR_TOKEN=" + monitorToken}, nil
}

func startCodexMonitorProcess(kind codexMonitorProcessKind, executable string, args []string, extraEnv []string) (codexMonitorProcess, error) {
	cmd := exec.Command(executable, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	if kind == codexMonitorAppServer {
		cmd.Stdout = io.Discard
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execCodexMonitorProcess{cmd: cmd}, nil
}

type execCodexMonitorProcess struct {
	cmd *exec.Cmd
}

func (p *execCodexMonitorProcess) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *execCodexMonitorProcess) Wait() error { return p.cmd.Wait() }

func (p *execCodexMonitorProcess) Signal(signal os.Signal) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return errors.New("Codex monitor process is not started")
	}
	return p.cmd.Process.Signal(signal)
}

func (p *execCodexMonitorProcess) ExitCode() int {
	if p == nil || p.cmd == nil || p.cmd.ProcessState == nil {
		return -1
	}
	return p.cmd.ProcessState.ExitCode()
}

func waitCodexMonitorProcess(process codexMonitorProcess) <-chan error {
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	return done
}

func waitCodexMonitorDone(done <-chan error) {
	if done == nil {
		return
	}
	timer := time.NewTimer(codexMonitorProcessWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func stopCodexMonitorChild(process codexMonitorProcess, done <-chan error) error {
	if process == nil || done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal Codex monitor child %d: %w", process.PID(), err)
	}
	timer := time.NewTimer(codexMonitorProcessWait)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("Codex monitor child %d did not exit within %s", process.PID(), codexMonitorProcessWait)
	}
}

func connectCodexAppServer(ctx context.Context, socketPath string) (codexMonitorApp, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	for {
		app, err := dialCodexAppServer(ctx, context.Background(), socketPath)
		if err == nil {
			if err = app.Initialize(ctx); err == nil {
				return app, nil
			}
			_ = app.Close()
		}
		lastErr = err
		if ctx.Err() != nil {
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return nil, lastErr
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return nil, lastErr
		case <-timer.C:
		}
	}
}
