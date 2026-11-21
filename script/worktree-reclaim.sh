#!/usr/bin/env bash
# Reclaim a done/dropped goal's worktree without losing work.
#
#   atct worktree-reclaim <goal-id>
#
# Run from the primary checkout. The repo is located from cwd, because
# atct feeds this script to `bash -s` on stdin, where BASH_SOURCE[0] is empty.
#
# Uncommitted work (including a merge in progress) is snapshot-committed onto
# the goal's own branch, then the worktree is removed WITHOUT --force. The
# branch is deleted only when it is already merged into main; otherwise it is
# kept. Ignored files (web/node_modules symlink, copied web/dist) are
# re-creatable and are discarded. git stash is never used: it fails with
# unmerged paths and loses the work.
#
# Does not check goal status; the commander verifies that beforehand.
#
# Exit: 0 reclaimed (also when the branch is kept), 1 aborted because work
# could be lost (worktree untouched), 2 usage / not primary / HEAD not main.
set -euo pipefail

goal_id="${1:-}"
if [[ $# -ne 1 || ! "$goal_id" =~ ^[1-9][0-9]*$ ]]; then
  echo "usage: atct worktree-reclaim <goal-id>" >&2
  exit 2
fi

if ! repo="$(git rev-parse --show-toplevel 2>/dev/null)"; then
  echo "git リポジトリの外では実行できない。主チェックアウトの git リポジトリの中で実行しろ" >&2
  exit 2
fi
git_dir="$(git -C "$repo" rev-parse --absolute-git-dir)"
git_common_dir="$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir)"
if [[ "$git_dir" != "$git_common_dir" ]]; then
  echo "作業ツリーの中では実行できない。主チェックアウトで実行しろ" >&2
  exit 2
fi
if [[ "$(git -C "$repo" symbolic-ref --short -q HEAD || true)" != "main" ]]; then
  echo "主チェックアウトの HEAD が main ではない" >&2
  exit 2
fi

worktree="$repo/.worktrees/${goal_id}"
branch="wt/goal-${goal_id}"
found=0

if [[ -e "$worktree" || -L "$worktree" ]]; then
  found=1
  wt_git_dir="$(git -C "$worktree" rev-parse --absolute-git-dir)"
  wt_head="$(git -C "$worktree" symbolic-ref --short -q HEAD || true)"
  if [[ "$wt_head" != "$branch" ]]; then
    echo "作業ツリーの HEAD が $branch ではない (${wt_head:-detached})。スナップショットが goal の branch に載らないので中止する" >&2
    exit 1
  fi
  for state in rebase-merge rebase-apply CHERRY_PICK_HEAD REVERT_HEAD BISECT_LOG; do
    if [[ -e "$wt_git_dir/$state" ]]; then
      echo "作業ツリーに $state がある。スナップショットコミットでは保存できないので中止する" >&2
      exit 1
    fi
  done

  if [[ -n "$(git -C "$worktree" status --porcelain)" ]]; then
    git -C "$worktree" add -A
    if ! git -C "$worktree" commit -q --no-verify -m "snapshot: abandoned worktree of goal ${goal_id}"; then
      echo "スナップショットコミットに失敗した。何も削除していない" >&2
      exit 1
    fi
    echo "snapshot: $(git -C "$worktree" rev-parse --short HEAD)"
  fi

  if ! git -C "$repo" worktree remove "$worktree"; then
    echo "git worktree remove に失敗した" >&2
    exit 1
  fi
  echo "removed worktree"
fi

if git -C "$repo" show-ref --verify --quiet "refs/heads/$branch"; then
  found=1
  if git -C "$repo" merge-base --is-ancestor "$branch" main; then
    git -C "$repo" branch -d "$branch" >/dev/null
    echo "deleted branch $branch"
  else
    echo "kept branch $branch ($(git -C "$repo" rev-list --count "main..$branch") commits not on main)"
  fi
fi

if [[ $found -eq 0 ]]; then
  echo "nothing found for goal ${goal_id} (no worktree, no branch)"
fi
