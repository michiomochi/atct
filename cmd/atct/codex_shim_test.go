package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/store"
)

func TestWriteCodexShimInstallsExecutableAndProfileBlock(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(profile, []byte("# existing profile\n"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	if err := writeCodexShim(home, profile, "/opt/atct"); err != nil {
		t.Fatalf("writeCodexShim: %v", err)
	}

	shimPath := filepath.Join(home, ".atct", "bin", "codex")
	shim, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatalf("read installed shim: %v", err)
	}
	shimText := string(shim)
	if !strings.Contains(shimText, codexShimMarker) {
		t.Fatalf("shim = %q, want marker %q", shimText, codexShimMarker)
	}
	if !strings.Contains(shimText, "exec '/opt/atct' codex shim run -- \"$@\"") {
		t.Fatalf("shim = %q, want opaque launcher invocation", shimText)
	}
	info, err := os.Stat(shimPath)
	if err != nil {
		t.Fatalf("stat installed shim: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("shim mode = %o, want executable", info.Mode().Perm())
	}

	profileText, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if !strings.Contains(string(profileText), codexShimProfileBeginMarker) || !strings.Contains(string(profileText), codexShimProfileEndMarker) {
		t.Fatalf("profile = %q, want marked PATH block", profileText)
	}
}

func TestWriteCodexShimIsIdempotent(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(profile, []byte("# existing profile\n"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := writeCodexShim(home, profile, "/opt/atct"); err != nil {
			t.Fatalf("writeCodexShim iteration %d: %v", i, err)
		}
	}

	profileText, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if got := strings.Count(string(profileText), codexShimProfileBeginMarker); got != 1 {
		t.Fatalf("profile begin marker count = %d, want 1; profile = %q", got, profileText)
	}
	if got := strings.Count(string(profileText), codexShimProfileEndMarker); got != 1 {
		t.Fatalf("profile end marker count = %d, want 1; profile = %q", got, profileText)
	}
}

func TestWriteCodexShimPreservesUnmarkedCollision(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".zshrc")
	profileOriginal := []byte("# keep this profile\n")
	if err := os.WriteFile(profile, profileOriginal, 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	shimPath := filepath.Join(home, ".atct", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(shimPath), 0o700); err != nil {
		t.Fatalf("create shim directory: %v", err)
	}
	original := []byte("#!/bin/sh\necho user codex\n")
	if err := os.WriteFile(shimPath, original, 0o700); err != nil {
		t.Fatalf("write existing codex: %v", err)
	}

	err := writeCodexShim(home, profile, "/opt/atct")
	if err == nil {
		t.Fatal("writeCodexShim returned nil for unmarked existing codex")
	}
	got, readErr := os.ReadFile(shimPath)
	if readErr != nil {
		t.Fatalf("read existing codex: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("existing codex = %q, want unchanged %q", got, original)
	}
	profileGot, readErr := os.ReadFile(profile)
	if readErr != nil {
		t.Fatalf("read existing profile: %v", readErr)
	}
	if string(profileGot) != string(profileOriginal) {
		t.Fatalf("existing profile = %q, want unchanged %q", profileGot, profileOriginal)
	}
}

func TestRunCodexShimInstallWithoutSupportedProfilePrintsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")

	var (
		code int
		err  error
	)
	output := captureStderr(t, func() {
		code, err = runCodexShimInstall(cliConfig{codexShimAction: "install"}, "/opt/atct")
	})
	if err != nil {
		t.Fatalf("runCodexShimInstall: %v", err)
	}
	if code != 0 {
		t.Fatalf("runCodexShimInstall code = %d, want 0", code)
	}
	if !strings.Contains(output, codexShimPathLine(filepath.Join(home, ".atct", "bin"))) {
		t.Fatalf("output = %q, want PATH line", output)
	}
	for _, profile := range []string{filepath.Join(home, ".zshrc"), filepath.Join(home, ".bashrc")} {
		if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("profile %s stat error = %v, want absent", profile, err)
		}
	}
}

func TestDefaultCodexShimProfileSelectsSupportedShells(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		shell string
		want  string
	}{
		{shell: "/bin/zsh", want: filepath.Join(home, ".zshrc")},
		{shell: "/usr/bin/bash", want: filepath.Join(home, ".bashrc")},
		{shell: "/bin/fish", want: ""},
	}
	for _, tt := range tests {
		t.Run(filepath.Base(tt.shell), func(t *testing.T) {
			if got := defaultCodexShimProfile(tt.shell, home); got != tt.want {
				t.Fatalf("defaultCodexShimProfile(%q, %q) = %q, want %q", tt.shell, home, got, tt.want)
			}
		})
	}
}

func TestResolveRealCodexSkipsMarkedShimCandidates(t *testing.T) {
	binDir := t.TempDir()
	markedPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(markedPath, []byte("#!/bin/sh\n"+codexShimMarker+"\n"), 0o700); err != nil {
		t.Fatalf("write marked Codex shim: %v", err)
	}

	realDir := t.TempDir()
	realPath := filepath.Join(realDir, "codex")
	if err := os.WriteFile(realPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write real Codex executable: %v", err)
	}

	got, err := resolveRealCodex(binDir + string(os.PathListSeparator) + realDir)
	if err != nil {
		t.Fatalf("resolveRealCodex: %v", err)
	}
	if got != realPath {
		t.Fatalf("resolveRealCodex = %q, want %q", got, realPath)
	}
}

func TestResolveRealCodexReturnsErrorWhenOnlyMarkedShimsExist(t *testing.T) {
	binDir := t.TempDir()
	markedPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(markedPath, []byte(codexShimMarker+"\n"), 0o700); err != nil {
		t.Fatalf("write marked Codex shim: %v", err)
	}

	if _, err := resolveRealCodex(binDir); err == nil {
		t.Fatal("resolveRealCodex returned nil error for marked-only PATH")
	}
}

func TestCodexShimPassesThroughNonInteractiveCommands(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "empty", args: nil, want: true},
		{name: "help flag", args: []string{"--help"}, want: true},
		{name: "version flag", args: []string{"--version"}, want: true},
		{name: "exec", args: []string{"exec", "--help"}, want: true},
		{name: "short exec", args: []string{"e", "pwd"}, want: true},
		{name: "login", args: []string{"login"}, want: true},
		{name: "interactive resume", args: []string{"resume", "thread-1"}, want: false},
		{name: "interactive model option", args: []string{"--model", "gpt-5"}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexShimPassesThrough(tt.args); got != tt.want {
				t.Fatalf("codexShimPassesThrough(%#v) = %t, want %t", tt.args, got, tt.want)
			}
		})
	}
}

