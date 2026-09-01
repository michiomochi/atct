package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
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
	startProcess     func(codexMonitorProcessKind, string, []string) (codexMonitorProcess, error)
	connectAppServer func(context.Context, string) (codexMonitorApp, error)
	runWatch         func(context.Context, *codexMonitorBridge) error
	runWatchScoped   func(context.Context, string, watchScope, *codexMonitorBridge) error
	projectPath      func() (string, error)
	resolveScope     func(context.Context, string, watchScope) (watchScope, error)
	reap             func(string) (daemonctl.CodexMonitorReapResult, error)
	register         func(string, daemonctl.CodexMonitorRecord) (func(), error)
	stopMonitors     func(string, string) (daemonctl.CodexMonitorStopResult, error)
	now              func() time.Time
	stderr           io.Writer
}

// codexMonitorWatchOutput discards action lines forwarded through the sink,
// but turns watcher diagnostics into an error so the supervisor can disable a
// monitor instead of silently retrying a lost SSE connection forever.
type codexMonitorWatchOutput struct{}

func (codexMonitorWatchOutput) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line == "" || isCodexMonitorActionLine(line) {
		return len(p), nil
	}
	return 0, fmt.Errorf("Codex monitor watcher failure: %s", line)
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
	if config.codexMonitorPassthrough {
		executable, err := deps.resolveCodex()
		if err != nil {
			return deps.runNormal("codex", args)
		}
		return deps.runNormal(executable, args)
	}

	projectPath, err := deps.projectPath()
	if err != nil {
		return codexMonitorSetupFailure(config, deps, "resolve project directory: "+err.Error(), "codex", args)
	}
	scope := watchScope{}
	if config.codexMonitorAutomatic {
		if config.codexMonitorExplicit || config.codexMonitorRole != "commander" || strings.TrimSpace(config.codexMonitorProjectID) == "" || strings.TrimSpace(config.codexMonitorGoalID) != "" || strings.TrimSpace(config.codexMonitorTaskID) != "" {
			return codexMonitorSetupFailure(config, deps, "invalid automatic monitor scope", "codex", args)
		}
		scope = watchScope{Role: "commander", ProjectID: config.codexMonitorProjectID}
	} else if config.codexMonitorExplicit {
		scope, err = deps.resolveScope(context.Background(), projectPath, watchScope{Role: config.codexMonitorRole, GoalID: config.codexMonitorGoalID, TaskID: config.codexMonitorTaskID})
		if err != nil {
			return codexMonitorSetupFailure(config, deps, "resolve explicit monitor scope: "+err.Error(), "codex", args)
		}
	}
	if _, err := deps.reap(dir); err != nil {
		return codexMonitorSetupFailure(config, deps, "reap monitor records: "+err.Error(), "codex", args)
	}

	executable, err := deps.resolveCodex()
	if err != nil {
		return codexMonitorSetupFailure(config, deps, err.Error(), "codex", args)
	}

	monitorDir := daemonctl.CodexMonitorRegistryDir(dir)
	socketPath := filepath.Join(monitorDir, fmt.Sprintf("%d.sock", os.Getpid()))
	if err := os.MkdirAll(monitorDir, 0o700); err != nil {
		return codexMonitorSetupFailure(config, deps, "create monitor directory: "+err.Error(), executable, args)
	}
	if err := os.Chmod(monitorDir, 0o700); err != nil {
		return codexMonitorSetupFailure(config, deps, "protect monitor directory: "+err.Error(), executable, args)
	}
	appArgs := []string{"app-server", "--listen", "unix://" + socketPath}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return codexMonitorSetupFailure(config, deps, "remove stale monitor socket: "+err.Error(), executable, args)
	}
	appProcess, err := deps.startProcess(codexMonitorAppServer, executable, appArgs)
	if err != nil {
		return codexMonitorSetupFailure(config, deps, "start App Server: "+err.Error(), executable, args)
	}
	appWait := waitCodexMonitorProcess(appProcess)

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), codexMonitorSetupTimeout)
	app, err := deps.connectAppServer(setupCtx, socketPath)
	connected := err == nil
	var thread codexThread
	if err == nil {
		thread, err = app.StartThread(setupCtx, projectPath)
		if err == nil && strings.TrimSpace(thread.ID) == "" {
			err = errors.New("thread/start response has no thread ID")
		}
	}
	cancelSetup()
	if err != nil {
		if app != nil {
			_ = app.Close()
		}
		if cleanupErr := stopCodexMonitorChild(appProcess, appWait); cleanupErr != nil {
			fmt.Fprintf(deps.stderr, "atct codex monitor cleanup: %v\n", cleanupErr)
		}
		_ = os.Remove(socketPath)
		reason := "connect App Server: " + err.Error()
		if connected {
			reason = "start thread: " + err.Error()
		}
		return codexMonitorSetupFailure(config, deps, reason, executable, args)
	}

	bridge := newCodexMonitorBridge(app, thread.ID)
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
		return codexMonitorSetupFailure(config, deps, "register monitor: "+err.Error(), executable, args)
	}

	bridgeDone := make(chan error, 1)
	go func() { bridgeDone <- bridge.Run(monitorCtx) }()
	watchDone := make(chan error, 1)
	go func() {
		if config.codexMonitorExplicit || config.codexMonitorAutomatic {
			watchDone <- deps.runWatchScoped(watchCtx, projectPath, scope, bridge)
			return
		}
		watchDone <- deps.runWatch(watchCtx, bridge)
	}()

	remoteArgs := make([]string, 0, len(args)+4)
	remoteArgs = append(remoteArgs, "--remote", "unix://"+socketPath, "resume", thread.ID)
	remoteArgs = append(remoteArgs, args...)
	tuiProcess, err := deps.startProcess(codexMonitorTUI, executable, remoteArgs)
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
		return codexMonitorSetupFailure(config, deps, "start Codex TUI: "+err.Error(), executable, args)
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
				disableMonitor(err)
			}
			watchDone = nil
		}
	}
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

