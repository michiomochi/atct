#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
RECLAIM_SCRIPT="$REPO_ROOT/script/worktree-reclaim.sh"
TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/atct-worktree-reclaim-test.XXXXXX")"
trap 'rm -rf -- "$TEMP_ROOT"' EXIT

export GIT_AUTHOR_NAME='reclaim test' GIT_AUTHOR_EMAIL='reclaim-test@example.invalid'
export GIT_COMMITTER_NAME='reclaim test' GIT_COMMITTER_EMAIL='reclaim-test@example.invalid'

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

  mkdir -p "$repo/script"
  cp -- "$RECLAIM_SCRIPT" "$repo/script/worktree-reclaim.sh"
  chmod +x "$repo/script/worktree-reclaim.sh"
  printf 'line\n' >"$repo/file.txt"
  printf 'web/node_modules\nweb/dist/\n' >"$repo/.gitignore"

  git -C "$repo" init -q -b main
  git -C "$repo" add file.txt .gitignore
  git -C "$repo" commit -q -m 'initial fixture'
}

add_worktree() {
  local repo="$1"
  local id="$2"
  git -C "$repo" worktree add -q "$repo/.worktrees/$id" -b "wt/goal-$id"
}

# run_reclaim <repo> <output> [args...]; prints the exit status.
run_reclaim() {
  local repo="$1"
  local output="$2"
  shift 2
  local status=0

  (cd -- "$repo" && "$repo/script/worktree-reclaim.sh" "$@") >"$output" 2>&1 || status=$?
  printf '%s\n' "$status"
}

branch_exists() {
  git -C "$1" show-ref --verify --quiet "refs/heads/$2"
}

test_usage_and_preconditions() {
  local repo="$TEMP_ROOT/usage"
  local out="$TEMP_ROOT/usage.out"

  init_repo "$repo"
  assert_eq 2 "$(run_reclaim "$repo" "$out")" 'no args status'
  assert_file_contains 'usage:' "$out"
  assert_eq 2 "$(run_reclaim "$repo" "$out" abc)" 'non-integer id status'
  assert_eq 2 "$(run_reclaim "$repo" "$out" deadbeef)" '8-hex id status'
  assert_eq 2 "$(run_reclaim "$repo" "$out" 0)" 'zero id status'
  assert_file_contains 'usage:' "$out"

  local status=0
  local outside="$TEMP_ROOT/not-a-repo"
  mkdir -p "$outside"
  (cd -- "$outside" && bash "$RECLAIM_SCRIPT" 7) >"$out" 2>&1 || status=$?
  assert_eq 2 "$status" 'outside a git repo status'
  assert_file_contains 'git リポジトリの中で実行しろ' "$out"

  add_worktree "$repo" 5
  # The script locates the repo from cwd, so cwd inside the worktree hits the git_dir != git_common_dir branch.
  status=0
  (cd -- "$repo/.worktrees/5" && bash "$RECLAIM_SCRIPT" 7) >"$out" 2>&1 || status=$?
  assert_eq 2 "$status" 'inside worktree status'
  [[ -d "$repo/.worktrees/5" ]] || fail 'inside-worktree run removed the worktree'

  git -C "$repo" checkout -q -b other
  assert_eq 2 "$(run_reclaim "$repo" "$out" 5)" 'HEAD not main status'
  [[ -d "$repo/.worktrees/5" ]] || fail 'HEAD-not-main run removed the worktree'
}

test_clean_and_merged() {
  local repo="$TEMP_ROOT/clean"
  local out="$TEMP_ROOT/clean.out"

  init_repo "$repo"
  add_worktree "$repo" 11
  assert_eq 0 "$(run_reclaim "$repo" "$out" 11)" 'clean status'
  [[ ! -e "$repo/.worktrees/11" ]] || fail 'worktree still exists'
  branch_exists "$repo" wt/goal-11 && fail 'merged branch was not deleted'
  assert_file_contains 'deleted branch wt/goal-11' "$out"
}

