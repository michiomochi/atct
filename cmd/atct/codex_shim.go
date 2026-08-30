package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/michiomochi/atct/internal/store"
)

const (
	codexShimMarker             = "# atct-codex-shim-v1"
	codexShimProfileBeginMarker = "# atct-codex-shim-path-begin"
	codexShimProfileEndMarker   = "# atct-codex-shim-path-end"
)

func parseCodexShimArgs(cfg cliConfig, args []string) (cliConfig, error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "codex shim requires an action: install or run")
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	switch args[0] {
	case "install":
		return parseCodexShimInstallArgs(cfg, args[1:])
	case "run":
		return parseCodexShimRunArgs(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown codex shim action %q\n", args[0])
		fmt.Fprintln(os.Stderr, "codex shim requires an action: install or run")
		printUsage()
		return cliConfig{}, errInvalidArgs
	}
}

func parseCodexShimInstallArgs(cfg cliConfig, args []string) (cliConfig, error) {
	profileOption := false
	profileFlagCount := 0
	for _, arg := range args {
		if profileOption {
			profileOption = false
			continue
		}
		if arg == "--" {
			fmt.Fprintln(os.Stderr, "codex shim install does not accept a passthrough delimiter")
			return cliConfig{}, errInvalidArgs
		}
		if arg == "--profile" {
			profileOption = true
			profileFlagCount++
			continue
		}
		if strings.HasPrefix(arg, "--profile=") {
			profileFlagCount++
			if len(arg) == len("--profile=") {
				fmt.Fprintln(os.Stderr, "codex shim install requires a profile path")
				return cliConfig{}, errInvalidArgs
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			fmt.Fprintf(os.Stderr, "unknown codex shim install option %q\n", arg)
			return cliConfig{}, errInvalidArgs
		}
	}
	if profileFlagCount > 1 {
		fmt.Fprintln(os.Stderr, "codex shim install accepts only one --profile option")
		return cliConfig{}, errInvalidArgs
	}

	flags := newFlagSet("codex shim install")
	profile := flags.String("profile", "", "shell profile to update")
	if err := flags.Parse(args); err != nil {
		return cliConfig{}, errInvalidArgs
	}
	if len(flags.Args()) != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flags.Args()[0])
		printUsage()
		return cliConfig{}, errInvalidArgs
	}
	if flags.Lookup("profile") != nil {
		wasSet := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "profile" {
				wasSet = true
			}
		})
		if wasSet && *profile == "" {
			fmt.Fprintln(os.Stderr, "codex shim install requires a profile path")
			return cliConfig{}, errInvalidArgs
		}
	}

	cfg.codexShimAction = "install"
	cfg.codexShimProfile = *profile
	return cfg, nil
}

func parseCodexShimRunArgs(cfg cliConfig, args []string) (cliConfig, error) {
	if len(args) == 0 || args[0] != "--" {
		fmt.Fprintln(os.Stderr, "codex shim run requires a literal -- before Codex arguments")
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	cfg.codexShimAction = "run"
	cfg.codexArgs = append([]string(nil), args[1:]...)
	return cfg, nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	return flags
}

func defaultCodexShimProfile(shell, home string) string {
	switch filepath.Base(shell) {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "bash":
		return filepath.Join(home, ".bashrc")
	default:
		return ""
	}
}

func runCodexShimInstall(config cliConfig, atctExecutable string) (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 1, fmt.Errorf("resolve home: %w", err)
	}

	profile := config.codexShimProfile
	if profile == "" {
		profile = defaultCodexShimProfile(os.Getenv("SHELL"), home)
		if profile != "" {
			if _, err := os.Stat(profile); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					return 1, fmt.Errorf("inspect shell profile: %w", err)
				}
				profile = ""
			}
		}
	}

	realCodex, _ := resolveRealCodex(os.Getenv("PATH"))
	if err := writeCodexShimWithFallback(home, profile, atctExecutable, realCodex); err != nil {
		return 1, err
	}

	shimDir := filepath.Join(home, ".atct", "bin")
	fmt.Fprintf(os.Stderr, "atct codex shim installed at %s\n", filepath.Join(shimDir, "codex"))
	if profile == "" {
		fmt.Fprintf(os.Stderr, "Add this line to your shell profile:\n%s\n", codexShimPathLine(shimDir))
	}
	return 0, nil
}

type codexShimDeps struct {
	cwd          func() (string, error)
	openStore    func(string) (*store.Store, error)
	resolveCodex func() (string, error)
	runNormal    func(string, []string) (int, error)
	runMonitor   func(cliConfig, string) (int, error)
	stderr       io.Writer
}

func runCodexShim(config cliConfig, dir string) (int, error) {
	return runCodexShimWithDeps(config, dir, codexShimDeps{})
}