// Explicit role launches carry an ATCT scope contract. Starting plain Codex
// after any monitor setup failure would silently discard that contract, so only
// the legacy no-role path may use the historical fallback.
func codexMonitorSetupFailure(config cliConfig, deps codexMonitorDeps, reason, executable string, args []string) (int, error) {
	if config.codexMonitorExplicit {
		return 1, errors.New(reason)
	}
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
	if deps.connectAppServer == nil {
		deps.connectAppServer = connectCodexAppServer
	}
	if deps.projectPath == nil {
		deps.projectPath = codexMonitorProjectPath
	}
	if deps.resolveScope == nil {
		deps.resolveScope = func(ctx context.Context, cwd string, scope watchScope) (watchScope, error) {
			return resolveCodexMonitorScope(ctx, dir, cwd, scope)
		}
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
	if deps.stderr == nil {
		deps.stderr = os.Stderr
	}
	if deps.runWatch == nil {
		deps.runWatch = func(ctx context.Context, bridge *codexMonitorBridge) error {
			projectPath, err := deps.projectPath()
			if err != nil {
				return fmt.Errorf("resolve project directory: %w", err)
			}
			return runCodexMonitorWatch(ctx, &http.Client{}, watchBaseURLs(dir), projectPath, bridge)
		}
	}
	if deps.runWatchScoped == nil {
		deps.runWatchScoped = func(ctx context.Context, projectPath string, scope watchScope, bridge *codexMonitorBridge) error {
			return runCodexMonitorWatchScoped(ctx, &http.Client{}, watchBaseURLs(dir), projectPath, scope, bridge)
		}
	}
	return deps
}

func resolveCodexMonitorScope(ctx context.Context, dir, cwd string, scope watchScope) (watchScope, error) {
	client := &http.Client{Timeout: codexMonitorSetupTimeout}
	return resolveCodexMonitorScopeWithClient(ctx, client, watchBaseURLs(dir), cwd, scope)
}

func resolveCodexMonitorScopeWithClient(ctx context.Context, client *http.Client, bases []string, cwd string, scope watchScope) (watchScope, error) {
	for _, base := range bases {
		projects, err := fetchWatchProjects(ctx, client, base)
		if err != nil {
			continue
		}
		projectID := resolveWatchProjectID(cwd, projects)
		if projectID == "" {
			continue
		}
		if scope.Role == "commander" {
			scope.ProjectID = projectID
			return scope, nil
		}
		goalID := scope.GoalID
		if scope.Role == "executor" {
			var taskPayload struct {
				Task struct {
					ID int64 `json:"id"`
				} `json:"task"`
				Goal struct {
					ID int64 `json:"id"`
				} `json:"goal"`
			}
			if err := fetchCodexMonitorJSON(ctx, client, base, "/api/tasks/"+scope.TaskID, &taskPayload); err != nil || taskPayload.Task.ID == 0 || taskPayload.Goal.ID == 0 {
				continue
			}
			scope.TaskID = strconv.FormatInt(taskPayload.Task.ID, 10)
			goalID = strconv.FormatInt(taskPayload.Goal.ID, 10)
		}
		var goalPayload struct {
			Goal struct {
				ID        int64 `json:"id"`
				ProjectID int64 `json:"project_id"`
			} `json:"goal"`
		}
		if err := fetchCodexMonitorJSON(ctx, client, base, "/api/goals/"+goalID, &goalPayload); err != nil || goalPayload.Goal.ID == 0 || goalPayload.Goal.ProjectID == 0 {
			continue
		}
		if strconv.FormatInt(goalPayload.Goal.ProjectID, 10) != projectID {
			continue
		}
		scope.GoalID = strconv.FormatInt(goalPayload.Goal.ID, 10)
		scope.ProjectID = projectID
		return scope, nil
	}
	return watchScope{}, errors.New("selector is unresolved in the current project")
}

func fetchCodexMonitorJSON(ctx context.Context, client *http.Client, base, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GET %s: HTTP %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(target)
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

func startCodexMonitorProcess(kind codexMonitorProcessKind, executable string, args []string) (codexMonitorProcess, error) {
	cmd := exec.Command(executable, args...)
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