func TestGeneratedCodexShimUsesATCTLauncherWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	atctPath := filepath.Join(dir, "atct")
	realCodexPath := filepath.Join(dir, "real-codex")
	atctRecord := filepath.Join(dir, "atct-args")
	realRecord := filepath.Join(dir, "real-args")
	writeCodexShimTestExecutable(t, atctPath, atctRecord, 23)
	writeCodexShimTestExecutable(t, realCodexPath, realRecord, 37)
	shimPath := filepath.Join(dir, "shim")
	if err := os.WriteFile(shimPath, []byte(codexShimScript(atctPath, realCodexPath)), 0o700); err != nil {
		t.Fatalf("write generated shim: %v", err)
	}

	output, err := exec.Command(shimPath, "resume", "thread-1", "--last").CombinedOutput()
	if got := commandExitCode(err); got != 23 {
		t.Fatalf("generated shim exit code = %d, want 23 (output %q, err %v)", got, output, err)
	}
	if _, err := os.Stat(realRecord); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real Codex record stat error = %v, want absent", err)
	}
	if got := readCodexShimTestArgs(t, atctRecord); strings.Join(got, "\x00") != strings.Join([]string{"codex", "shim", "run", "--", "resume", "thread-1", "--last"}, "\x00") {
		t.Fatalf("ATCT launcher args = %#v, want shim dispatch args", got)
	}
}

