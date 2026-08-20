#!/usr/bin/env bash
# Hand a request file to an executor, refusing if the tasks are not claimed.
#
#   script/delegate.sh <executor> <request-file> <task-id>...
#
# Claiming is MCP-only: a claim belongs to an agent session, and a CLI process
# would hold one for the instant before it exits. So this cannot claim for you.
# What it can do is refuse to dispatch work nobody has claimed, which is the
# failure it exists to prevent -- delegating and leaving the tasks reading todo,
# so the dashboard says the work has not begun and the human plans around that.
#
# atct claim-check exits non-zero unless every task is claimed by an agent
# session whose process is still running, so a claim held by a session that died
# does not count.
set -euo pipefail

executor="${1:?usage: script/delegate.sh <executor> <request-file> <task-id>...}"
request="${2:?usage: script/delegate.sh <executor> <request-file> <task-id>...}"
shift 2
if [[ $# -eq 0 ]]; then
  echo "give at least one task_id: the tasks this request covers" >&2
  exit 2
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

if [[ ! -r "$request" ]]; then
  echo "cannot read the request file: $request" >&2
  exit 2
fi

atct="$(command -v atct || true)"
if [[ -z "$atct" ]]; then
  atct="$(ls -d "$HOME"/.claude/plugins/cache/atct/atct/*/bin/atct 2>/dev/null | sort -V | tail -1 || true)"
fi
if [[ -z "$atct" ]]; then
  echo "cannot find the atct binary" >&2
  exit 2
fi

echo "==> checking claims for $# task(s)"
if ! "$atct" claim-check "$@"; then
  cat >&2 <<MSG

Refusing to delegate. Claim these tasks first, with atct_task_claim, then run
this again. A claim is what tells the human the work has started and who holds
it; dispatching without one leaves the dashboard saying nothing has begun.
MSG
  exit 1
fi

echo "==> dispatching to $executor"
herdr agent prompt "$executor" "$(cat "$request")"
echo "==> sent"
