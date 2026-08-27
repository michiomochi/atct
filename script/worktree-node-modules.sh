#!/usr/bin/env bash
# Detach a reusable worktree from the primary checkout's frontend dependencies.
#
#   script/worktree-node-modules.sh status [<goal-id>]
#   script/worktree-node-modules.sh detach [<goal-id>]
#   script/worktree-node-modules.sh attach [<goal-id>] --yes
set -euo pipefail

usage() {
	echo "usage: script/worktree-node-modules.sh {status|detach|attach} [<goal-id>] [--yes]" >&2
}

fail() {
	echo "$1" >&2
	exit "${2:-2}"
}

if [[ $# -lt 1 ]]; then
	usage
	exit 2
fi

command_name="$1"
shift
goal_id=""
yes=0

case "$command_name" in
status|detach)
	if [[ $# -gt 1 ]]; then
		usage
		exit 2
	fi
	if [[ $# -eq 1 ]]; then
		goal_id="$1"
	fi
	;;
attach)
	while [[ $# -gt 0 ]]; do
		case "$1" in
		--yes)
			if (( yes == 1 )); then
				usage
				exit 2
			fi
			yes=1
			;;
		*)
			if [[ -n "$goal_id" ]]; then
				usage
				exit 2
			fi
			goal_id="$1"
			;;
		esac
		shift
	done
	;;
*)
	usage
	exit 2
	;;
esac

if [[ -n "$goal_id" && ! "$goal_id" =~ ^([1-9][0-9]*|[0-9a-f]{8,})$ ]]; then
	usage
	exit 2
fi

PNPM="${PNPM:-pnpm}"

if ! git_common_dir="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)"; then
	fail "Git の作業ツリー内で実行しろ" 2
fi
if ! repo="$(cd "$(dirname "$git_common_dir")" 2>/dev/null && pwd -P)"; then
	fail "主チェックアウトを解決できない" 2
fi
if ! repo_realpath="$(cd "$repo" 2>/dev/null && pwd -P)"; then
	fail "主チェックアウトを解決できない" 2
fi

if [[ -z "$goal_id" ]]; then
	if ! git_dir="$(git rev-parse --path-format=absolute --git-dir 2>/dev/null)"; then
		fail "作業ツリーを解決できない" 2
	fi
	if [[ "$git_dir" == "$git_common_dir" ]]; then
		fail "主チェックアウトでは goal-id を指定しろ" 2
	fi
	if ! worktree="$(git rev-parse --path-format=absolute --show-toplevel 2>/dev/null)"; then
		fail "作業ツリーを解決できない" 2
	fi
else
	goal8="${goal_id:0:8}"
	worktree="$repo/.worktrees/$goal8"
fi

if [[ ! -e "$worktree" && ! -L "$worktree" ]]; then
	fail "対象の作業ツリーがありません: $worktree" 2
fi
if ! worktree_realpath="$(cd "$worktree" 2>/dev/null && pwd -P)"; then
	fail "対象の作業ツリーを解決できない: $worktree" 2
fi
if [[ "$worktree_realpath" == "$repo_realpath" ]]; then
	fail "主チェックアウトは対象にできない" 2
fi

nm="$worktree/web/node_modules"
primary_nm="$repo/web/node_modules"

is_empty_dir() {
	local directory="$1"
	local -a entries
	shopt -s nullglob dotglob
	entries=("$directory"/*)
	shopt -u nullglob dotglob
	(( ${#entries[@]} == 0 ))
}

status() {
	if [[ -L "$nm" ]]; then
		printf 'attached: %s\n' "$worktree"
	elif [[ -d "$nm" ]]; then
		printf 'detached: %s\n' "$worktree"
	elif [[ ! -e "$nm" ]]; then
		printf 'missing: %s\n' "$worktree"
	else
		printf 'invalid: %s\n' "$worktree"
	fi
}

detach() {
	if [[ -L "$nm" ]]; then
		rm "$nm"
	elif [[ -d "$nm" ]]; then
		printf 'detached: %s\n' "$worktree"
		exit 0
	elif [[ -e "$nm" ]]; then
		fail "worktree web/node_modules がディレクトリでも symlink でもない: $nm" 2
	fi

	if [[ ! -d "$worktree/web" ]]; then
		fail "対象の web ディレクトリがありません: $worktree/web" 2
	fi
	(
		cd "$worktree/web"
		"$PNPM" install --frozen-lockfile
	)
	printf 'detached: %s\n' "$worktree"
}

attach() {
	if [[ -L "$nm" ]]; then
		printf 'attached: %s\n' "$worktree"
		exit 0
	fi

	if [[ -d "$nm" ]]; then
		if (( yes == 0 )); then
			printf '削除対象: %s。確認するなら --yes を指定しろ\n' "$nm" >&2
			exit 2
		fi
		case "$worktree_realpath" in
			"$repo_realpath/.worktrees/"*)
				;;
			*)
				fail "作業ツリーが .worktrees の外にあるため削除を拒否した: $worktree_realpath" 1
				;;
		esac
		if [[ ! -e "$nm/.pnpm" && ! -L "$nm/.pnpm" ]] && ! is_empty_dir "$nm"; then
			fail "pnpm 管理の node_modules に見えないため削除を拒否した: $nm" 1
		fi
	elif [[ -e "$nm" ]]; then
		fail "worktree web/node_modules がディレクトリでも symlink でもない: $nm" 1
	fi

	if [[ ! -d "$primary_nm" ]]; then
		fail "主チェックアウトに web/node_modules がありません: $primary_nm" 2
	fi
	if [[ -d "$nm" ]]; then
		rm -rf "$nm"
	fi
	ln -s "$primary_nm" "$nm"
	printf 'attached: %s\n' "$worktree"
}

case "$command_name" in
status)
	status
	;;
detach)
	detach
	;;
attach)
	attach
	;;
esac