test_uncommitted_and_untracked_are_snapshotted() {
  local repo="$TEMP_ROOT/dirty"
  local out="$TEMP_ROOT/dirty.out"
  local wt="$repo/.worktrees/12"

  init_repo "$repo"
  add_worktree "$repo" 12
  printf 'changed\n' >"$wt/file.txt"
  printf 'brand new\n' >"$wt/new.txt"

  assert_eq 0 "$(run_reclaim "$repo" "$out" 12)" 'dirty status'
  [[ ! -e "$wt" ]] || fail 'worktree still exists'
  branch_exists "$repo" wt/goal-12 || fail 'branch with snapshot was deleted'
  assert_file_contains 'snapshot:' "$out"
  assert_eq 'changed' "$(git -C "$repo" show wt/goal-12:file.txt)" 'tracked change in snapshot'
  assert_eq 'brand new' "$(git -C "$repo" show wt/goal-12:new.txt)" 'untracked file in snapshot'
  assert_file_contains 'kept branch wt/goal-12 (1 commits not on main)' "$out"
}

test_merge_in_progress_is_snapshotted() {
  local repo="$TEMP_ROOT/merge"
  local out="$TEMP_ROOT/merge.out"
  local wt="$repo/.worktrees/13"

  init_repo "$repo"
  add_worktree "$repo" 13
  printf 'main side\n' >"$repo/file.txt"
  git -C "$repo" commit -q -am 'main change'
  printf 'branch side\n' >"$wt/file.txt"
  git -C "$wt" commit -q -am 'branch change'
  git -C "$wt" merge main >/dev/null 2>&1 && fail 'merge unexpectedly succeeded'
  [[ -n "$(git -C "$wt" ls-files -u)" ]] || fail 'fixture has no unmerged paths'

  assert_eq 0 "$(run_reclaim "$repo" "$out" 13)" 'merge-in-progress status'
  [[ ! -e "$wt" ]] || fail 'worktree still exists'
  assert_eq 3 "$(git -C "$repo" rev-list --parents -n 1 wt/goal-13 | wc -w | tr -d ' ')" \
    'branch tip is not a 2-parent commit'
  git -C "$repo" show wt/goal-13:file.txt | grep -Fq '<<<<<<<' || \
    fail 'conflict markers missing from snapshot tree'
  assert_eq '' "$(git -C "$repo" stash list)" 'stash list must be empty'
}

test_branch_with_unmerged_commit_is_kept() {
  local repo="$TEMP_ROOT/unmerged"
  local out="$TEMP_ROOT/unmerged.out"
  local wt="$repo/.worktrees/14"

  init_repo "$repo"
  add_worktree "$repo" 14
  printf 'work\n' >"$wt/work.txt"
  git -C "$wt" add work.txt
  git -C "$wt" commit -q -m 'goal work'

  assert_eq 0 "$(run_reclaim "$repo" "$out" 14)" 'unmerged status'
  [[ ! -e "$wt" ]] || fail 'worktree still exists'
  branch_exists "$repo" wt/goal-14 || fail 'branch was deleted'
  assert_file_contains 'kept branch wt/goal-14 (1 commits not on main)' "$out"
  assert_eq 'work' "$(git -C "$repo" show wt/goal-14:work.txt)" 'commit content lost'
}

test_rebase_in_progress_aborts() {
  local repo="$TEMP_ROOT/rebase"
  local out="$TEMP_ROOT/rebase.out"
  local wt="$repo/.worktrees/15"

  init_repo "$repo"
  add_worktree "$repo" 15
  printf 'main side\n' >"$repo/file.txt"
  git -C "$repo" commit -q -am 'main change'
  printf 'branch side\n' >"$wt/file.txt"
  git -C "$wt" commit -q -am 'branch change'
  git -C "$wt" rebase main >/dev/null 2>&1 && fail 'rebase unexpectedly succeeded'
  [[ -d "$(git -C "$wt" rev-parse --absolute-git-dir)/rebase-merge" ]] || \
    fail 'fixture has no rebase in progress'

  assert_eq 1 "$(run_reclaim "$repo" "$out" 15)" 'rebase status'
  [[ -d "$wt" ]] || fail 'worktree was removed during rebase'
  grep -Fq '<<<<<<<' "$wt/file.txt" || fail 'conflict state lost'
  branch_exists "$repo" wt/goal-15 || fail 'branch was deleted'
}

