# Homebrew CLI Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Distribute `atct` and `atct-mcp` through the existing Homebrew Cask, while the Claude and Codex plugins retain only MCP, hooks, and skills.

**Architecture:** GoReleaser publishes the release archive and updates `michiomochi/homebrew-tap`'s `atct` Cask. Claude hooks call the `atct` installed on `PATH` and compare its version with their manifest; Codex monitors retain the direct CLI path injected by the command that launched them. No plugin code downloads, caches, extracts, or installs a CLI launcher.

**Tech Stack:** Go, Bash, GoReleaser, Homebrew Cask, existing Bash regression tests.

## Global Constraints

- Keep the existing Cask name and install command: `brew install --cask michiomochi/tap/atct`.
- Keep `.mcp.json`, plugin skill files, and harness hook registrations.  Do not turn the plugin into a Homebrew package.
- Do not add a new tap, Formula, dependency, download mechanism, cache, or launcher.
- An unavailable or older CLI must produce the documented install/upgrade instruction and must not start a daemon or ask for a permission decision.
- Retain all non-resolver assertions in `tests/wrapper_test.bash`.

---

### Task 1: Expose the installed CLI release version

**Files:**
- Modify: `cmd/atct/main.go`
- Modify: `cmd/atct/main_test.go`

**Interfaces:**
- Produces: `atct version`, printing the build-time `version` value to standard output without resolving `HOME`, creating `~/.atct`, or touching the daemon.
- Consumed by: plugin hooks' minimum-version guard.

- [ ] **Step 1: Add a failing command-level test**

Use the existing `buildAtctTestBinary` and `runAtctCommand` helpers to assert
that `atct version` succeeds and emits the build version as its only output.
Add parser coverage that `version` accepts no positional arguments and is shown
in usage text.

- [ ] **Step 2: Run the focused test to verify failure**

```sh
GOCACHE=/private/tmp/atct-go-cache go test ./cmd/atct -run 'TestVersion' -count=1
```

Expected: FAIL because `version` is not a valid subcommand.

- [ ] **Step 3: Implement the smallest command path**

Add `version` to `validSubcommands` and usage.  After argument parsing, handle
it before `os.UserHomeDir`, `MkdirAll`, or executable resolution, printing the
existing build-time `version` variable and returning.

- [ ] **Step 4: Re-run the focused test**

Run the command in Step 2. Expected: PASS.

### Task 2: Make plugin hooks use the PATH CLI and reject incompatible installations

**Files:**
- Modify: `hooks/session-start`
- Modify: `hooks/pre-ask`
- Modify: `hooks/stop`
- Modify: `tests/session_start_test.bash`
- Modify: `tests/pre_ask_test.bash`
- Modify: `tests/wrapper_test.bash`

**Interfaces:**
- Consumes: `atct` resolved by `command -v`, `atct version`, and the Claude
  plugin's adjacent manifest version.
- Produces: the existing normal hook actions for a current-or-newer CLI; an
  actionable Homebrew install/upgrade message for a missing or stale CLI.

- [ ] **Step 1: Write the failing shell-hook cases**

Update fixtures so the fake binary is made discoverable through `PATH`, rather
than copied to a plugin-adjacent `bin/` directory.  Add cases for a missing
command and an older reported version.  Assert the exact messages:

```text
ATCT: install or upgrade the CLI with: brew install --cask michiomochi/tap/atct
ATCT: upgrade the CLI with: brew upgrade --cask michiomochi/tap/atct
```

For the Claude stop hook, preserve the blocking JSON response around the
actionable reason. Keep the existing managed-context, ordinary-error, and
active-stop assertions.

- [ ] **Step 2: Run focused hook tests to verify failure**

```sh
bash tests/session_start_test.bash
bash tests/pre_ask_test.bash
bash tests/wrapper_test.bash
```

Expected: FAIL because Claude hooks still require the plugin-local
resolver/wrapper.

- [ ] **Step 3: Replace local resolution with a small, local guard**

In each Bash hook, use `command -v atct` and compare `atct version` with the
plugin manifest's `version` using the existing dotted-number shell comparison
pattern.  Read the manifest relative to the hook, not from the working
directory.  This guard performs no download, cache, extraction, launcher, or
filesystem write.

