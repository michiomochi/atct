#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SETUP_SCRIPT="$REPO_ROOT/script/worktree-setup.sh"
TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/atct-worktree-setup-test.XXXXXX")"
trap 'rm -rf -- "$TEMP_ROOT"' EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_eq() {
  local expected="$1"
  local actual="$2"
  local message="${3:-values differ}"
  [[ "$expected" == "$actual" ]] || fail "$message: expected <$expected>, got <$actual>"
}

assert_file_contains() {
  local needle="$1"
  local file="$2"
  grep -Fq -- "$needle" "$file" || fail "<$file> does not contain <$needle>"
}

init_repo() {
  local repo="$1"

  mkdir -p "$repo/script" "$repo/web/node_modules" "$repo/web/dist"
  cp -- "$SETUP_SCRIPT" "$repo/script/worktree-setup.sh"
  chmod +x "$repo/script/worktree-setup.sh"
  printf 'fixture repository\n' >"$repo/README.md"
  printf 'module fixture\n' >"$repo/web/node_modules/marker"
  printf 'tracked dist directory\n' >"$repo/web/dist/.gitkeep"
  printf 'dist fixture\n' >"$repo/web/dist/index.html"

  git -C "$repo" init -q -b main
  git -C "$repo" add README.md web/dist/.gitkeep
  git -C "$repo" -c user.name='worktree test' -c user.email='worktree-test@example.invalid' \
    commit -q -m 'initial fixture'
}

run_setup() {
  local repo="$1"
  local goal_id="$2"
  local output="$3"
  local status=0

  if (cd -- "$repo" && "$repo/script/worktree-setup.sh" "$goal_id") >"$output" 2>&1; then
    status=0
  else
    status=$?
  fi
  printf '%s\n' "$status"
}

test_full_uuid_uses_goal_prefix() {
  local repo="$TEMP_ROOT/full-uuid"
  local goal_id='bacacb8b-4f34-47d4-9bd9-699a112eb032'
  local goal8='bacacb8b'
  local output="$TEMP_ROOT/full-uuid.out"
  local status
  local worktree="$TEMP_ROOT/atct-wt-$goal8"

  init_repo "$repo"
  status="$(run_setup "$repo" "$goal_id" "$output")"

  assert_eq 0 "$status" 'full UUID setup status'
  [[ -d "$worktree" ]] || fail 'full UUID did not create the goal worktree'
  assert_eq "wt/goal-$goal8" "$(git -C "$worktree" branch --show-current)" \
    'full UUID branch'
}

test_short_goal_id_uses_goal_prefix() {
  local repo="$TEMP_ROOT/short-goal"
  local goal_id='deadbeef-4f34-47d4-9bd9-699a112eb032'
  local goal8='deadbeef'
  local output="$TEMP_ROOT/short-goal.out"
  local status
  local worktree="$TEMP_ROOT/atct-wt-$goal8"

  init_repo "$repo"
  status="$(run_setup "$repo" "$goal_id" "$output")"
  assert_eq 0 "$status" 'full UUID setup before short ID'

  status="$(run_setup "$repo" "$goal8" "$output")"
  assert_eq 0 "$status" 'short goal ID setup status'
  [[ -d "$worktree" ]] || fail 'short goal ID did not reuse the goal worktree'
  assert_eq "wt/goal-$goal8" "$(git -C "$worktree" branch --show-current)" \
    'short goal ID branch'
}

test_reusing_goal_id_is_idempotent() {
  local repo="$TEMP_ROOT/reuse"
  local goal_id='0123abcd-4f34-47d4-9bd9-699a112eb032'
  local goal8='0123abcd'
  local output="$TEMP_ROOT/reuse.out"
  local status
  local worktree="$TEMP_ROOT/atct-wt-$goal8"

  init_repo "$repo"
  status="$(run_setup "$repo" "$goal_id" "$output")"
  assert_eq 0 "$status" 'first repeated goal ID setup'

  status="$(run_setup "$repo" "$goal_id" "$output")"
  assert_eq 0 "$status" 'second repeated goal ID setup'
  [[ -d "$worktree" ]] || fail 'repeated goal ID worktree disappeared'
  assert_eq "wt/goal-$goal8" "$(git -C "$worktree" branch --show-current)" \
    'repeated goal ID branch'
}

