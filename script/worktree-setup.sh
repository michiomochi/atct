#!/usr/bin/env bash
# Prepare a reusable executor worktree by borrowing frontend dependencies from
# the primary checkout.
#
#   script/worktree-setup.sh <goal-id>
#
# node_modules is shared with the primary checkout. Running pnpm install in a
# worktree changes the primary checkout's dependencies too.
# web/dist is copied from the primary checkout's build. If web/src changes in
# a worktree, run pnpm build there once to refresh the copied output.
set -euo pipefail

goal_id="${1:-}"
if [[ $# -ne 1 || ! "$goal_id" =~ ^[1-9][0-9]*$ ]]; then
  echo "usage: script/worktree-setup.sh <goal-id>" >&2
  exit 2
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
git_dir="$(git -C "$repo" rev-parse --absolute-git-dir)"
git_common_dir="$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir)"
if [[ "$git_dir" != "$git_common_dir" ]]; then
  echo "作業ツリーの中では実行できない。主チェックアウトで実行しろ" >&2
  exit 2
fi
worktree="$repo/.worktrees/${goal_id}"
branch="wt/goal-${goal_id}"

if [[ ! -d "$repo/web/node_modules" ]]; then
  echo "主チェックアウトに web/node_modules がありません。先に主で pnpm install を走らせろ" >&2
  exit 2
fi
if [[ ! -f "$repo/web/dist/index.html" ]]; then
  echo "主チェックアウトに web/dist/index.html がありません。先に主で pnpm build を走らせろ" >&2
  exit 2
fi

if [[ ! -e "$worktree" && ! -L "$worktree" ]]; then
  if git show-ref --verify --quiet "refs/heads/$branch"; then
    git worktree add "$worktree" "$branch"
  else
    git worktree add "$worktree" -b "$branch"
  fi
fi

worktree_node_modules="$worktree/web/node_modules"
if [[ -L "$worktree_node_modules" ]]; then
  :
elif [[ -e "$worktree_node_modules" ]]; then
  echo "refusing to overwrite existing worktree web/node_modules: $worktree_node_modules" >&2
  exit 1
else
  ln -s "$repo/web/node_modules" "$worktree_node_modules"
fi

cp -R "$repo/web/dist/." "$worktree/web/dist/"

printf 'worktree: %s\nnext: cd %s && go test ./...\n' "$worktree" "$worktree"
