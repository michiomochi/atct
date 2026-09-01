package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

func TestCodexMonitorExplicitRoleFailureDoesNotLaunchCodexOrAppServer(t *testing.T) {
	config, err := parseArgs([]string{"codex", "monitor", "--role", "executor", "--task", "999999"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	var appServerStarts, normalStarts int
	deps := codexMonitorDeps{
		projectPath:  func() (string, error) { return "/project", nil },
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				appServerStarts++
			}
			return nil, errors.New("must not launch")
		},
		runNormal: func(_ string, _ []string) (int, error) {
			normalStarts++
			return 0, nil
		},
		stderr: io.Discard,
	}

	if _, err := runCodexMonitorWithDeps(config, t.TempDir(), deps); err == nil {
		t.Fatal("explicit unresolved executor task succeeded")
	}
	if appServerStarts != 0 || normalStarts != 0 {
		t.Fatalf("explicit configuration started app server=%d normal Codex=%d, want neither", appServerStarts, normalStarts)
	}
}

func TestCodexMonitorExplicitSetupFailureDoesNotFallBack(t *testing.T) {
	var normalStarts int
	deps := codexMonitorDeps{
		projectPath: func() (string, error) { return "/project", nil },
		resolveScope: func(context.Context, string, watchScope) (watchScope, error) {
			return watchScope{Role: "executor", ProjectID: "7", GoalID: "16", TaskID: "46"}, nil
		},
		reap: func(string) (daemonctl.CodexMonitorReapResult, error) {
			return daemonctl.CodexMonitorReapResult{}, errors.New("registry unavailable")
		},
		runNormal: func(string, []string) (int, error) {
			normalStarts++
			return 0, nil
		},
		stderr: io.Discard,
	}
	_, err := runCodexMonitorWithDeps(cliConfig{codexMonitorAction: "monitor", codexMonitorExplicit: true, codexMonitorRole: "executor", codexMonitorTaskID: "46"}, t.TempDir(), deps)
	if err == nil || !strings.Contains(err.Error(), "reap monitor records: registry unavailable") {
		t.Fatalf("explicit setup failure error = %v, want reap failure", err)
	}
	if normalStarts != 0 {
		t.Fatalf("explicit setup failure started normal Codex %d times, want 0", normalStarts)
	}
}

