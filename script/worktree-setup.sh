#!/usr/bin/env bash
# Prepare a reusable executor worktree by borrowing frontend dependencies from
# the primary checkout.
#
#   script/worktree-setup.sh <executor-number>
#
# node_modules is shared with the primary checkout. Running pnpm install in a
# worktree changes the primary checkout's dependencies too.
# web/dist is copied from the primary checkout's build. If web/src changes in
# a worktree, run pnpm build there once to refresh the copied output.
set -euo pipefail

executor_number="${1:?usage: script/worktree-setup.sh <executor-number>}"
if [[ $# -ne 1 || ! "$executor_number" =~ ^[0-9]+$ ]]; then
  echo "usage: script/worktree-setup.sh <executor-number>" >&2
  exit 2
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
worktree="$(cd "$repo/.." && pwd)/atct-wt${executor_number}"
branch="wt/executor-${executor_number}"

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
