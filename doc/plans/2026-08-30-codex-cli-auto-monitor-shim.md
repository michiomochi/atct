# Transparent Codex CLI Monitor Shim Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make an explicitly installed `codex` PATH shim automatically launch the existing ATCT monitor only for a zero-argument invocation in a registered project, while preserving every argument-bearing Codex invocation unchanged.

**Architecture:** Add internal `atct codex shim install` and `atct codex shim run -- <args>` actions. The installer writes a marked POSIX launcher; the runner finds a non-shim Codex binary, passes every nonempty argument vector straight through, and only then checks local registration through `Store.ResolveProject` for a bare invocation.

**Tech Stack:** Go standard library, existing SQLite store, generated POSIX shell, existing Codex App Server supervisor.

## Global Constraints

- CLI-only: no Codex App support.
- Do not add Herdr, pane, or multiplexer dependencies to production code or the generated shim.
- Do not edit `.codex/hooks.json`, Codex configuration, or ATCT global settings.
- Only the explicit installer may edit a shell profile. Tests use temporary homes/profiles, never the human's profile.
- Automatic scope is commander/project only. Subcommander/executor remain explicit `atct codex monitor --role ... --goal/--task ...` calls.
- Preserve raw arguments and exit status. Unregistered/missing/broken local store and monitor setup failure must leave normal Codex usable.
- Automatic interception is exactly `len(args) == 0`; do not implement a Codex option, prompt, or subcommand parser.
- Reuse Goal 206 lifecycle. Explicit role resolution remains fail-closed.

---

## File structure

| File | Responsibility |
| --- | --- |
| `cmd/atct/main.go` | Parse and route shim actions without changing current monitor grammar. |
| `cmd/atct/codex_shim.go` | Template, installer, resolver, argument-count dispatcher, local registration check, automatic dispatch. |
| `cmd/atct/codex_shim_test.go` | Installer, resolver, argument-boundary, local-store, and fallback tests. |
| `cmd/atct/codex_monitor_supervisor.go` | Consume verified automatic commander scope while retaining explicit scope resolution. |
| `cmd/atct/codex_monitor_lifecycle_test.go` | Automatic lifecycle/fallback and explicit-scope regression tests. |
| `cmd/atct/main_test.go` | CLI grammar and opaque argument tests. |

## Task 1: CLI grammar and installer

**Files:**
- Modify: `cmd/atct/main.go:31-126, 230-317, 455-545`
- Modify: `cmd/atct/main_test.go:57-220`
- Create: `cmd/atct/codex_shim.go`
- Create: `cmd/atct/codex_shim_test.go`

**Interfaces:**
- Produces `cliConfig.codexShimAction string` (`install`/`run`), `cliConfig.codexShimProfile string`, and opaque `cliConfig.codexArgs []string`.
- Produces `runCodexShimInstall(config cliConfig, exePath string) (int, error)`.
- Produces `writeCodexShim(home, profile, atctExecutable string) error`.

- [ ] **Step 1: Write failing parser tests**

Add tests for exactly these cases:

```go
cfg, err := parseArgs([]string{"codex", "shim", "run", "--", "resume", "abc", "--last"})
if err != nil { t.Fatal(err) }
if cfg.codexShimAction != "run" || !slices.Equal(cfg.codexArgs, []string{"resume", "abc", "--last"}) {
    t.Fatalf("shim run config = %#v", cfg)
}
```