func TestGeneratedCodexShimFallsBackToRealCodexWithOriginalArguments(t *testing.T) {
	dir := t.TempDir()
	atctPath := filepath.Join(dir, "atct")
	realCodexPath := filepath.Join(dir, "real-codex")
	realRecord := filepath.Join(dir, "real-args")
	writeCodexShimTestExecutable(t, atctPath, filepath.Join(dir, "atct-args"), 23)
	if err := os.Chmod(atctPath, 0o600); err != nil {
		t.Fatalf("disable embedded ATCT launcher: %v", err)
	}
	writeCodexShimTestExecutable(t, realCodexPath, realRecord, 37)
	shimPath := filepath.Join(dir, "shim")
	if err := os.WriteFile(shimPath, []byte(codexShimScript(atctPath, realCodexPath)), 0o700); err != nil {
		t.Fatalf("write generated shim: %v", err)
	}

	output, err := exec.Command(shimPath, "resume", "thread-1", "--last").CombinedOutput()
	if got := commandExitCode(err); got != 37 {
		t.Fatalf("generated shim fallback exit code = %d, want 37 (output %q, err %v)", got, output, err)
	}
	if got := readCodexShimTestArgs(t, realRecord); strings.Join(got, "\x00") != strings.Join([]string{"resume", "thread-1", "--last"}, "\x00") {
		t.Fatalf("real Codex args = %#v, want original args", got)
	}
}

func TestGeneratedCodexShimWithoutFallbackReportsCommandNotFound(t *testing.T) {
	dir := t.TempDir()
	shimPath := filepath.Join(dir, "shim")
	if err := os.WriteFile(shimPath, []byte(codexShimScript(filepath.Join(dir, "missing-atct"), "")), 0o700); err != nil {
		t.Fatalf("write generated shim: %v", err)
	}

	output, err := exec.Command(shimPath, "resume").CombinedOutput()
	if got := commandExitCode(err); got != 127 {
		t.Fatalf("generated shim without fallback exit code = %d, want 127 (output %q, err %v)", got, output, err)
	}
	if !strings.Contains(string(output), "codex: command not found") {
		t.Fatalf("generated shim output = %q, want command-not-found diagnostic", output)
	}
}

func writeCodexShimTestExecutable(t *testing.T, path, record string, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(record) + "\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write test executable %s: %v", path, err)
	}
}

func readCodexShimTestArgs(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recorded arguments %s: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func TestRunCodexShimDispatchesRegisteredInteractiveProject(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	cwd := filepath.Join(root, "nested")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatalf("create nested project directory: %v", err)
	}
	db, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	project, err := db.CreateProject(context.Background(), "registered", root)
	if closeErr := db.Close(); err != nil {
		t.Fatalf("register test project: %v", err)
	} else if closeErr != nil {
		t.Fatalf("close test store: %v", closeErr)
	}

	var (
		gotMonitorConfig cliConfig
		normalCalls      int
	)
	deps := codexShimDeps{
		cwd:       func() (string, error) { return cwd, nil },
		openStore: store.Open,
		resolveCodex: func() (string, error) {
			return "/opt/real-codex", nil
		},
		runNormal: func(string, []string) (int, error) {
			normalCalls++
			return 19, nil
		},
		runMonitor: func(config cliConfig, _ string) (int, error) {
			gotMonitorConfig = config
			return 29, nil
		},
		stderr: &bytes.Buffer{},
	}

	code, err := runCodexShimWithDeps(cliConfig{codexArgs: []string{"resume", "thread-1"}}, dir, deps)
	if err != nil {
		t.Fatalf("runCodexShimWithDeps: %v", err)
	}
	if code != 29 {
		t.Fatalf("exit code = %d, want 29", code)
	}
	if normalCalls != 0 {
		t.Fatalf("normal Codex calls = %d, want 0", normalCalls)
	}
	if gotMonitorConfig.codexMonitorAction != "monitor" || !gotMonitorConfig.codexMonitorAutomatic {
		t.Fatalf("monitor config = %#v, want automatic monitor action", gotMonitorConfig)
	}
	if gotMonitorConfig.codexMonitorExplicit || gotMonitorConfig.codexMonitorRole != "commander" {
		t.Fatalf("monitor config = %#v, want non-explicit commander scope", gotMonitorConfig)
	}
	if gotMonitorConfig.codexMonitorProjectID != strconv.FormatInt(project.ID, 10) {
		t.Fatalf("monitor project ID = %q, want %q", gotMonitorConfig.codexMonitorProjectID, strconv.FormatInt(project.ID, 10))
	}
	if got := strings.Join(gotMonitorConfig.codexArgs, "\x00"); got != "resume\x00thread-1" {
		t.Fatalf("monitor args = %#v, want original args", gotMonitorConfig.codexArgs)
	}
}