test_detached_head_aborts() {
  local repo="$TEMP_ROOT/detached"
  local out="$TEMP_ROOT/detached.out"
  local wt="$repo/.worktrees/18"

  init_repo "$repo"
  add_worktree "$repo" 18
  git -C "$wt" checkout -q --detach
  printf 'precious\n' >"$wt/file.txt"

  assert_eq 1 "$(run_reclaim "$repo" "$out" 18)" 'detached HEAD status'
  [[ -d "$wt" ]] || fail 'worktree was removed with detached HEAD'
  assert_eq 'precious' "$(<"$wt/file.txt")" 'uncommitted change lost'
  branch_exists "$repo" wt/goal-18 || fail 'branch was deleted'
}

test_only_ignored_files_dirty() {
  local repo="$TEMP_ROOT/ignored"
  local out="$TEMP_ROOT/ignored.out"
  local wt="$repo/.worktrees/16"

  init_repo "$repo"
  mkdir -p "$repo/web/node_modules"
  printf 'module fixture\n' >"$repo/web/node_modules/marker"
  add_worktree "$repo" 16
  mkdir -p "$wt/web/dist"
  ln -s "$repo/web/node_modules" "$wt/web/node_modules"
  printf 'built\n' >"$wt/web/dist/index.html"
  [[ -z "$(git -C "$wt" status --porcelain)" ]] || fail 'fixture is not clean apart from ignored files'

  assert_eq 0 "$(run_reclaim "$repo" "$out" 16)" 'ignored-only status'
  [[ ! -e "$wt" ]] || fail 'worktree still exists'
  ! grep -Fq 'snapshot:' "$out" || fail 'snapshot commit was made for ignored files'
  branch_exists "$repo" wt/goal-16 && fail 'merged branch was not deleted'
  assert_eq 'module fixture' "$(<"$repo/web/node_modules/marker")" \
    'primary web/node_modules was damaged'
}

# atct worktree-reclaim feeds the script to bash on stdin, where BASH_SOURCE[0] is empty.
test_script_on_stdin_reclaims() {
  local repo="$TEMP_ROOT/stdin"
  local out="$TEMP_ROOT/stdin.out"
  local status=0

  init_repo "$repo"
  add_worktree "$repo" 19
  (cd -- "$repo" && bash -s -- 19 <"$RECLAIM_SCRIPT") >"$out" 2>&1 || status=$?
  assert_eq 0 "$status" 'stdin status'
  [[ ! -e "$repo/.worktrees/19" ]] || fail 'worktree still exists'
  branch_exists "$repo" wt/goal-19 && fail 'merged branch was not deleted'
  assert_file_contains 'deleted branch wt/goal-19' "$out"
}

test_nothing_to_reclaim() {
  local repo="$TEMP_ROOT/nothing"
  local out="$TEMP_ROOT/nothing.out"

  init_repo "$repo"
  add_worktree "$repo" 99
  assert_eq 0 "$(run_reclaim "$repo" "$out" 17)" 'nothing-found status'
  assert_file_contains 'nothing found' "$out"
  [[ -d "$repo/.worktrees/99" ]] || fail 'unrelated worktree was removed'
  branch_exists "$repo" wt/goal-99 || fail 'unrelated branch was removed'
}

test_usage_and_preconditions
test_clean_and_merged
test_uncommitted_and_untracked_are_snapshotted
test_merge_in_progress_is_snapshotted
test_branch_with_unmerged_commit_is_kept
test_rebase_in_progress_aborts
test_detached_head_aborts
test_only_ignored_files_dirty
test_nothing_to_reclaim
test_script_on_stdin_reclaims
printf 'PASS: worktree reclaim (10 tests)\n'