Also assert `codex shim install --profile /tmp/profile` records the profile. Assert missing run delimiter, unknown shim action, trailing install arguments, and role selectors on shim run return `errInvalidArgs`.

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./cmd/atct -run TestParseArgsCodexShim -count=1`

Expected: FAIL because `shim` is rejected.

- [ ] **Step 3: Implement parser and route**

Keep `codex monitor` unchanged. Accept only:

```text
atct codex shim install [--profile <path>]
atct codex shim run -- <opaque Codex arguments>
```

Use a `flag.NewFlagSet` only for install's `--profile`; reject every run token before literal `--`. Route install to `runCodexShimInstall` and run to `runCodexShim`; add usage lines.

- [ ] **Step 4: Write failing installer tests**

Use `t.TempDir()` as home/profile. Assert the created `<home>/.atct/bin/codex` contains `codexShimMarker` and `exec '/opt/atct' codex shim run -- "$@"`; mode is executable; double install writes one marked PATH block; an unmarked pre-existing shim is unchanged/error; default absent profile prints the PATH line and does not create a profile. No test reads process home.

- [ ] **Step 5: Implement installer**

Define:

```go
const codexShimMarker = "# atct-codex-shim-v1"
func writeCodexShim(home, profile, atctExecutable string) error
func defaultCodexShimProfile(shell, home string) string
func appendCodexShimProfileBlock(profile, shimDir string) error
```

Reject destination unless absent or marker-owned. Use same-directory temporary file, mode `0700`, close, then rename. Use distinct begin/end profile markers; add only one PATH prepend. Obtain home/shell from `os.UserHomeDir`/`os.Getenv("SHELL")`; do not invoke a shell or modify parent environment.

- [ ] **Step 6: Verify and commit**

```bash
go test ./cmd/atct -run 'TestParseArgsCodexShim|TestWriteCodexShim' -count=1
gofmt -w cmd/atct/main.go cmd/atct/main_test.go cmd/atct/codex_shim.go cmd/atct/codex_shim_test.go
git diff --check
git add cmd/atct/main.go cmd/atct/main_test.go cmd/atct/codex_shim.go cmd/atct/codex_shim_test.go
git commit -m "feat: add Codex shim installer"
```

Expected: passing focused tests; only listed files committed.

## Task 2: Resolver, local registration, and automatic scope

**Files:**
- Modify: `cmd/atct/codex_shim.go`
- Modify: `cmd/atct/codex_shim_test.go`
- Modify: `cmd/atct/codex_monitor_supervisor.go:37-115, 343-470`
- Modify: `cmd/atct/codex_monitor_lifecycle_test.go`

**Interfaces:**
- Produces `resolveRealCodex(pathEnv string) (string, error)`, ignoring executable candidates whose contents have `codexShimMarker`.
- Extends the generated shim to embed the installer-resolved absolute real-Codex fallback and execute it with unchanged arguments when the embedded ATCT launcher is unavailable.
- Produces `codexShimPassesThrough(args []string) bool`, which returns true
  exactly when `len(args) > 0` and does not inspect their contents.
- Produces `runCodexShimWithDeps(config cliConfig, dir string, deps codexShimDeps) (int, error)`.
- Adds `cliConfig.codexMonitorAutomatic bool`, `cliConfig.codexMonitorProjectID string`.

- [ ] **Step 1: Write failing resolver/argument-boundary tests**

Put marked `bin/codex` before real `real/codex` in a temporary PATH:

```go
got, err := resolveRealCodex(pathEnv)
if err != nil || got != realCodex { t.Fatalf("resolve = %q, %v", got, err) }
for _, args := range [][]string{
    {"exec", "--help"}, {"resume", "abc"}, {"--version"},
    {"--config", `model="gpt-5"`, "exec", "--help"},
    {"--profile", "work", "review", "--help"},
    {"--image", "a.png", "b.png", "exec", "--help"},
} {
    if !codexShimPassesThrough(args) { t.Fatalf("%q must pass through", args) }
}
if codexShimPassesThrough(nil) { t.Fatal("bare invocation must be automatic") }
```

Cover a marked-only PATH and a non-executable marked file.

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./cmd/atct -run 'TestResolveRealCodex|TestCodexShimPassesThrough' -count=1`

Expected: FAIL because these helpers do not exist.

- [ ] **Step 3: Implement resolution/argument boundary and launcher fallback**

Split PATH with `filepath.SplitList`, treating an empty entry as `.`. For each executable candidate named `codex`, inspect its prefix for `codexShimMarker`; return the first unmarked executable. Make existing `resolveCodexExecutable` delegate to it so direct `atct codex monitor` cannot recurse either.

During installation, resolve that unmarked executable before writing the shim and pass its absolute path into the script template. The script must test whether its embedded ATCT launcher remains executable: if yes, `exec` it with `codex shim run -- "$@"`; otherwise `exec` the embedded real Codex with the original `"$@"`. If real Codex cannot be resolved at installation, still write the shim only when its fallback branch prints the normal command-not-found diagnostic and exits `127`; do not write a launcher-only shim that can make `codex` unusable after ATCT is removed.

Do not define a Codex option table or command classifier. Implement
`codexShimPassesThrough(args)` as the argument-count boundary: `len(args) > 0`.
Do not strip, reorder, parse, classify, or otherwise mutate the original slice.

- [ ] **Step 4: Write failing local dispatch tests**

Create a temporary `atct.db`, register a root with `store.CreateProject`, and inject a nested cwd. With counters, assert:
- registered bare `codex` calls monitor with empty args, automatic true, commander role, and decimal local project ID;
- registered `resume`, `exec --help`, `--config model=\"gpt-5\" exec --help`,
  `--profile work review --help`, and `--image a.png b.png exec --help` call
  normal Codex with identical args and zero store/monitor calls;
- unregistered cwd and absent database call normal Codex;
- store/cwd error calls normal Codex and emits a diagnostic.

- [ ] **Step 5: Implement local dispatch**

Define:

```go
type codexShimDeps struct {
    cwd func() (string, error)
    openStore func(string) (*store.Store, error)
    resolveCodex func() (string, error)
    runNormal func(string, []string) (int, error)
    runMonitor func(cliConfig, string) (int, error)
    stderr io.Writer
}
```