func TestRunCodexShimPassesThroughNonInteractiveBeforeStoreLookup(t *testing.T) {
	var (
		storeCalls    int
		gotExecutable string
		gotArgs       []string
	)
	deps := codexShimDeps{
		cwd: func() (string, error) { return "/project", nil },
		openStore: func(string) (*store.Store, error) {
			storeCalls++
			return nil, errors.New("store must not be opened")
		},
		resolveCodex: func() (string, error) { return "/opt/real-codex", nil },
		runNormal: func(executable string, args []string) (int, error) {
			gotExecutable = executable
			gotArgs = append([]string(nil), args...)
			return 31, nil
		},
		runMonitor: func(cliConfig, string) (int, error) {
			return 1, errors.New("monitor must not start")
		},
		stderr: &bytes.Buffer{},
	}

	code, err := runCodexShimWithDeps(cliConfig{codexArgs: []string{"exec", "--help"}}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexShimWithDeps: %v", err)
	}
	if code != 31 || gotExecutable != "/opt/real-codex" {
		t.Fatalf("pass-through result = (%d, %q), want (31, /opt/real-codex)", code, gotExecutable)
	}
	if got := strings.Join(gotArgs, "\x00"); got != "exec\x00--help" {
		t.Fatalf("pass-through args = %#v, want original args", gotArgs)
	}
	if storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0 for noninteractive command", storeCalls)
	}
}

func TestRunCodexShimFallsBackForUnregisteredOrUnavailableProjects(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string) func(string) (*store.Store, error)
		cwd        func() (string, error)
		wantReason string
	}{
		{
			name: "unregistered",
			setup: func(t *testing.T, dir string) func(string) (*store.Store, error) {
				db, err := store.Open(filepath.Join(dir, "atct.db"))
				if err != nil {
					t.Fatalf("open test store: %v", err)
				}
				if err := db.Close(); err != nil {
					t.Fatalf("close test store: %v", err)
				}
				return store.Open
			},
			cwd:        func() (string, error) { return t.TempDir(), nil },
			wantReason: "project lookup",
		},
		{
			name: "missing database",
			setup: func(t *testing.T, _ string) func(string) (*store.Store, error) {
				return store.Open
			},
			cwd:        func() (string, error) { return t.TempDir(), nil },
			wantReason: "database",
		},
		{
			name: "store error",
			setup: func(t *testing.T, dir string) func(string) (*store.Store, error) {
				if err := os.WriteFile(filepath.Join(dir, "atct.db"), nil, 0o600); err != nil {
					t.Fatalf("create store error fixture: %v", err)
				}
				return func(string) (*store.Store, error) { return nil, errors.New("database unavailable") }
			},
			cwd:        func() (string, error) { return t.TempDir(), nil },
			wantReason: "open local store",
		},
		{
			name: "cwd error",
			setup: func(_ *testing.T, _ string) func(string) (*store.Store, error) {
				return store.Open
			},
			cwd:        func() (string, error) { return "", errors.New("cwd unavailable") },
			wantReason: "resolve current directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var (
				gotExecutable string
				gotArgs       []string
				monitorCalls  int
				stderr        bytes.Buffer
			)
			deps := codexShimDeps{
				cwd:          tt.cwd,
				openStore:    tt.setup(t, dir),
				resolveCodex: func() (string, error) { return "/opt/real-codex", nil },
				runNormal: func(executable string, args []string) (int, error) {
					gotExecutable = executable
					gotArgs = append([]string(nil), args...)
					return 37, nil
				},
				runMonitor: func(cliConfig, string) (int, error) {
					monitorCalls++
					return 1, errors.New("monitor must not start")
				},
				stderr: &stderr,
			}

			code, err := runCodexShimWithDeps(cliConfig{codexArgs: []string{"resume", "thread-2"}}, dir, deps)
			if err != nil {
				t.Fatalf("runCodexShimWithDeps: %v", err)
			}
			if code != 37 || gotExecutable != "/opt/real-codex" {
				t.Fatalf("fallback result = (%d, %q), want (37, /opt/real-codex)", code, gotExecutable)
			}
			if got := strings.Join(gotArgs, "\x00"); got != "resume\x00thread-2" {
				t.Fatalf("fallback args = %#v, want original args", gotArgs)
			}
			if monitorCalls != 0 {
				t.Fatalf("monitor calls = %d, want 0", monitorCalls)
			}
			if !strings.Contains(stderr.String(), tt.wantReason) {
				t.Fatalf("fallback diagnostic = %q, want %q", stderr.String(), tt.wantReason)
			}
			if tt.name == "missing database" {
				if _, statErr := os.Stat(filepath.Join(dir, "atct.db")); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("missing database stat error = %v, want database left absent", statErr)
				}
			}
		})
	}
}

