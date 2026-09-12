package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemonctl"
)

func TestCodexMonitorFallbackUsesOriginalCommandAndArguments(t *testing.T) {
	var stderr safeCodexMonitorBuffer
	var gotExecutable string
	var gotArgs []string
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) {
			return "", errors.New("codex not found")
		},
		runNormal: func(executable string, args []string) (int, error) {
			gotExecutable = executable
			gotArgs = append([]string(nil), args...)
			return 23, nil
		},
		stderr: &stderr,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction: "monitor",
		codexArgs:          []string{"-m", "gpt-5", "--config", "a=b"},
	}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 23 {
		t.Fatalf("exit code = %d, want 23", code)
	}
	if gotExecutable != "codex" {
		t.Fatalf("fallback executable = %q, want codex", gotExecutable)
	}
	if want := []string{"-m", "gpt-5", "--config", "a=b"}; !slices.Equal(gotArgs, want) {
		t.Fatalf("fallback args = %#v, want %#v", gotArgs, want)
	}
	wantWarning := "atct codex monitor disabled: codex not found; running normal codex\n"
	if got := stderr.String(); got != wantWarning {
		t.Fatalf("fallback warning = %q, want %q", got, wantWarning)
	}
}

func TestCodexMonitorPassThroughExecDoesNotStartMonitor(t *testing.T) {
	var started bool
	var gotExecutable string
	var gotArgs []string
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		runNormal: func(executable string, args []string) (int, error) {
			gotExecutable = executable
			gotArgs = append([]string(nil), args...)
			return 0, nil
		},
		startProcess: func(codexMonitorProcessKind, string, []string, []string) (codexMonitorProcess, error) {
			started = true
			return nil, errors.New("monitor process should not start")
		},
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction:      "monitor",
		codexMonitorPassthrough: true,
		codexArgs:               []string{"exec", "--help"},
	}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if started {
		t.Fatal("pass-through started monitor processes")
	}
	if gotExecutable != "/opt/codex" {
		t.Fatalf("pass-through executable = %q, want /opt/codex", gotExecutable)
	}
	if want := []string{"exec", "--help"}; !slices.Equal(gotArgs, want) {
		t.Fatalf("pass-through args = %#v, want %#v", gotArgs, want)
	}
}

func TestCodexMonitorDirectResolutionSkipsMarkedShim(t *testing.T) {
	shimDir := t.TempDir()
	markedShim := filepath.Join(shimDir, "codex")
	if err := os.WriteFile(markedShim, []byte("#!/bin/sh\n"+codexShimMarker+"\n"), 0o700); err != nil {
		t.Fatalf("write marked Codex shim: %v", err)
	}
	realDir := t.TempDir()
	realCodex := filepath.Join(realDir, "codex")
	if err := os.WriteFile(realCodex, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write real Codex executable: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)

	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(0)
	tui.markStarted()
	tuiStarted := make(chan struct{})
	done := make(chan struct{})
	var (
		appExecutable string
		tuiExecutable string
		code          int
		runErr        error
	)
	deps := codexMonitorDeps{
		startProcess: func(kind codexMonitorProcessKind, executable string, _ []string, _ []string) (codexMonitorProcess, error) {
			switch kind {
			case codexMonitorAppServer:
				appExecutable = executable
				return app, nil
			case codexMonitorTUI:
				tuiExecutable = executable
				close(tuiStarted)
				return tui, nil
			default:
				return nil, errors.New("unexpected process kind")
			}
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		reap:             func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register: func(string, daemonctl.CodexMonitorRecord) (func(), error) {
			return func() {}, nil
		},
		runBoundWatch: func(ctx context.Context, _ string, _ string, _ *codexMonitorBridge) error {
			<-ctx.Done()
			return nil
		},
		stderr: io.Discard,
	}

	go func() {
		code, runErr = runCodexMonitorWithDeps(cliConfig{
			codexMonitorAction: "monitor",
			codexArgs:          []string{"--model", "gpt-5"},
		}, monitorDir, deps)
		close(done)
	}()

	select {
	case <-tuiStarted:
	case <-time.After(time.Second):
		t.Fatal("direct monitor did not start TUI")
	}
	if appExecutable != realCodex || tuiExecutable != realCodex {
		t.Fatalf("direct monitor executables = (%q, %q), want real Codex %q", appExecutable, tuiExecutable, realCodex)
	}
	tui.finish()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("direct monitor did not finish after TUI exit")
	}
	if runErr != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", runErr)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestCodexMonitorFallbackWhenAppServerCannotStart(t *testing.T) {
	var stderr safeCodexMonitorBuffer
	var gotArgs []string
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(codexMonitorProcessKind, string, []string, []string) (codexMonitorProcess, error) {
			return nil, errors.New("permission denied")
		},
		runNormal: func(_ string, args []string) (int, error) {
			gotArgs = append([]string(nil), args...)
			return 17, nil
		},
		stderr: &stderr,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction: "monitor",
		codexArgs:          []string{"--model", "gpt-5"},
	}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 17 {
		t.Fatalf("exit code = %d, want 17", code)
	}
	if want := []string{"--model", "gpt-5"}; !slices.Equal(gotArgs, want) {
		t.Fatalf("fallback args = %#v, want %#v", gotArgs, want)
	}
	wantWarning := "atct codex monitor disabled: start App Server: permission denied; running normal codex\n"
	if got := stderr.String(); got != wantWarning {
		t.Fatalf("fallback warning = %q, want %q", got, wantWarning)
	}
}

