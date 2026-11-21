# Global `atct` CLI Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide bare `atct` through a stable, product-owned launcher without editing profiles.

**Architecture:** `bin/_resolve` remains the version/integrity authority and creates a guarded stable launcher that re-enters `bin/atct`.

**Tech Stack:** Bash, existing wrapper tests, Go release artifacts.

## Global Constraints

- Do not modify shell profiles, dotfiles, `PATH`, release state, or publish state.
- Never overwrite a non-ATCT path; diagnostics go to stderr and wrapper stdout stays unchanged.
- Do not edit Goal 209-owned Codex shim, `.mcp.json`, or `.codex-plugin/` files.

---

### Task 1: Add failing launcher tests

**Files:** Modify `tests/wrapper_test.bash`; modify `tests/cache_prune_test.bash` only for isolated-home fixture support.

**Produces:** Coverage for `$HOME/.local/bin/atct`.

- [ ] Add a first-run test: invoke repository `bin/atct` using the existing fake-release fixture and isolated `HOME`; assert an executable, marked launcher exists and forwards `--version`.
- [ ] Run `bash tests/wrapper_test.bash`; expect the new assertion to fail before implementation.
- [ ] Add collision test: an unmarked sentinel at the target remains byte-identical and resolution exits non-zero with the target path on stderr.
- [ ] Add refresh/no-profile tests: a marked old launcher is refreshed; seeded `.zshrc` and `.bashrc` checksums remain unchanged.

### Task 2: Implement guarded launcher creation

**Files:** Modify `bin/_resolve`; test `tests/wrapper_test.bash` and `tests/cache_prune_test.bash`.

**Consumes:** canonical repository wrapper path and marker `ATCT_GLOBAL_LAUNCHER_V1`.

**Produces:** `ensure_terminal_launcher()` and executable `$HOME/.local/bin/atct`.

- [ ] Define `ensure_terminal_launcher()`: `mkdir -p -- "$HOME/.local/bin"`, create a same-directory temporary Bash file containing the marker, canonical wrapper assignment, and `exec "$ATCT_WRAPPER" "$@"`; chmod 0755 then rename atomically.
- [ ] Before replacement reject symlinks, directories, non-regular files, and regular files missing the marker; return non-zero before any mutation.
- [ ] Call it after canonical wrapper resolution and before final cached-binary exec. Preserve `atct-mcp`'s `ATCT_ATCT_BIN` behavior and create no global `atct-mcp` command.
- [ ] Run `bash tests/wrapper_test.bash && bash tests/cache_prune_test.bash`; expect all focused tests to pass.
- [ ] Commit exactly: `git add bin/_resolve tests/wrapper_test.bash tests/cache_prune_test.bash && git commit -m "feat: install stable atct terminal launcher" -- bin/_resolve tests/wrapper_test.bash tests/cache_prune_test.bash`.

### Task 3: Document and verify user control

**Files:** Modify `README.md`.

- [ ] Document that the wrapper installs `~/.local/bin/atct` but never edits profiles; if it is not on `PATH`, the user may choose to add it or use the wrapper.
- [ ] Run `bash tests/wrapper_test.bash && bash tests/cache_prune_test.bash && bash tests/release_test.bash`; all exit zero without publishing.
- [ ] In an isolated `HOME` with its `.local/bin` on `PATH`, run the repository wrapper, `command -v atct`, and `atct --version`; confirm the stable launcher and fixture-selected version.
- [ ] Commit exactly: `git add README.md && git commit -m "docs: explain stable atct terminal launcher" -- README.md`.

## Plan self-review

Tasks cover creation, refresh, collisions, forwarding, no profile writes, cache compatibility, documentation, and focused regression tests without touching Goal 209 files.