Keep each hook's present fail-closed/open behavior: session start does not
start a daemon; pre-ask does not return a decision; stop returns its existing
block envelope. Codex monitor hooks retain their `ATCT_BIN` path because the
Homebrew-installed command that starts the monitor injects it directly.

- [ ] **Step 4: Re-run focused hook tests**

Run the commands in Step 2. Expected: PASS, including current/newer CLI,
missing CLI, stale CLI, and the existing normal hook paths.

### Task 3: Remove resolver distribution and restore Cask publication

**Files:**
- Delete: `bin/atct`
- Delete: `bin/atct-mcp`
- Delete: `bin/_resolve`
- Delete: `tests/cache_prune_test.bash`
- Modify: `.goreleaser.yaml`
- Modify: `script/release.sh`
- Modify: `tests/wrapper_test.bash`
- Modify: `tests/release_test.bash`

**Interfaces:**
- Produces: Homebrew Cask generation from the ordinary GoReleaser release
  archive, installing both `atct` and `atct-mcp`.
- Removes: all plugin-side binary resolution, release download/checksum/cache
  behavior, `~/.local/bin` launcher creation, and version bumping of a
  resolver script.

- [ ] **Step 1: Change regression tests before deleting implementation**

Replace the static wrapper contract with assertions that the three `bin/`
resolver files are absent, the plugin manifests remain in sync, and hook
registrations remain present. Remove only the
resolver-oriented fixture helpers, test functions, and invocation lines from
`tests/wrapper_test.bash`; retain its role, handoff, skill, and hook-registry
checks.  Remove the resolver fixture and version assertion from
`tests/release_test.bash`.

- [ ] **Step 2: Remove resolver execution from the release gate**

Delete the cache-prune test invocation, the resolver-version mutation, and
the resolver file from `git add` in `script/release.sh`.  The release bump
continues to update and validate both plugin manifests.

- [ ] **Step 3: Restore the historical Cask stanza**

Restore the proven `homebrew_casks` block in `.goreleaser.yaml` targeting
`michiomochi/homebrew-tap`, with the existing archive layout, both binaries,
verified GitHub URL, and macOS quarantine-removal post-install hook.

- [ ] **Step 4: Delete the obsolete resolver files and test**

Delete `bin/atct`, `bin/atct-mcp`, `bin/_resolve`, and
`tests/cache_prune_test.bash` using `apply_patch` deletions.  Do not replace
them with another launcher or downloader.

- [ ] **Step 5: Run distribution regression checks**

```sh
bash tests/release_test.bash
bash tests/wrapper_test.bash
git diff --check
```

Expected: PASS; no resolver/cache/launcher references remain in the checked
distribution paths, and GoReleaser has exactly the Cask publication stanza.

### Task 4: Document the supported install path

**Files:**
- Modify: `README.md`

**Interfaces:**
- Produces: a single supported CLI installation route followed by plugin
  installation instructions.

- [ ] **Step 1: Replace the resolver-install description**

Put the Homebrew command before plugin setup:

```sh
brew install --cask michiomochi/tap/atct
```

State that the Cask installs both CLI binaries, while the plugin supplies MCP,
hooks, and skills.  Retain the generic MCP-client `atct-mcp` guidance.

- [ ] **Step 2: Check stale documentation**

```sh
rg -n 'cache|launcher|wrapper|download.*release|\.atct/bin|atct install' README.md bin hooks script .goreleaser.yaml
```

Expected: no stale plugin-resolver installation claim; intentional runtime
`~/.atct` database references are allowed.

### Task 5: Complete verification and commit

**Files:**
- Modify: none

- [ ] **Step 1: Format and run the complete test suite**

```sh
gofmt -w cmd/atct/main.go cmd/atct/main_test.go
GOCACHE=/private/tmp/atct-go-cache go test ./... -count=1
bash tests/session_start_test.bash
bash tests/pre_ask_test.bash
bash tests/release_test.bash
bash tests/wrapper_test.bash
git diff --check
```

Expected: all commands pass.

- [ ] **Step 2: Commit the implementation**

```sh
git add .goreleaser.yaml README.md cmd/atct/main.go cmd/atct/main_test.go hooks \
  script/release.sh tests .claude-plugin .codex-plugin
git commit -m "feat: distribute cli through homebrew"
```

The committed spec and this plan remain part of the project knowledge.