func TestCodexMonitorGenericLifecycleInjectsTokenAndStartsBoundWatch(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(0)
	tui.markStarted()
	watchStarted := make(chan struct{})
	tuiStarted := make(chan struct{})
	done := make(chan struct{})
	var (
		code       int
		runErr     error
		watchPath  string
		watchToken string
		tuiEnv     []string
	)
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string, env []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return app, nil
			}
			tuiEnv = append([]string(nil), env...)
			close(tuiStarted)
			return tui, nil
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		atctExecutable:   func() (string, error) { return "/opt/atct", nil },
		newMonitorToken:  func() (string, error) { return "token-1", nil },
		reap:             func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register: func(string, daemonctl.CodexMonitorRecord) (func(), error) {
			return func() {}, nil
		},
		runBoundWatch: func(ctx context.Context, projectPath, token string, _ *codexMonitorBridge) error {
			watchPath = projectPath
			watchToken = token
			close(watchStarted)
			<-ctx.Done()
			return nil
		},
		stderr: io.Discard,
	}

	go func() {
		code, runErr = runCodexMonitorWithDeps(cliConfig{
			codexMonitorAction: "monitor",
			codexArgs:          []string{"-m", "gpt-5"},
		}, monitorDir, deps)
		close(done)
	}()

	select {
	case <-watchStarted:
	case <-time.After(time.Second):
		t.Fatal("generic monitor did not start a bound watcher")
	}
	select {
	case <-tuiStarted:
	case <-time.After(time.Second):
		t.Fatal("generic monitor did not start its TUI")
	}
	if watchPath != "/project" || watchToken != "token-1" {
		t.Fatalf("bound watch = (%q, %q), want (/project, token-1)", watchPath, watchToken)
	}
	if want := []string{"ATCT_BIN=/opt/atct", "ATCT_MONITOR_TOKEN=token-1"}; !slices.Equal(tuiEnv, want) {
		t.Fatalf("TUI environment = %#v, want %#v", tuiEnv, want)
	}
	tui.finish()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automatic monitor did not finish after TUI exit")
	}
	if runErr != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", runErr)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestCodexMonitorAutomaticSetupFailureFallsBack(t *testing.T) {
	var (
		normalCalls   int
		gotExecutable string
		gotArgs       []string
	)
	deps := codexMonitorDeps{
		projectPath: func() (string, error) { return "/project", nil },
		reap: func(string) (daemonctl.CodexMonitorReapResult, error) {
			return daemonctl.CodexMonitorReapResult{}, errors.New("registry unavailable")
		},
		resolveCodex: func() (string, error) {
			return "/opt/codex", nil
		},
		runNormal: func(executable string, args []string) (int, error) {
			normalCalls++
			gotExecutable = executable
			gotArgs = append([]string(nil), args...)
			return 41, nil
		},
		stderr: io.Discard,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction:    "monitor",
		codexMonitorAutomatic: true,
		codexArgs:             []string{"resume", "thread-2"},
	}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 41 || normalCalls != 1 || gotExecutable != "codex" {
		t.Fatalf("automatic fallback = (%d, %d, %q), want (41, 1, codex)", code, normalCalls, gotExecutable)
	}
	if !slices.Equal(gotArgs, []string{"resume", "thread-2"}) {
		t.Fatalf("automatic fallback args = %#v, want original args", gotArgs)
	}
}