func TestResolveCodexMonitorExecutorScopeUsesTaskAndGoalResponseShapes(t *testing.T) {
	for _, tt := range []struct {
		name      string
		projectID int64
		wantErr   bool
	}{
		{name: "current project", projectID: 7},
		{name: "other project", projectID: 8, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: watchRoundTripper(func(req *http.Request) (*http.Response, error) {
				body := ""
				switch req.URL.Path {
				case "/api/projects":
					body = `[{"id":7,"root_path":"/project"}]`
				case "/api/tasks/46":
					body = `{"task":{"id":46},"goal":{"id":16,"project_name":"current"}}`
				case "/api/goals/16":
					body = fmt.Sprintf(`{"goal":{"id":16,"project_id":%d}}`, tt.projectID)
				default:
					t.Fatalf("unexpected request %s", req.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}

			scope, err := resolveCodexMonitorScopeWithClient(context.Background(), client, []string{"http://daemon"}, "/project/worktree", watchScope{Role: "executor", TaskID: "46"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveCodexMonitorScopeWithClient() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && (scope.ProjectID != "7" || scope.GoalID != "16" || scope.TaskID != "46") {
				t.Fatalf("scope = %#v, want resolved current-project task scope", scope)
			}
		})
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
		startProcess: func(codexMonitorProcessKind, string, []string) (codexMonitorProcess, error) {
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
		startProcess: func(kind codexMonitorProcessKind, executable string, _ []string) (codexMonitorProcess, error) {
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
		runWatch: func(ctx context.Context, _ *codexMonitorBridge) error {
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
		startProcess: func(codexMonitorProcessKind, string, []string) (codexMonitorProcess, error) {
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

func TestCodexMonitorAutomaticUsesCommanderProjectScope(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(0)
	tui.markStarted()
	scopeSeen := make(chan watchScope, 1)
	done := make(chan struct{})
	var (
		code   int
		runErr error
	)
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return app, nil
			}
			return tui, nil
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		reap:             func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register: func(string, daemonctl.CodexMonitorRecord) (func(), error) {
			return func() {}, nil
		},
		runWatchScoped: func(ctx context.Context, cwd string, scope watchScope, _ *codexMonitorBridge) error {
			if cwd != "/project" {
				return fmt.Errorf("watch cwd = %q, want /project", cwd)
			}
			scopeSeen <- scope
			<-ctx.Done()
			return nil
		},
		runWatch: func(context.Context, *codexMonitorBridge) error {
			return errors.New("unscoped watcher must not run for automatic monitor")
		},
		stderr: io.Discard,
	}

	go func() {
		code, runErr = runCodexMonitorWithDeps(cliConfig{
			codexMonitorAction:    "monitor",
			codexMonitorAutomatic: true,
			codexMonitorRole:      "commander",
			codexMonitorProjectID: "42",
			codexArgs:             []string{"resume", "thread-1"},
		}, monitorDir, deps)
		close(done)
	}()

	var scope watchScope
	select {
	case scope = <-scopeSeen:
	case <-time.After(time.Second):
		t.Fatal("automatic monitor did not start a scoped watcher")
	}
	if scope != (watchScope{Role: "commander", ProjectID: "42"}) {
		t.Fatalf("automatic watcher scope = %#v, want commander project scope", scope)
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
		resolveScopes int
	)
	deps := codexMonitorDeps{
		projectPath: func() (string, error) { return "/project", nil },
		resolveScope: func(context.Context, string, watchScope) (watchScope, error) {
			resolveScopes++
			return watchScope{}, errors.New("automatic monitor must not resolve HTTP scope")
		},
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
		codexMonitorRole:      "commander",
		codexMonitorProjectID: "42",
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
	if resolveScopes != 0 {
		t.Fatalf("automatic monitor resolved explicit scopes %d times, want 0", resolveScopes)
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
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string) (codexMonitorProcess, error) {
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
		codexMonitorRole:      "commander",
		codexMonitorProjectID: "42",
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

func TestCodexMonitorAutomaticInvalidScopeFallsBack(t *testing.T) {
	tests := []struct {
		name   string
		config cliConfig
	}{
		{
			name: "worker role",
			config: cliConfig{
				codexMonitorAutomatic: true,
				codexMonitorRole:      "executor",
				codexMonitorProjectID: "42",
			},
		},
		{
			name: "missing project",
			config: cliConfig{
				codexMonitorAutomatic: true,
				codexMonitorRole:      "commander",
			},
		},
		{
			name: "goal selector mixed in",
			config: cliConfig{
				codexMonitorAutomatic: true,
				codexMonitorRole:      "commander",
				codexMonitorProjectID: "42",
				codexMonitorGoalID:    "16",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var normalCalls int
			deps := codexMonitorDeps{
				projectPath: func() (string, error) { return "/project", nil },
				reap: func(string) (daemonctl.CodexMonitorReapResult, error) {
					t.Fatal("invalid automatic scope reached monitor setup")
					return daemonctl.CodexMonitorReapResult{}, nil
				},
				resolveCodex: func() (string, error) {
					t.Fatal("invalid automatic scope resolved Codex")
					return "", nil
				},
				runNormal: func(string, []string) (int, error) {
					normalCalls++
					return 43, nil
				},
				stderr: io.Discard,
			}
			tt.config.codexMonitorAction = "monitor"
			code, err := runCodexMonitorWithDeps(tt.config, t.TempDir(), deps)
			if err != nil {
				t.Fatalf("runCodexMonitorWithDeps: %v", err)
			}
			if code != 43 || normalCalls != 1 {
				t.Fatalf("invalid automatic fallback = (%d, %d), want (43, 1)", code, normalCalls)
			}
		})
	}
}

func TestCodexMonitorTUIStartFailureStopsMonitorGoroutines(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	watchStopped := make(chan struct{})
	var gotArgs []string
	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, args []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return app, nil
			}
			gotArgs = append([]string(nil), args...)
			return nil, errors.New("terminal unavailable")
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		runWatch: func(ctx context.Context, _ *codexMonitorBridge) error {
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

func TestCodexMonitorWatchDiagnosticsReportSSEFailure(t *testing.T) {
	var out codexMonitorWatchOutput
	if _, err := out.Write([]byte("atct watch: connection unavailable; reconnecting in 5s\n")); err == nil {
		t.Fatal("watch diagnostic write error = nil, want SSE failure")
	}
	if _, err := out.Write([]byte("atct decision approved (decision_id: d1)\n")); err != nil {
		t.Fatalf("action line write error = %v, want nil", err)
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
		startProcess: func(kind codexMonitorProcessKind, executable string, args []string) (codexMonitorProcess, error) {
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
		runWatch: func(ctx context.Context, _ *codexMonitorBridge) error {
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
	if len(tuiArgs) != 6 || tuiArgs[0] != "--remote" || !strings.HasPrefix(tuiArgs[1], "unix://"+filepath.Join(monitorDir, "codex-monitors")+string(filepath.Separator)) || !slices.Equal(tuiArgs[2:], []string{"resume", "thread-new", "-m", "gpt-5"}) {
		t.Fatalf("TUI args = %#v, want --remote socket resume thread-new followed by original args", tuiArgs)
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

func TestCodexMonitorExplicitNonResumeStartsOwnedThreadAndPreservesArgs(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	app.thread = codexThread{
		ID:     "thread-from-start",
		CWD:    "/project",
		Status: codexThreadStatus{Type: "idle"},
	}
	startCWD := make(chan string, 1)
	app.startThread = func(_ context.Context, cwd string) (codexThread, error) {
		startCWD <- cwd
		return app.thread, nil
	}
	tui := newFakeCodexMonitorProcess(0)
	tuiStarted := make(chan struct{})
	var tuiArgs []string

	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, args []string) (codexMonitorProcess, error) {
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
		resolveScope: func(context.Context, string, watchScope) (watchScope, error) {
			return watchScope{Role: "executor", ProjectID: "7", GoalID: "216", TaskID: "920"}, nil
		},
		reap:     func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register: func(string, daemonctl.CodexMonitorRecord) (func(), error) { return func() {}, nil },
		runWatchScoped: func(ctx context.Context, _ string, _ watchScope, _ *codexMonitorBridge) error {
			<-ctx.Done()
			return nil
		},
		stderr: io.Discard,
	}

	done := make(chan struct{})
	var (
		code   int
		runErr error
	)
	go func() {
		code, runErr = runCodexMonitorWithDeps(cliConfig{
			codexMonitorAction:   "monitor",
			codexMonitorExplicit: true,
			codexMonitorRole:     "executor",
			codexMonitorTaskID:   "920",
			codexArgs:            []string{"-m", "gpt-5"},
		}, monitorDir, deps)
		close(done)
	}()

	select {
	case <-tuiStarted:
	case <-time.After(time.Second):
		t.Fatal("TUI did not start")
	}
	select {
	case got := <-startCWD:
		if got != "/project" {
			t.Fatalf("thread/start cwd = %q, want /project", got)
		}
	case <-time.After(time.Second):
		t.Fatal("thread/start was not called")
	}
	wantPrefix := []string{"--remote", "unix://"}
	if len(tuiArgs) != 6 || tuiArgs[0] != wantPrefix[0] || !strings.HasPrefix(tuiArgs[1], wantPrefix[1]) || !slices.Equal(tuiArgs[2:], []string{"resume", "thread-from-start", "-m", "gpt-5"}) {
		t.Fatalf("TUI args = %#v, want --remote socket resume thread-from-start followed by original args", tuiArgs)
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
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string) (codexMonitorProcess, error) {
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

func TestCodexMonitorExplicitLeadingResumeFailsBeforeStartingAnything(t *testing.T) {
	var (
		projectPathCalls  int
		scopeCalls        int
		reapCalls         int
		resolveCodexCalls int
		appStarts         int
		tuiStarts         int
		normalCalls       int
	)
	deps := codexMonitorDeps{
		projectPath: func() (string, error) {
			projectPathCalls++
			return "/project", nil
		},
		resolveScope: func(context.Context, string, watchScope) (watchScope, error) {
			scopeCalls++
			return watchScope{Role: "executor", ProjectID: "7", GoalID: "216", TaskID: "920"}, nil
		},
		reap: func(string) (daemonctl.CodexMonitorReapResult, error) {
			reapCalls++
			return daemonctl.CodexMonitorReapResult{}, nil
		},
		resolveCodex: func() (string, error) {
			resolveCodexCalls++
			return "/opt/codex", nil
		},
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string) (codexMonitorProcess, error) {
			switch kind {
			case codexMonitorAppServer:
				appStarts++
			case codexMonitorTUI:
				tuiStarts++
			}
			return nil, errors.New("explicit resume must not start a monitor process")
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) {
			t.Fatal("explicit leading resume connected to App Server")
			return nil, nil
		},
		runNormal: func(string, []string) (int, error) {
			normalCalls++
			return 0, nil
		},
		stderr: io.Discard,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction:   "monitor",
		codexMonitorExplicit: true,
		codexMonitorRole:     "executor",
		codexMonitorTaskID:   "920",
		codexArgs:            []string{"resume", "thread-existing", "--last"},
	}, t.TempDir(), deps)
	if code != 1 {
		t.Fatalf("exit code = %d, want explicit failure code 1", code)
	}
	if err == nil || !strings.Contains(err.Error(), "leading resume") {
		t.Fatalf("explicit leading resume error = %v, want leading resume contract error", err)
	}
	if projectPathCalls != 0 || scopeCalls != 0 || reapCalls != 0 || resolveCodexCalls != 0 || appStarts != 0 || tuiStarts != 0 || normalCalls != 0 {
		t.Fatalf("explicit leading resume setup calls = project=%d scope=%d reap=%d resolve=%d app=%d tui=%d normal=%d, want all zero", projectPathCalls, scopeCalls, reapCalls, resolveCodexCalls, appStarts, tuiStarts, normalCalls)
	}
}

func TestCodexMonitorDoesNotAdoptThreadStartedBroadcast(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(0)
	tuiStarted := make(chan struct{})
	notificationDelivered := make(chan struct{})
	resumedThread := make(chan string, 1)

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
	app.onResume = func(threadID string) { resumedThread <- threadID }

	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string) (codexMonitorProcess, error) {
			switch kind {
			case codexMonitorAppServer:
				return app, nil
			case codexMonitorTUI:
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
		runWatch: func(ctx context.Context, _ *codexMonitorBridge) error {
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
	select {
	case got := <-resumedThread:
		t.Fatalf("thread/started broadcast resumed %q", got)
	default:
	}
}

func TestCodexMonitorThreadStartFailureFallsBackBeforeTUI(t *testing.T) {
	monitorDir := t.TempDir()
	app := newFakeCodexMonitorApp()
	app.startThread = func(context.Context, string) (codexThread, error) {
		return codexThread{}, errors.New("thread/start failed")
	}
	var tuiStarts, normalCalls, watchStarts int
	var gotArgs []string

	deps := codexMonitorDeps{
		resolveCodex: func() (string, error) { return "/opt/codex", nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return app, nil
			}
			tuiStarts++
			return nil, errors.New("TUI must not start after thread/start failure")
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		reap:             func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register:         func(string, daemonctl.CodexMonitorRecord) (func(), error) { return func() {}, nil },
		runWatch: func(ctx context.Context, _ *codexMonitorBridge) error {
			watchStarts++
			<-ctx.Done()
			return nil
		},
		runNormal: func(_ string, args []string) (int, error) {
			normalCalls++
			gotArgs = append([]string(nil), args...)
			return 17, nil
		},
		stderr: io.Discard,
	}

	code, runErr := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction: "monitor",
		codexArgs:          []string{"--model", "gpt-5"},
	}, monitorDir, deps)
	if runErr != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", runErr)
	}
	if tuiStarts != 0 {
		t.Fatalf("TUI starts = %d, want 0", tuiStarts)
	}
	if watchStarts != 0 {
		t.Fatalf("watcher starts = %d, want 0", watchStarts)
	}
	if normalCalls != 1 {
		t.Fatalf("normal fallback calls = %d, want 1", normalCalls)
	}
	if !slices.Equal(gotArgs, []string{"--model", "gpt-5"}) {
		t.Fatalf("fallback args = %#v, want original args", gotArgs)
	}
	if code != 17 {
		t.Fatalf("exit code = %d, want normal fallback exit code 17", code)
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
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return app, nil
			}
			return tui, nil
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		runWatch: func(ctx context.Context, _ *codexMonitorBridge) error {
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
	startThread           func(context.Context, string) (codexThread, error)
	notificationErr       error
	notifications         chan codexAppServerNotification
	notificationDelivered chan struct{}
	onDiscover            func()
	onDiscoverBefore      func(map[string]struct{})
	onResume              func(string)
	listThreadsPage       func(context.Context, string, *string) (codexThreadListPage, error)
}

func newFakeCodexMonitorApp() *fakeCodexMonitorApp {
	return &fakeCodexMonitorApp{process: newFakeCodexMonitorProcess(0), thread: codexThread{ID: "thread-new", CWD: "/project"}}
}

func (a *fakeCodexMonitorApp) Initialize(context.Context) error { return nil }

func (a *fakeCodexMonitorApp) StartThread(ctx context.Context, cwd string) (codexThread, error) {
	if a.startThread != nil {
		return a.startThread(ctx, cwd)
	}
	return a.thread, nil
}

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

func (a *fakeCodexMonitorApp) StartTurn(context.Context, string, string) (codexTurn, error) {
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
