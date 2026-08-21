#!/usr/bin/env bash
# Refuse to hand a request file to an executor while its tasks read todo.
#
# Delegating without claiming leaves the dashboard saying the work has not begun,
# and the human plans around that dashboard. On 2026-08-20 I dispatched three
# executors and left eleven tasks unclaimed; the stop hook listed them as
# unstarted while the executors were editing those very files.
#
# Instructions could not fix this. From the Claude Code docs: "Claude treats them
# as context, not enforced configuration. To block an action regardless of what
# Claude decides, use a PreToolUse hook instead."
#
# Scope, deliberately narrow: only a `herdr agent prompt` that names a request
# file (a path matching req-*.md) is treated as delegation. Of the nineteen-plus
# herdr agent prompt calls on 2026-08-20, eight were delegations and the rest were
# answers to executors pushing back, corrections, information, and one withdrawal.
# Blocking those would make the tool unusable, so they pass.
#
# The limit is real and worth stating: writing the request inline instead of in a
# file walks straight past this. It closes the path actually taken, not every path.
set -uo pipefail

input="$(cat)"

# Cheap rejections first: this runs on every Bash call.
[[ "$input" != *"herdr agent prompt"* ]] && exit 0
[[ "$input" != *"req-"*".md"* ]] && exit 0

command_text="$(printf '%s' "$input" | python3 -c '
import json, sys
try:
    payload = json.load(sys.stdin)
except Exception:
    sys.exit(0)
tool = payload.get("tool_name") or payload.get("toolName") or ""
if tool != "Bash":
    sys.exit(0)
params = payload.get("tool_input") or payload.get("toolInput") or {}
print(params.get("command", ""))
' 2>/dev/null)"

[[ -z "$command_text" ]] && exit 0
[[ "$command_text" != *"herdr agent prompt"* ]] && exit 0
[[ "$command_text" != *"req-"*".md"* ]] && exit 0

# Find the atct binary. Prefer PATH, fall back to the newest plugin cache.
atct="$(command -v atct 2>/dev/null || true)"
if [[ -z "$atct" ]]; then
  atct="$(ls -d "$HOME"/.claude/plugins/cache/atct/atct/*/bin/atct 2>/dev/null | sort -V | tail -1 || true)"
fi
# Cannot judge without the binary. Let it through rather than blocking real work.
[[ -z "$atct" || ! -x "$atct" ]] && exit 0

# Any task claimed by a live session in this project means work is under way.
if "$atct" claim-check any >/dev/null 2>&1; then
  exit 0
fi

cat >&2 <<'MSG'
Claim the tasks this request covers before delegating.

Call atct_task_claim for each of them, then dispatch. A claim is what says the
work has started and who holds it; without one the dashboard reads todo and the
human plans around that. script/delegate.sh checks this for you.

If this prompt is not a delegation -- answering a push-back, sending a
correction, withdrawing a request -- leave the request file path out of it.
MSG
exit 2