func TestCodexMonitorAutomaticAppServerFailureFallsBackOnce(t *testing.T) {
	var (
		appServerStarts int
		tuiStarts       int
		normalCalls     int
		gotArgs         []string
	)
	deps := codexMonitorDeps{
		projectPath: func() (string, error) { return "/project", nil },
		reap: func(string) (daemonctl.CodexMonitorReapResult, error) {
			return daemonctl.CodexMonitorReapResult{}, nil
		},
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string, _ []string) (codexMonitorProcess, error) {
			switch kind {
			case codexMonitorAppServer:
				appServerStarts++
				return nil, errors.New("App Server unavailable")
			case codexMonitorTUI:
				tuiStarts++
				return nil, errors.New("TUI must not start after App Server failure")
			default:
				return nil, errors.New("unexpected process kind")
			}
		},
		runNormal: func(_ string, args []string) (int, error) {
			normalCalls++
			gotArgs = append([]string(nil), args...)
			return 47, nil
		},
		stderr: io.Discard,
	}

	wantArgs := []string{"resume", "thread-3", "--last", "value with spaces"}
	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction:    "monitor",
		codexMonitorAutomatic: true,
		codexArgs:             wantArgs,
	}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 47 || normalCalls != 1 {
		t.Fatalf("automatic setup fallback = (code %d, normal calls %d), want (47, 1)", code, normalCalls)
	}
	if appServerStarts != 1 || tuiStarts != 0 {
		t.Fatalf("monitor child starts = (App Server %d, TUI %d), want (1, 0)", appServerStarts, tuiStarts)
	}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("automatic fallback args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestCodexMonitorTUIStartFailureStopsMonitorGoroutines(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	watchStopped := make(chan struct{})
	var gotArgs []string
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, args []string, _ []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return app, nil
			}
			gotArgs = append([]string(nil), args...)
			return nil, errors.New("terminal unavailable")
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		runBoundWatch: func(ctx context.Context, _ string, _ string, _ *codexMonitorBridge) error {
			<-ctx.Done()
			close(watchStopped)
			return nil
		},
		runNormal: func(_ string, args []string) (int, error) {
			gotArgs = append([]string(nil), args...)
			return 19, nil
		},
		stderr: io.Discard,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction: "monitor",
		codexArgs:          []string{"--model", "gpt-5"},
	}, monitorDir, deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 19 {
		t.Fatalf("exit code = %d, want 19", code)
	}
	select {
	case <-watchStopped:
	default:
		t.Fatal("watcher was still running after TUI startup failure")
	}
	if !app.signaled() {
		t.Fatal("App Server was not stopped after TUI startup failure")
	}
	if want := []string{"--model", "gpt-5"}; !slices.Equal(gotArgs, want) {
		t.Fatalf("fallback args = %#v, want %#v", gotArgs, want)
	}
}

func TestCodexMonitorStopUsesCurrentProjectOnly(t *testing.T) {
	var gotProject string
	var stderr safeCodexMonitorBuffer
	deps := codexMonitorDeps{
		projectPath: func() (string, error) { return "/project", nil },
		stopMonitors: func(_ string, project string) (daemonctl.CodexMonitorStopResult, error) {
			gotProject = project
			return daemonctl.CodexMonitorStopResult{
				Stopped: []daemonctl.CodexMonitorRecord{{SupervisorPID: 1001}},
			}, nil
		},
		stderr: &stderr,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{codexMonitorAction: "stop"}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if gotProject != "/project" {
		t.Fatalf("stop project = %q, want /project", gotProject)
	}
	if !strings.Contains(stderr.String(), "stopped 1 monitor") {
		t.Fatalf("stop output = %q, want stopped message", stderr.String())
	}
}

func TestCodexMonitorStopReportsPartialFailure(t *testing.T) {
	var stderr safeCodexMonitorBuffer
	deps := codexMonitorDeps{
		projectPath: func() (string, error) { return "/project", nil },
		stopMonitors: func(_ string, _ string) (daemonctl.CodexMonitorStopResult, error) {
			return daemonctl.CodexMonitorStopResult{
				Stopped: []daemonctl.CodexMonitorRecord{{SupervisorPID: 1001}},
				Failed:  []int{1002},
			}, nil
		},
		stderr: &stderr,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{codexMonitorAction: "stop"}, t.TempDir(), deps)
	if err == nil {
		t.Fatal("runCodexMonitorWithDeps returned nil error for partial stop failure")
	}
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "failed to stop supervisor 1002") {
		t.Fatalf("stop output = %q, want failed supervisor warning", stderr.String())
	}
}