`runCodexShimWithDeps` resolves real Codex and, for every nonempty argument vector, calls normal Codex before cwd or store lookup. For a bare invocation it opens `<dir>/atct.db`, calls `ResolveProject(context.Background(), cwd)`, and passes through on absent DB, `store.ErrProjectNotFound`, or lookup error. On a registered bare launch it calls monitor with action `monitor`, automatic true, commander role, local decimal project ID, and empty args. It never starts a daemon, writes store data, or infers goal/task.

- [ ] **Step 6: Consume automatic scope in supervisor**

Before existing explicit selection, validate automatic mode has exactly commander role, non-empty project ID, and no goal/task IDs. Use:

```go
scope = watchScope{Role: "commander", ProjectID: config.codexMonitorProjectID}
```

Keep explicit `deps.resolveScope` unchanged. Ensure default watcher receives selected `scope`. Automatic setup failures use normal-Codex fallback; explicit role setup failures remain fail-closed.

- [ ] **Step 7: Verify and commit**

```bash
go test ./cmd/atct -run 'TestResolveRealCodex|TestCodexShimPassesThrough|TestCodexShim.*Dispatch|TestCodexMonitor.*Automatic|TestCodexMonitorExplicit' -count=1
gofmt -w cmd/atct/codex_shim.go cmd/atct/codex_shim_test.go cmd/atct/codex_monitor_supervisor.go cmd/atct/codex_monitor_lifecycle_test.go
git diff --check
git add cmd/atct/codex_shim.go cmd/atct/codex_shim_test.go cmd/atct/codex_monitor_supervisor.go cmd/atct/codex_monitor_lifecycle_test.go
git commit -m "feat: route registered Codex sessions through monitor"
```

Expected: focused tests pass, automatic setup falls back, explicit worker tests stay green.

## Task 3: Public shim boundary and regression verification

**Files:**
- Modify: `cmd/atct/codex_shim_test.go`
- Modify: `cmd/atct/codex_monitor_lifecycle_test.go`
- Modify: `doc/specs/2026-08-30-codex-cli-auto-monitor-shim.md` only if a material implementation mismatch is proven.

**Interfaces:** Consumes Tasks 1–2; adds no production interface.

- [ ] **Step 1: Write failing boundary tests**

Execute generated shim against temporary fake absolute `atct` and real `codex` executables. Assert a bare invocation in an unregistered cwd or with a missing local database invokes real Codex with identical (empty) arguments and no shim diagnostic; unexpected bare-store/cwd errors still emit one diagnostic before pass-through. Assert an argument-bearing call never performs local lookup and calls exactly:

```text
<absolute-atct> codex shim run -- resume test-thread --last
```

and returns the fake launcher's exit status. Remove or make the fake ATCT launcher non-executable and assert the generated shim invokes the embedded real Codex with exactly the original arguments and its exit status. Add lifecycle tests where automatic reap/App Server startup fails: exactly one normal fallback, unchanged args, no second TUI. Add a direct monitor test with `PATH=<shim-dir>:<real-dir>` that observes the real binary for App Server and TUI.

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./cmd/atct -run 'TestGeneratedCodexShim|TestCodexMonitorAutomatic.*Fallback|TestCodexMonitor.*SkipsShim' -count=1`

Expected: FAIL until public boundary/recursion coverage is complete.

- [ ] **Step 3: Implement only test-proven corrections**

Keep generated script POSIX: `#!/bin/sh`, quoted absolute launcher, and `exec`. Correct only demonstrated forwarding, exit-status, resolver, or fallback defects. Do not add hooks, config files, shell functions, daemon startup, settings writes, or Herdr integration.

- [ ] **Step 4: Run full verification**

```bash
go test ./cmd/atct -count=1
go test ./... -count=1 -timeout 600s
go build ./...
git diff --check
```

Expected: all commands exit 0. For an unexpected failure, stop and use `superpowers:systematic-debugging` before changing code.

- [ ] **Step 5: Commit regression coverage**

```bash
git add cmd/atct/codex_shim_test.go cmd/atct/codex_monitor_lifecycle_test.go
git commit -m "test: cover transparent Codex monitor shim"
```

Expected: intended test files only, after passing full verification.

## Self-review

- Spec coverage: Tasks cover explicit installation, profile safety, marked resolver, local worktree-aware registration, conservative passthrough, commander-only automatic scope, Goal 206 lifecycle reuse, fallback, and no-hooks/no-config/no-App/no-Herdr boundaries.
- Placeholder scan: each task gives files, interfaces, test inputs, commands, and expected outputs; no unspecified work item remains.
- Type consistency: shim config feeds `runCodexShimWithDeps`, which feeds existing `runCodexMonitorWithDeps`; automatic fields become `watchScope` only inside the supervisor.