func runCodexShimWithDeps(config cliConfig, dir string, deps codexShimDeps) (int, error) {
	deps = codexShimDepsWithDefaults(deps)
	args := append([]string(nil), config.codexArgs...)

	executable, err := deps.resolveCodex()
	if err != nil {
		return codexShimFallback(deps, "resolve real Codex: "+err.Error(), "codex", args)
	}
	if codexShimPassesThrough(args) {
		return deps.runNormal(executable, args)
	}

	cwd, err := deps.cwd()
	if err != nil {
		return codexShimFallback(deps, "resolve current directory: "+err.Error(), executable, args)
	}
	dbPath := filepath.Join(dir, "atct.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return codexShimFallback(deps, "local database is missing", executable, args)
		}
		return codexShimFallback(deps, "inspect local database: "+err.Error(), executable, args)
	}
	localStore, err := deps.openStore(dbPath)
	if err != nil {
		return codexShimFallback(deps, "open local store: "+err.Error(), executable, args)
	}
	defer func() { _ = localStore.Close() }()

	project, err := localStore.ResolveProject(context.Background(), cwd)
	if err != nil {
		return codexShimFallback(deps, "project lookup: "+err.Error(), executable, args)
	}

	monitorConfig := config
	monitorConfig.codexMonitorAction = "monitor"
	monitorConfig.codexMonitorPassthrough = false
	monitorConfig.codexMonitorExplicit = false
	monitorConfig.codexMonitorAutomatic = true
	monitorConfig.codexMonitorRole = "commander"
	monitorConfig.codexMonitorProjectID = strconv.FormatInt(project.ID, 10)
	monitorConfig.codexMonitorGoalID = ""
	monitorConfig.codexMonitorTaskID = ""
	return deps.runMonitor(monitorConfig, dir)
}

func codexShimDepsWithDefaults(deps codexShimDeps) codexShimDeps {
	if deps.cwd == nil {
		deps.cwd = os.Getwd
	}
	if deps.openStore == nil {
		deps.openStore = store.Open
	}
	if deps.resolveCodex == nil {
		deps.resolveCodex = resolveCodexExecutable
	}
	if deps.runNormal == nil {
		deps.runNormal = runCodexProcess
	}
	if deps.runMonitor == nil {
		deps.runMonitor = runCodexMonitor
	}
	if deps.stderr == nil {
		deps.stderr = os.Stderr
	}
	return deps
}

func codexShimFallback(deps codexShimDeps, reason, executable string, args []string) (int, error) {
	fmt.Fprintf(deps.stderr, "atct codex shim disabled: %s; running normal codex\n", reason)
	if strings.TrimSpace(executable) == "" {
		executable = "codex"
	}
	return deps.runNormal(executable, args)
}

func writeCodexShim(home, profile, atctExecutable string) error {
	realCodex, _ := resolveRealCodex(os.Getenv("PATH"))
	return writeCodexShimWithFallback(home, profile, atctExecutable, realCodex)
}

func writeCodexShimWithFallback(home, profile, atctExecutable, realCodex string) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("Codex shim home is empty")
	}
	if strings.TrimSpace(atctExecutable) == "" {
		return errors.New("ATCT executable is empty")
	}

	shimDir := filepath.Join(home, ".atct", "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return fmt.Errorf("create Codex shim directory: %w", err)
	}
	if err := os.Chmod(shimDir, 0o700); err != nil {
		return fmt.Errorf("protect Codex shim directory: %w", err)
	}

	shimPath := filepath.Join(shimDir, "codex")
	if err := ensureCodexShimDestination(shimPath); err != nil {
		return err
	}

	shimContent := []byte(codexShimScript(atctExecutable, realCodex))
	shimTemp, err := stageCodexShimFile(shimDir, filepath.Base(shimPath), shimContent, 0o700)
	if err != nil {
		return err
	}
	removeShimTemp := true
	defer func() {
		if removeShimTemp {
			_ = os.Remove(shimTemp)
		}
	}()

	profileTemp := ""
	if profile != "" {
		profileTemp, err = stageCodexShimProfile(profile, shimDir)
		if err != nil {
			return err
		}
		if profileTemp != "" {
			defer func() { _ = os.Remove(profileTemp) }()
		}
	}

	if err := os.Rename(shimTemp, shimPath); err != nil {
		return fmt.Errorf("install Codex shim: %w", err)
	}
	removeShimTemp = false
	if profileTemp != "" {
		if err := os.Rename(profileTemp, profile); err != nil {
			return fmt.Errorf("install Codex shim profile: %w", err)
		}
	}
	return nil
}

func ensureCodexShimDestination(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing Codex executable: %w", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read existing Codex executable: %w", err)
	}
	if !strings.Contains(string(content), codexShimMarker) {
		return fmt.Errorf("refusing to overwrite existing non-ATCT Codex executable %s", path)
	}
	return nil
}