func TestCodexMonitorWatchDiagnosticsAreDiscardedByHealthGate(t *testing.T) {
	var out codexMonitorWatchOutput
	for _, line := range []string{
		"atct watch: connection unavailable; reconnecting in 5s\n",
		"atct watch: daemon keepalive missing for 90s\n",
		"atct watch: daemon ensure failed 5 consecutive times; continuing connection retries\n",
	} {
		if got, err := out.Write([]byte(line)); err != nil {
			t.Fatalf("watch diagnostic %q write error = %v, want nil", line, err)
		} else if got != len(line) {
			t.Fatalf("watch diagnostic %q wrote %d bytes, want %d", line, got, len(line))
		}
	}
	if got, err := out.Write([]byte("atct decision approved (decision_id: d1)\n")); err != nil {
		t.Fatalf("action line write error = %v, want nil", err)
	} else if got != len("atct decision approved (decision_id: d1)\n") {
		t.Fatalf("action line wrote %d bytes, want %d", got, len("atct decision approved (decision_id: d1)\n"))
	}
}

func TestCodexMonitorLifecycleCleansChildrenAndPreservesTUIStatus(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(73)
	tui.markStarted()
	app.thread = codexThread{ID: "thread-new", CWD: "/project", Status: codexThreadStatus{Type: "idle"}}
	var appArgs, tuiArgs []string
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, executable string, args []string, _ []string) (codexMonitorProcess, error) {
			switch kind {
			case codexMonitorAppServer:
				appArgs = append([]string(nil), args...)
				return app, nil
			case codexMonitorTUI:
				tuiArgs = append([]string(nil), args...)
				tui.finish()
				return tui, nil
			default:
				return nil, errors.New("unexpected process kind")
			}
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		runBoundWatch: func(ctx context.Context, _ string, _ string, _ *codexMonitorBridge) error {
			<-ctx.Done()
			return nil
		},
		stderr: io.Discard,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction: "monitor",
		codexArgs:          []string{"-m", "gpt-5"},
	}, monitorDir, deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 73 {
		t.Fatalf("exit code = %d, want 73", code)
	}
	if len(appArgs) != 3 || appArgs[0] != "app-server" || appArgs[1] != "--listen" || !strings.HasPrefix(appArgs[2], "unix://"+filepath.Join(monitorDir, "codex-monitors")+string(filepath.Separator)) {
		t.Fatalf("App Server args = %#v, want app-server --listen unix://<managed-dir>/codex-monitors/<pid>.sock", appArgs)
	}
	if len(tuiArgs) != 4 || tuiArgs[0] != "--remote" || !strings.HasPrefix(tuiArgs[1], "unix://"+filepath.Join(monitorDir, "codex-monitors")+string(filepath.Separator)) || !slices.Equal(tuiArgs[2:], []string{"-m", "gpt-5"}) {
		t.Fatalf("TUI args = %#v, want --remote socket followed by original args", tuiArgs)
	}
	if !app.signaled() {
		t.Fatal("App Server was not stopped during cleanup")
	}
	if !tui.started() {
		t.Fatal("TUI process was not started")
	}
	if records, err := daemonctl.ListCodexMonitors(monitorDir); err != nil {
		t.Fatalf("ListCodexMonitors: %v", err)
	} else if len(records) != 0 {
		t.Fatalf("monitor records after cleanup = %#v, want empty", records)
	}
}