test_numeric_goal_id_is_rejected_without_legacy_worktree() {
  local repo="$TEMP_ROOT/numeric"
  local output="$TEMP_ROOT/numeric.out"
  local status

  init_repo "$repo"
  status="$(run_setup "$repo" 1 "$output")"

  assert_eq 2 "$status" 'numeric goal ID status'
  [[ ! -e "$TEMP_ROOT/atct-wt1" && ! -L "$TEMP_ROOT/atct-wt1" ]] || \
    fail 'numeric goal ID created the legacy atct-wt1 worktree'
  assert_file_contains 'usage:' "$output"
}

test_missing_frontend_prerequisites_are_rejected() {
  local no_node_modules="$TEMP_ROOT/no-node-modules"
  local no_dist="$TEMP_ROOT/no-dist"
  local output="$TEMP_ROOT/missing.out"
  local status

  init_repo "$no_node_modules"
  rm -rf -- "$no_node_modules/web/node_modules"
  status="$(run_setup "$no_node_modules" a1b2c3d4 "$output")"
  assert_eq 2 "$status" 'missing web/node_modules status'
  [[ ! -e "$TEMP_ROOT/atct-wt-a1b2c3d4" && ! -L "$TEMP_ROOT/atct-wt-a1b2c3d4" ]] || \
    fail 'missing web/node_modules created a worktree'

  init_repo "$no_dist"
  rm -f -- "$no_dist/web/dist/index.html"
  status="$(run_setup "$no_dist" b1c2d3e4 "$output")"
  assert_eq 2 "$status" 'missing web/dist/index.html status'
  [[ ! -e "$TEMP_ROOT/atct-wt-b1c2d3e4" && ! -L "$TEMP_ROOT/atct-wt-b1c2d3e4" ]] || \
    fail 'missing web/dist/index.html created a worktree'
}

test_frontend_dependencies_are_linked_and_dist_is_copied() {
  local repo="$TEMP_ROOT/frontend"
  local goal8='c0ffee12'
  local output="$TEMP_ROOT/frontend.out"
  local status
  local worktree="$TEMP_ROOT/atct-wt-$goal8"
  local repo_path

  init_repo "$repo"
  status="$(run_setup "$repo" "$goal8" "$output")"

  assert_eq 0 "$status" 'frontend setup status'
  [[ -L "$worktree/web/node_modules" ]] || fail 'web/node_modules is not a symlink'
  repo_path="$(cd -- "$repo" && pwd)"
  assert_eq "$repo_path/web/node_modules" "$(readlink "$worktree/web/node_modules")" \
    'web/node_modules symlink target'
  assert_eq 'module fixture' "$(<"$worktree/web/node_modules/marker")" \
    'linked node_modules content'
  [[ -d "$worktree/web/dist" && ! -L "$worktree/web/dist" ]] || \
    fail 'web/dist is not a copied directory'
  assert_eq 'dist fixture' "$(<"$worktree/web/dist/index.html")" 'copied web/dist content'

  printf 'changed source\n' >"$repo/web/dist/index.html"
  assert_eq 'dist fixture' "$(<"$worktree/web/dist/index.html")" \
    'web/dist must not remain linked to the source'
}

test_full_uuid_uses_goal_prefix
test_short_goal_id_uses_goal_prefix
test_reusing_goal_id_is_idempotent
test_numeric_goal_id_is_rejected_without_legacy_worktree
test_missing_frontend_prerequisites_are_rejected
test_frontend_dependencies_are_linked_and_dist_is_copied
printf 'PASS: worktree setup (6 tests)\n'
