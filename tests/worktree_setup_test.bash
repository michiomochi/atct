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

test_reusing_goal_id_is_idempotent() {
  local repo="$TEMP_ROOT/reuse"
  local output="$TEMP_ROOT/reuse.out"
  local status
  local worktree="$repo/.worktrees/12"

  init_repo "$repo"
  status="$(run_setup "$repo" 12 "$output")"
  assert_eq 0 "$status" 'first repeated goal ID setup'

  status="$(run_setup "$repo" 12 "$output")"
  assert_eq 0 "$status" 'second repeated goal ID setup'
  [[ -d "$worktree" ]] || fail 'repeated goal ID worktree disappeared'
  assert_eq 'wt/goal-12' "$(git -C "$worktree" branch --show-current)" \
    'repeated goal ID branch'
}

test_numeric_goal_id_creates_goal_worktree() {
  local repo="$TEMP_ROOT/numeric"
  local output="$TEMP_ROOT/numeric.out"
  local status
  local worktree="$repo/.worktrees/21"

  init_repo "$repo"
  status="$(run_setup "$repo" 21 "$output")"

  assert_eq 0 "$status" 'numeric goal ID status'
  [[ -d "$worktree" ]] || fail 'numeric goal ID did not create the goal worktree'
  assert_eq 'wt/goal-21' "$(git -C "$worktree" branch --show-current)" \
    'numeric goal ID branch'
}

test_long_numeric_goal_id_is_not_truncated() {
  local repo="$TEMP_ROOT/long-numeric"
  local output="$TEMP_ROOT/long-numeric.out"
  local status
  local worktree="$repo/.worktrees/1234567890"

  init_repo "$repo"
  status="$(run_setup "$repo" 1234567890 "$output")"

  assert_eq 0 "$status" 'long numeric goal ID status'
  [[ -d "$worktree" ]] || fail 'long numeric goal ID did not create the goal worktree'
  assert_eq 'wt/goal-1234567890' "$(git -C "$worktree" branch --show-current)" \
    'long numeric goal ID branch'
}

test_legacy_goal_ids_are_rejected() {
  local repo="$TEMP_ROOT/legacy"
  local output="$TEMP_ROOT/legacy.out"
  local status
  local goal_id

  init_repo "$repo"
  for goal_id in bacacb8b-4f34-47d4-9bd9-699a112eb032 deadbeef; do
    status="$(run_setup "$repo" "$goal_id" "$output")"
    assert_eq 2 "$status" "legacy goal ID <$goal_id> status"
    assert_file_contains 'usage:' "$output"
    [[ ! -e "$repo/.worktrees/${goal_id:0:8}" && ! -e "$repo/.worktrees/$goal_id" ]] || \
      fail "legacy goal ID <$goal_id> created a worktree"
  done
}

test_malformed_goal_id_is_rejected() {
  local repo="$TEMP_ROOT/malformed"
  local output="$TEMP_ROOT/malformed.out"
  local status
  local goal_id

  init_repo "$repo"
  for goal_id in abc 0 -1; do
    status="$(run_setup "$repo" "$goal_id" "$output")"
    assert_eq 2 "$status" "malformed goal ID <$goal_id> status"
    assert_file_contains 'usage:' "$output"
  done

  if (cd -- "$repo" && "$repo/script/worktree-setup.sh") >"$output" 2>&1; then
    status=0
  else
    status=$?
  fi
  assert_eq 2 "$status" 'missing goal ID status'
  assert_file_contains 'usage:' "$output"
}

test_missing_frontend_prerequisites_are_rejected() {
  local no_node_modules="$TEMP_ROOT/no-node-modules"
  local no_dist="$TEMP_ROOT/no-dist"
  local output="$TEMP_ROOT/missing.out"
  local status

  init_repo "$no_node_modules"
  rm -rf -- "$no_node_modules/web/node_modules"
  status="$(run_setup "$no_node_modules" 21 "$output")"
  assert_eq 2 "$status" 'missing web/node_modules status'
  [[ ! -e "$no_node_modules/.worktrees/21" && ! -L "$no_node_modules/.worktrees/21" ]] || \
    fail 'missing web/node_modules created a worktree'

  init_repo "$no_dist"
  rm -f -- "$no_dist/web/dist/index.html"
  status="$(run_setup "$no_dist" 22 "$output")"
  assert_eq 2 "$status" 'missing web/dist/index.html status'
  [[ ! -e "$no_dist/.worktrees/22" && ! -L "$no_dist/.worktrees/22" ]] || \
    fail 'missing web/dist/index.html created a worktree'
}

test_frontend_dependencies_are_linked_and_dist_is_copied() {
  local repo="$TEMP_ROOT/frontend"
  local goal_id=31
  local output="$TEMP_ROOT/frontend.out"
  local status
  local worktree="$repo/.worktrees/$goal_id"
  local repo_path

  init_repo "$repo"
  status="$(run_setup "$repo" "$goal_id" "$output")"

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

test_reusing_goal_id_is_idempotent
test_numeric_goal_id_creates_goal_worktree
test_long_numeric_goal_id_is_not_truncated
test_legacy_goal_ids_are_rejected
test_malformed_goal_id_is_rejected
test_missing_frontend_prerequisites_are_rejected
test_frontend_dependencies_are_linked_and_dist_is_copied
printf 'PASS: worktree setup (7 tests)\n'