func TestCodexMonitorEnvironmentRequiresAndInjectsToken(t *testing.T) {
	got, err := codexMonitorEnvironment("token-1", func() (string, error) { return "/opt/atct", nil })
	if err != nil {
		t.Fatalf("codexMonitorEnvironment: %v", err)
	}
	if want := []string{"ATCT_BIN=/opt/atct", "ATCT_MONITOR_TOKEN=token-1"}; !slices.Equal(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
	if _, err := codexMonitorEnvironment("", func() (string, error) { return "/opt/atct", nil }); err == nil {
		t.Fatal("empty monitor token succeeded")
	}
}

func TestCodexMonitorLegacyResumePassesThroughWithoutStartingMonitor(t *testing.T) {
	var (
		appStarts     int
		tuiStarts     int
		normalCalls   int
		gotExecutable string
		gotArgs       []string
	)
	args := []string{"resume", "thread-existing", "--last"}
	app := newFakeCodexMonitorApp()
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) {
			t.Fatal("legacy resume resolved a monitor Codex executable")
			return "", nil
		},
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string, _ []string) (codexMonitorProcess, error) {
			switch kind {
			case codexMonitorAppServer:
				appStarts++
				return app, nil
			case codexMonitorTUI:
				tuiStarts++
				return nil, errors.New("legacy resume must not start TUI")
			default:
				return nil, errors.New("unexpected process kind")
			}
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) {
			t.Fatal("legacy resume connected to App Server")
			return nil, nil
		},
		projectPath: func() (string, error) {
			t.Fatal("legacy resume resolved project path")
			return "", nil
		},
		runNormal: func(executable string, got []string) (int, error) {
			normalCalls++
			gotExecutable = executable
			gotArgs = append([]string(nil), got...)
			return 29, nil
		},
		stderr: io.Discard,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction: "monitor",
		codexArgs:          args,
	}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 29 {
		t.Fatalf("exit code = %d, want 29", code)
	}
	if appStarts != 0 || tuiStarts != 0 {
		t.Fatalf("monitor process starts = (App Server %d, TUI %d), want (0, 0)", appStarts, tuiStarts)
	}
	if normalCalls != 1 {
		t.Fatalf("normal calls = %d, want 1", normalCalls)
	}
	if gotExecutable != "codex" {
		t.Fatalf("normal executable = %q, want codex", gotExecutable)
	}
	if !slices.Equal(gotArgs, args) {
		t.Fatalf("normal args = %#v, want %#v", gotArgs, args)
	}
}

func TestCodexMonitorAdoptsThreadStartedByItsDedicatedRemoteTUI(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(0)
	tuiStarted := make(chan struct{})
	notificationDelivered := make(chan struct{})
	turnStarted := make(chan string, 1)
	var tuiArgs []string

	notificationThread := codexThread{
		ID:         "thread-from-notification",
		CWD:        "/project",
		Source:     "cli",
		SourceKind: "cli",
		Status:     codexThreadStatus{Type: "idle"},
	}
	notificationParams, err := json.Marshal(struct {
		Thread codexThread `json:"thread"`
	}{Thread: notificationThread})
	if err != nil {
		t.Fatalf("marshal thread/started notification: %v", err)
	}
	app.notifications = make(chan codexAppServerNotification, 1)
	app.notificationDelivered = notificationDelivered
	app.notifications <- codexAppServerNotification{
		Method: "thread/started",
		Params: notificationParams,
	}
	app.startTurn = func(_ context.Context, threadID, _ string) (codexTurn, error) {
		turnStarted <- threadID
		return codexTurn{ID: "turn-test"}, nil
	}

	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, args []string, _ []string) (codexMonitorProcess, error) {
			switch kind {
			case codexMonitorAppServer:
				return app, nil
			case codexMonitorTUI:
				tuiArgs = append([]string(nil), args...)
				tui.markStarted()
				close(tuiStarted)
				return tui, nil
			default:
				return nil, errors.New("unexpected process kind")
			}
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		reap:             func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register:         func(string, daemonctl.CodexMonitorRecord) (func(), error) { return func() {}, nil },
		runBoundWatch: func(ctx context.Context, _ string, _ string, bridge *codexMonitorBridge) error {
			if err := bridge.Enqueue(ctx, "atct goal created (goal_id: 7)"); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		},
		stderr: io.Discard,
	}

	type monitorResult struct {
		code int
		err  error
	}
	done := make(chan monitorResult, 1)
	go func() {
		code, err := runCodexMonitorWithDeps(cliConfig{codexMonitorAction: "monitor"}, monitorDir, deps)
		done <- monitorResult{code: code, err: err}
	}()

	select {
	case <-tuiStarted:
	case <-time.After(time.Second):
		t.Fatal("TUI did not start")
	}
	select {
	case <-notificationDelivered:
	case <-time.After(time.Second):
		t.Fatal("thread/started notification was not delivered")
	}
	select {
	case got := <-turnStarted:
		if got != "thread-from-notification" {
			t.Fatalf("turn started for thread %q, want thread-from-notification", got)
		}
	case <-time.After(time.Second):
		t.Fatal("queued monitor event did not start a turn for the TUI thread")
	}
	if len(tuiArgs) != 2 || tuiArgs[0] != "--remote" || !strings.HasPrefix(tuiArgs[1], "unix://") {
		t.Fatalf("TUI args = %#v, want only --remote unix://<socket>", tuiArgs)
	}

	tui.finish()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("runCodexMonitorWithDeps: %v", result.err)
		}
		if result.code != 0 {
			t.Fatalf("exit code = %d, want 0", result.code)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor did not finish after TUI exit")
	}
}