func TestResolveCodexExecutableSkipsMarkedShim(t *testing.T) {
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "codex"), []byte("#!/bin/sh\n"+codexShimMarker+"\n"), 0o700); err != nil {
		t.Fatalf("write marked Codex shim: %v", err)
	}
	realDir := t.TempDir()
	realPath := filepath.Join(realDir, "codex")
	if err := os.WriteFile(realPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write real Codex executable: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)

	got, err := resolveCodexExecutable()
	if err != nil {
		t.Fatalf("resolveCodexExecutable: %v", err)
	}
	if got != realPath {
		t.Fatalf("resolveCodexExecutable = %q, want %q", got, realPath)
	}
}

func TestRunCodexProcessDoesNotLaunchMarkedShimWhenCodexIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "marked-shim-ran")
	shimPath := filepath.Join(dir, "codex")
	script := "#!/bin/sh\n" + codexShimMarker + "\ntouch " + shellQuote(record) + "\nexit 23\n"
	if err := os.WriteFile(shimPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write marked Codex shim: %v", err)
	}
	t.Setenv("PATH", dir)

	code, err := runCodexProcess("codex", []string{"resume"})
	if err != nil {
		t.Fatalf("runCodexProcess: %v", err)
	}
	if code != 127 {
		t.Fatalf("runCodexProcess exit code = %d, want command-not-found 127", code)
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marked shim record stat error = %v, want shim not launched", err)
	}
}

func TestRunCodexShimInstallEmbedsAbsoluteRealCodexFallback(t *testing.T) {
	home := t.TempDir()
	realDir := t.TempDir()
	realPath := filepath.Join(realDir, "codex")
	if err := os.WriteFile(realPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write real Codex executable: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/fish")
	t.Setenv("PATH", realDir)

	code, err := runCodexShimInstall(cliConfig{codexShimAction: "install"}, filepath.Join(t.TempDir(), "atct"))
	if err != nil {
		t.Fatalf("runCodexShimInstall: %v", err)
	}
	if code != 0 {
		t.Fatalf("runCodexShimInstall exit code = %d, want 0", code)
	}
	shim, err := os.ReadFile(filepath.Join(home, ".atct", "bin", "codex"))
	if err != nil {
		t.Fatalf("read installed shim: %v", err)
	}
	wantFallback := "exec " + shellQuote(realPath) + " \"$@\""
	if !strings.Contains(string(shim), wantFallback) {
		t.Fatalf("installed shim = %q, want embedded absolute fallback %q", shim, wantFallback)
	}
}