func codexShimScript(atctExecutable string, realCodex ...string) string {
	fallback := ""
	if len(realCodex) > 0 {
		fallback = realCodex[0]
	}

	script := "#!/bin/sh\n" + codexShimMarker + "\n"
	script += "if [ -x " + shellQuote(atctExecutable) + " ]; then\n"
	script += "exec " + shellQuote(atctExecutable) + " codex shim run -- \"$@\"\n"
	script += "fi\n"
	if fallback != "" {
		script += "if [ -x " + shellQuote(fallback) + " ]; then\n"
		script += "exec " + shellQuote(fallback) + " \"$@\"\n"
		script += "fi\n"
	}
	script += "echo 'codex: command not found' >&2\nexit 127\n"
	return script
}

func appendCodexShimProfileBlock(profile, shimDir string) error {
	if strings.TrimSpace(profile) == "" {
		return nil
	}
	temp, err := stageCodexShimProfile(profile, shimDir)
	if err != nil {
		return err
	}
	if temp == "" {
		return nil
	}
	if err := os.Rename(temp, profile); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("install Codex shim profile: %w", err)
	}
	return nil
}

func stageCodexShimProfile(profile, shimDir string) (string, error) {
	content, mode, err := readCodexShimProfile(profile)
	if err != nil {
		return "", err
	}
	hasBegin := strings.Contains(string(content), codexShimProfileBeginMarker)
	hasEnd := strings.Contains(string(content), codexShimProfileEndMarker)
	if hasBegin || hasEnd {
		if hasBegin && hasEnd {
			return "", nil
		}
		return "", fmt.Errorf("shell profile %s contains an incomplete ATCT Codex shim block", profile)
	}

	block := codexShimProfileBlock(shimDir)
	updated := append([]byte(nil), content...)
	if len(updated) > 0 && updated[len(updated)-1] != '\n' {
		updated = append(updated, '\n')
	}
	updated = append(updated, block...)

	return stageCodexShimFile(filepath.Dir(profile), filepath.Base(profile), updated, mode)
}

func readCodexShimProfile(profile string) ([]byte, fs.FileMode, error) {
	content, err := os.ReadFile(profile)
	if err == nil {
		info, statErr := os.Stat(profile)
		if statErr != nil {
			return nil, 0, fmt.Errorf("stat shell profile: %w", statErr)
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o600
		}
		return content, mode, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, 0, fmt.Errorf("read shell profile: %w", err)
	}
	return nil, 0o600, nil
}

func codexShimProfileBlock(shimDir string) []byte {
	return []byte(codexShimProfileBeginMarker + "\n" + codexShimMarker + "\n" + codexShimPathLine(shimDir) + "\n" + codexShimProfileEndMarker + "\n")
}

func codexShimPathLine(shimDir string) string {
	return "export PATH=" + shellQuote(shimDir) + ":\"$PATH\""
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// resolveRealCodex searches PATH for an executable named codex while ignoring
// launchers generated by this package. Returning an absolute path lets the
// installer embed an escape hatch that is independent of the installed PATH.
func resolveRealCodex(pathEnv string) (string, error) {
	entries := filepath.SplitList(pathEnv)
	if len(entries) == 0 {
		entries = []string{""}
	}
	for _, entry := range entries {
		if entry == "" {
			entry = "."
		}
		candidate := filepath.Join(entry, "codex")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}

		file, err := os.Open(candidate)
		if err != nil {
			continue
		}
		prefix := make([]byte, 4096)
		read, readErr := file.Read(prefix)
		closeErr := file.Close()
		if (readErr != nil && !errors.Is(readErr, io.EOF)) || closeErr != nil {
			continue
		}
		if strings.Contains(string(prefix[:read]), codexShimMarker) {
			continue
		}

		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve codex executable: %w", err)
		}
		return absolute, nil
	}
	return "", errors.New("resolve codex executable: command not found")
}

func codexShimPassesThrough(args []string) bool {
	if len(args) == 0 {
		return true
	}
	first := args[0]
	switch first {
	case "-h", "--help", "-V", "--version":
		return true
	}
	if strings.HasPrefix(first, "--help=") || strings.HasPrefix(first, "--version=") {
		return true
	}
	_, ok := codexMonitorPassthroughCommands[first]
	return ok
}

func stageCodexShimFile(dir, name string, content []byte, mode fs.FileMode) (string, error) {
	temp, err := os.CreateTemp(dir, "."+name+".atct-tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temporary Codex shim file: %w", err)
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return "", fmt.Errorf("protect temporary Codex shim file: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		return "", fmt.Errorf("write temporary Codex shim file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return "", fmt.Errorf("sync temporary Codex shim file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("close temporary Codex shim file: %w", err)
	}
	keep = true
	return tempPath, nil
}