func TestCodexMonitorBridgeFailureLeavesTUIAlive(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	app.notificationErr = errors.New("App Server connection lost")
	tui := newFakeCodexMonitorProcess(0)
	tui.markStarted()
	watchStopped := make(chan struct{})
	var stderr safeCodexMonitorBuffer
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string, _ []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return app, nil
			}
			return tui, nil
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		runBoundWatch: func(ctx context.Context, _ string, _ string, _ *codexMonitorBridge) error {
			<-ctx.Done()
			close(watchStopped)
			return nil
		},
		stderr: &stderr,
	}

	done := make(chan struct{})
	var code int
	var runErr error
	go func() {
		code, runErr = runCodexMonitorWithDeps(cliConfig{codexMonitorAction: "monitor"}, monitorDir, deps)
		close(done)
	}()
	if !stderr.waitFor("Codex session remains active", time.Second) {
		t.Fatal("post-launch warning was not written")
	}
	if tui.signaled() {
		t.Fatal("TUI was stopped after bridge failure")
	}
	select {
	case <-watchStopped:
	case <-time.After(time.Second):
		t.Fatal("watcher was not stopped after bridge failure")
	}
	tui.finish()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not finish after TUI exit")
	}
	if runErr != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", runErr)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestCodexMonitorKeepsTUIAliveDuringRecoverableWatchFailure(t *testing.T) {
	if codexMonitorWatchErrorIsTerminal(errors.New("temporary SSE disconnect")) {
		t.Fatal("recoverable watch failure was classified as terminal")
	}
}

func TestCodexMonitorDisablesOnlyForTerminalWatchSinkError(t *testing.T) {
	if !codexMonitorWatchErrorIsTerminal(&watchSinkError{err: errors.New("bridge disabled")}) {
		t.Fatal("terminal watch sink failure was not classified as terminal")
	}
}

type fakeCodexMonitorProcess struct {
	mu           sync.Mutex
	waitCh       chan error
	exitCode     int
	startedFlag  bool
	signaledFlag bool
}

func newFakeCodexMonitorProcess(exitCode int) *fakeCodexMonitorProcess {
	return &fakeCodexMonitorProcess{waitCh: make(chan error, 1), exitCode: exitCode}
}

func (p *fakeCodexMonitorProcess) PID() int { return 90000 + p.exitCode + 1 }

func (p *fakeCodexMonitorProcess) Wait() error { return <-p.waitCh }

func (p *fakeCodexMonitorProcess) Signal(os.Signal) error {
	p.mu.Lock()
	if !p.signaledFlag {
		p.signaledFlag = true
		select {
		case p.waitCh <- nil:
		default:
		}
	}
	p.mu.Unlock()
	return nil
}

func (p *fakeCodexMonitorProcess) ExitCode() int { return p.exitCode }

func (p *fakeCodexMonitorProcess) markStarted() {
	p.mu.Lock()
	p.startedFlag = true
	p.mu.Unlock()
}

func (p *fakeCodexMonitorProcess) finish() {
	p.mu.Lock()
	p.mu.Unlock()
	p.waitCh <- nil
}

func (p *fakeCodexMonitorProcess) started() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startedFlag
}

func (p *fakeCodexMonitorProcess) signaled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.signaledFlag
}

type fakeCodexMonitorApp struct {
	process               *fakeCodexMonitorProcess
	thread                codexThread
	notificationErr       error
	notifications         chan codexAppServerNotification
	notificationDelivered chan struct{}
	onDiscover            func()
	onDiscoverBefore      func(map[string]struct{})
	onResume              func(string)
	startTurn             func(context.Context, string, string) (codexTurn, error)
	listThreadsPage       func(context.Context, string, *string) (codexThreadListPage, error)
}

func newFakeCodexMonitorApp() *fakeCodexMonitorApp {
	return &fakeCodexMonitorApp{process: newFakeCodexMonitorProcess(0), thread: codexThread{ID: "thread-new", CWD: "/project"}}
}

func (a *fakeCodexMonitorApp) Initialize(context.Context) error { return nil }

func (a *fakeCodexMonitorApp) ListThreads(ctx context.Context, cwd string) ([]codexThread, error) {
	if a.listThreadsPage == nil {
		return []codexThread{{ID: "thread-existing", CWD: "/project"}}, nil
	}
	var all []codexThread
	var cursor *string
	for {
		page, err := a.ListThreadsPage(ctx, cwd, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Threads...)
		if page.NextCursor == nil || strings.TrimSpace(*page.NextCursor) == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

func (a *fakeCodexMonitorApp) ListThreadsPage(ctx context.Context, cwd string, cursor *string) (codexThreadListPage, error) {
	if a.listThreadsPage != nil {
		return a.listThreadsPage(ctx, cwd, cursor)
	}
	return codexThreadListPage{Threads: []codexThread{{ID: "thread-existing", CWD: "/project"}}}, nil
}

func (a *fakeCodexMonitorApp) DiscoverThread(_ context.Context, _ string, before map[string]struct{}, _ time.Duration, _ time.Duration) (codexThread, error) {
	if a.onDiscoverBefore != nil {
		a.onDiscoverBefore(before)
	}
	if a.onDiscover != nil {
		a.onDiscover()
	}
	return a.thread, nil
}

func (a *fakeCodexMonitorApp) ResumeThread(_ context.Context, threadID string) error {
	if a.onResume != nil {
		a.onResume(threadID)
	}
	return nil
}

func (a *fakeCodexMonitorApp) StartTurn(ctx context.Context, threadID, input string) (codexTurn, error) {
	if a.startTurn != nil {
		return a.startTurn(ctx, threadID, input)
	}
	return codexTurn{ID: "turn-test"}, nil
}

func (a *fakeCodexMonitorApp) NextNotification(ctx context.Context) (codexAppServerNotification, error) {
	if a.notificationErr != nil {
		return codexAppServerNotification{}, a.notificationErr
	}
	if a.notifications != nil {
		select {
		case notification := <-a.notifications:
			if a.notificationDelivered != nil {
				close(a.notificationDelivered)
				a.notificationDelivered = nil
			}
			return notification, nil
		case <-ctx.Done():
			return codexAppServerNotification{}, ctx.Err()
		}
	}
	<-ctx.Done()
	return codexAppServerNotification{}, ctx.Err()
}

func (a *fakeCodexMonitorApp) Close() error { return nil }

func (a *fakeCodexMonitorApp) Err() error { return a.notificationErr }

func (a *fakeCodexMonitorApp) PID() int { return a.process.PID() }

func (a *fakeCodexMonitorApp) Wait() error { return a.process.Wait() }

func (a *fakeCodexMonitorApp) Signal(signal os.Signal) error { return a.process.Signal(signal) }

func (a *fakeCodexMonitorApp) ExitCode() int { return a.process.ExitCode() }

func (a *fakeCodexMonitorApp) signaled() bool { return a.process.signaled() }

type safeCodexMonitorBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeCodexMonitorBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeCodexMonitorBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *safeCodexMonitorBuffer) waitFor(want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), want) {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return strings.Contains(b.String(), want)
}
