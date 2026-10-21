#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/atct-wrapper-test.XXXXXX")"
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

assert_file_matches() {
  local pattern="$1"
  local file="$2"
  grep -Eq -- "$pattern" "$file" || fail "<$file> does not match <$pattern>"
}

assert_file_not_contains() {
  local needle="$1"
  local file="$2"
  if grep -Fq -- "$needle" "$file"; then
    fail "<$file> must not contain <$needle>"
  fi
}

assert_empty_file() {
  local file="$1"
  [[ ! -s "$file" ]] || fail "<$file> is not empty"
}

# Headings named here must carry a numbered list in the body of
# skills/atct/SKILL.md. A section whose steps are ordered but unnumbered cannot
# be spotted from the text alone, so it is registered here by hand.
# skill|heading. 6979a33 moved four of these into the role skills, so the file
# is part of the entry: a heading checked against the wrong skill finds nothing
# and the section silently stops being checked at all.
ORDERED_SECTIONS=(
  'atct|## Declare before you work'
  'atct|## Handoff review order'
  'commander|## Delegate a goal'
  'atct|## Fill in a report on a handoff that is already closed'
  'atct|## Recover when your role comes back wrong'
  'executor|## task_worker: finishing a task handoff received before the two-layer change'
)

# Every skill whose numbered lists are checked for contiguity.
NUMBERED_SKILLS=(atct start commander executor)

# Skills whose numbered sections must also name the cost of running out of
# order. `start` is not one: it never carried the marker.
ORDERED_SECTION_SKILLS=(atct commander executor)

# Prints one line per broken numbered list, and nothing when every list is
# sound. Sections are split on `## ` and `### `; within a section the body
# series (`1. `) and the quoted series (`> 1. `, as transcribed into a request)
# are counted apart.
numbering_violations() {
  local file="$1"

  awk '
    function report(kind, numbers, message) {
      printf "%s: %s: %s numbering %s: %s\n", FILENAME, section, kind, message, numbers
    }
    function check(kind, numbers, count,   i, parts) {
      if (count == 0) return
      if (count == 1) {
        report(kind, numbers, "has only one item")
        return
      }
      split(numbers, parts, " ")
      if (parts[1] != 1) {
        report(kind, numbers, "does not start at 1")
        return
      }
      for (i = 2; i <= count; i++) {
        if (parts[i] != parts[i - 1] + 1) {
          report(kind, numbers, "is not contiguous (" parts[i] " follows " parts[i - 1] ")")
          return
        }
      }
    }
    function flush() {
      check("body", body, body_count)
      check("quote", quote, quote_count)
      body = ""
      body_count = 0
      quote = ""
      quote_count = 0
    }
    BEGIN { section = "(before the first heading)" }
    /^## |^### / {
      flush()
      section = $0
      next
    }
    /^[0-9]+\. / {
      n = $0
      sub(/\..*/, "", n)
      body = (body_count == 0) ? n : body " " n
      body_count++
      next
    }
    /^[ \t]*> [0-9]+\. / {
      n = $0
      sub(/^[ \t]*> /, "", n)
      sub(/\..*/, "", n)
      quote = (quote_count == 0) ? n : quote " " n
      quote_count++
      next
    }
    END { flush() }
  ' "$file"
}

# Prints one line per section that numbers its body steps without naming the
# consequence of running them out of order, and nothing when every such section
# names it exactly once. A quoted series carries no such requirement; the body
# of the same section already states it.
out_of_order_violations() {
  local file="$1"

  awk '
    function flush() {
      if (body_count > 0) {
        if (marker_count == 0) {
          printf "%s: %s: numbers its steps but has no line starting with **Out of order:**\n", \
            FILENAME, section
        } else if (marker_count > 1) {
          printf "%s: %s: has %d lines starting with **Out of order:** (expected 1)\n", \
            FILENAME, section, marker_count
        }
      }
      body_count = 0
      marker_count = 0
    }
    BEGIN { section = "(before the first heading)" }
    /^## |^### / {
      flush()
      section = $0
      next
    }
    /^[0-9]+\. / {
      body_count++
      next
    }
    /^\*\*Out of order:\*\*/ {
      marker_count++
      next
    }
    END { flush() }
  ' "$file"
}

test_static_contract() {
  [[ ! -e "$REPO_ROOT/bin/atct" ]] || fail 'bin/atct resolver must be absent'
  [[ ! -e "$REPO_ROOT/bin/atct-mcp" ]] || fail 'bin/atct-mcp resolver must be absent'
  [[ ! -e "$REPO_ROOT/bin/_resolve" ]] || fail 'bin/_resolve must be absent'
  assert_file_contains '"type": "http"' "$REPO_ROOT/.mcp.json"
  assert_file_contains '"url": "http://127.0.0.1:8787/mcp"' "$REPO_ROOT/.mcp.json"
  if grep -Fq 'CLAUDE_PLUGIN_ROOT' "$REPO_ROOT/.mcp.json"; then
    fail '.mcp.json must not name CLAUDE_PLUGIN_ROOT'
  fi
  if ! python3 - "$REPO_ROOT/.claude-plugin/plugin.json" "$REPO_ROOT/.codex-plugin/plugin.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    claude = json.load(stream)
with open(sys.argv[2], encoding="utf-8") as stream:
    codex = json.load(stream)

if claude.get("hooks") != "./hooks/claude-hooks.json":
    raise SystemExit(f"Claude hooks path = {claude.get('hooks')!r}")
if codex.get("hooks") != "./hooks/codex-hooks.json":
    raise SystemExit(f"Codex hooks path = {codex.get('hooks')!r}")
PY
  then
    fail 'plugin manifests must register their harness-specific hooks'
  fi
  assert_file_contains '"source": "./"' "$REPO_ROOT/.claude-plugin"/marketplace.json
  local plugin_version codex_version
  plugin_version="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.claude-plugin"/plugin.json)"
  codex_version="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.codex-plugin"/plugin.json)"
  [[ -n "$plugin_version" ]] || fail 'plugin.json has no version'
  assert_eq "$plugin_version" "$codex_version" 'plugin manifests must declare the same version'
  assert_file_not_contains 'ATCT_BIN' "$REPO_ROOT/hooks/codex-hooks.json"
  assert_file_contains "$codex_version" "$REPO_ROOT/hooks/codex-hooks.json"
  assert_file_contains 'homebrew_casks:' "$REPO_ROOT/.goreleaser.yaml"
  assert_file_contains 'name: atct' "$REPO_ROOT/.goreleaser.yaml"
  assert_file_contains 'name: homebrew-tap' "$REPO_ROOT/.goreleaser.yaml"
  assert_file_contains 'binaries: [atct, atct-mcp]' "$REPO_ROOT/.goreleaser.yaml"
  [[ -f "$REPO_ROOT/.mcp.json" ]] || fail 'repository root must contain .mcp.json'
}

test_stop_hooks_share_server_check() {
  local plugin_version
  plugin_version="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.codex-plugin/plugin.json")"
  if python3 - "$REPO_ROOT/hooks/claude-hooks.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    hooks = json.load(stream)["hooks"]

stop = hooks.get("Stop")
if stop != [{
    "hooks": [{
        "type": "command",
        "command": '"${CLAUDE_PLUGIN_ROOT}/hooks/stop"',
        "shell": "bash",
        "async": False,
    }],
}]:
    raise SystemExit(f"Stop hook registration is not the shared command: {stop!r}")
PY
  then
    :
  else
    fail 'claude-hooks.json must register the shared Stop hook'
  fi

  local fixture="$TEMP_ROOT/stop-hook"
  local hook="$fixture/hooks/stop"
  local adjacent="$fixture/bin/atct"
  local log="$fixture/atct.log"
  local output input='{"session_id":"hook-session-1","stop_hook_active":false}'

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")" "$fixture/.claude-plugin"
  cp "$REPO_ROOT/hooks/stop" "$hook"
  printf '{"version":"%s"}\n' "$plugin_version" >"$fixture/.claude-plugin/plugin.json"
  cat >"$adjacent" <<'SCRIPT'
#!/usr/bin/env bash
if [[ "${1:-}" == version ]]; then
  printf '%s\n' "$ATCT_TEST_VERSION"
  exit 0
fi
input="$(cat)"
printf '%s\n%s\n' "$*" "$input" >>"$ATCT_STOP_LOG"
if [[ "${1:-}" == session-key ]]; then
  printf '%s' 'ATCT session key: hook-session-1. Before any ATCT work, run `atct execution-flow` and follow its procedure.'
  exit 0
fi
if [[ "$input" == *'"stop_hook_active":true'* ]]; then exit 0; fi
printf '%s' '{"decision":"block","reason":"ATCT work remains: shared session"}'
SCRIPT
  chmod +x "$hook" "$adjacent"
  export ATCT_TEST_VERSION="$plugin_version"

  output="$(PATH="$(dirname "$adjacent"):$PATH" ATCT_STOP_LOG="$log" /bin/bash "$hook" <<< "$input")"
  assert_eq '{"decision":"block","reason":"ATCT work remains: shared session"}' "$output" 'Claude Stop hook must return the shared stop-check response'
  assert_eq $'stop-check --hook-input\n{"session_id":"hook-session-1","stop_hook_active":false}' "$(<"$log")" 'Claude Stop hook must pass raw hook input to shared stop-check'

  : >"$log"
  local codex_fixture="$TEMP_ROOT/codex-stop-hook"
  local codex_atct="$codex_fixture/atct"
  local command
  mkdir -p "$codex_fixture"
  cp "$adjacent" "$codex_atct"
  command="$(python3 - "$REPO_ROOT/hooks/codex-hooks.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    print(json.load(stream)["hooks"]["Stop"][0]["hooks"][0]["command"])
PY
)"

  : >"$log"
  output="$(PATH="$codex_fixture:/usr/bin:/bin" ATCT_STOP_LOG="$log" sh -c "$command" <<< "$input")"
  assert_eq '{"decision":"block","reason":"ATCT work remains: shared session"}' "$output" 'Codex Stop hook must return the shared stop-check response'
  assert_eq $'stop-check --hook-input\n{"session_id":"hook-session-1","stop_hook_active":false}' "$(<"$log")" 'Codex Stop hook must pass raw hook input to shared stop-check'

  : >"$log"
  output="$(PATH="$(dirname "$adjacent"):$PATH" ATCT_STOP_LOG="$log" /bin/bash "$hook" <<< '{"session_id":"hook-session-1","stop_hook_active":true}')"
  assert_eq '' "$output" 'active Claude Stop hook must be silent'
  assert_eq $'stop-check --hook-input\n{"session_id":"hook-session-1","stop_hook_active":true}' "$(<"$log")" 'Claude Stop hook must preserve active input for shared CLI'

  : >"$log"
  output="$(PATH="$codex_fixture:/usr/bin:/bin" ATCT_STOP_LOG="$log" sh -c "$command" <<< '{"session_id":"hook-session-1","stop_hook_active":true}')"
  assert_eq '' "$output" 'active Codex Stop hook must be silent'
  assert_eq $'stop-check --hook-input\n{"session_id":"hook-session-1","stop_hook_active":true}' "$(<"$log")" 'Codex Stop hook must preserve active input for shared CLI'

  output="$(PATH="/usr/bin:/bin" sh -c "$command" <<< "$input")"
  assert_eq '{"decision":"block","reason":"ATCT: install or upgrade the CLI with: brew install --cask michiomochi/tap/atct"}' "$output" 'missing Codex Stop hook must block with Homebrew installation instruction'

  local older_codex_fixture="$TEMP_ROOT/older-codex-stop-hook"
  local older_codex_atct="$older_codex_fixture/atct"
  mkdir -p "$older_codex_fixture"
  cat >"$older_codex_atct" <<'SCRIPT'
#!/usr/bin/env bash
if [[ "${1:-}" == version ]]; then
  printf '0.62.0\n'
fi
SCRIPT
  chmod +x "$older_codex_atct"
  output="$(PATH="$older_codex_fixture:/usr/bin:/bin" sh -c "$command" <<< "$input")"
  assert_eq '{"decision":"block","reason":"ATCT: upgrade the CLI with: brew upgrade --cask michiomochi/tap/atct"}' "$output" 'older Codex Stop hook must block with Homebrew upgrade instruction'

  local session_command
  session_command="$(python3 - "$REPO_ROOT/hooks/codex-hooks.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    print(json.load(stream)["hooks"]["SessionStart"][0]["hooks"][0]["command"])
PY
)"
  : >"$log"
  output="$(PATH="$codex_fixture:/usr/bin:/bin" ATCT_STOP_LOG="$log" sh -c "$session_command" <<< "$input")"
  assert_eq 'ATCT session key: hook-session-1. Before any ATCT work, run `atct execution-flow` and follow its procedure.' "$output" 'Codex SessionStart hook must use the PATH CLI'
  assert_eq $'session-key --hook-input\n{"session_id":"hook-session-1","stop_hook_active":false}' "$(<"$log")" 'Codex SessionStart hook must pass raw hook input to shared CLI'
  output="$(PATH="$older_codex_fixture:/usr/bin:/bin" sh -c "$session_command" <<< "$input")"
  assert_eq 'ATCT: upgrade the CLI with: brew upgrade --cask michiomochi/tap/atct' "$output" 'older Codex SessionStart hook must print Homebrew upgrade instruction'

  local missing_fixture="$TEMP_ROOT/missing-stop-hook"
  local missing_hook="$missing_fixture/hooks/stop"
  mkdir -p "$(dirname "$missing_hook")" "$missing_fixture/.claude-plugin"
  cp "$REPO_ROOT/hooks/stop" "$missing_hook"
  printf '{"version":"%s"}\n' "$plugin_version" >"$missing_fixture/.claude-plugin/plugin.json"
  output="$(PATH="/usr/bin:/bin" /bin/bash "$missing_hook" <<< "$input")"
  assert_eq '{"decision":"block","reason":"ATCT: install or upgrade the CLI with: brew install --cask michiomochi/tap/atct"}' "$output" 'missing Claude Stop hook must block with Homebrew installation instruction'

  local older_fixture="$TEMP_ROOT/older-stop-hook"
  local older_hook="$older_fixture/hooks/stop"
  local older_atct="$older_fixture/bin/atct"
  mkdir -p "$(dirname "$older_hook")" "$(dirname "$older_atct")" "$older_fixture/.claude-plugin"
  cp "$REPO_ROOT/hooks/stop" "$older_hook"
  printf '{"version":"%s"}\n' "$plugin_version" >"$older_fixture/.claude-plugin/plugin.json"
  cat >"$older_atct" <<'SCRIPT'
#!/usr/bin/env bash
if [[ "${1:-}" == version ]]; then
  printf '0.62.0\n'
fi
SCRIPT
  chmod +x "$older_atct"
  output="$(PATH="$(dirname "$older_atct"):/usr/bin:/bin" /bin/bash "$older_hook" <<< "$input")"
  assert_eq '{"decision":"block","reason":"ATCT: upgrade the CLI with: brew upgrade --cask michiomochi/tap/atct"}' "$output" 'older Claude Stop hook must block with Homebrew upgrade instruction'
}

test_claude_hooks_json_keeps_session_start_and_pre_tool_use_sections() {
  if python3 - "$REPO_ROOT/hooks/claude-hooks.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    hooks = json.load(stream)["hooks"]

missing = {"SessionStart", "PreToolUse"} - hooks.keys()
if missing:
    raise SystemExit(f"missing hook sections: {sorted(missing)}")
PY
  then
    return
  fi
  fail 'claude-hooks.json must keep SessionStart and PreToolUse'
}

# Writes an atct stub. It logs its arguments and stdin to $ATCT_HOOK_LOG,
# answers `version` with $ATCT_TEST_VERSION, and answers merge-check by
# $ATCT_MERGE_MODE: fail (exit 1, no output), json (print $ATCT_MERGE_JSON), or
# anything else (exit 0, no output).
write_merge_stub_atct() {
  local path="$1"
  mkdir -p "$(dirname "$path")"
  cat >"$path" <<'SCRIPT'
#!/usr/bin/env bash
if [[ "${1:-}" == version ]]; then
  printf '%s\n' "$ATCT_TEST_VERSION"
  exit 0
fi
input="$(cat)"
printf '%s\n%s\n' "$*" "$input" >>"$ATCT_HOOK_LOG"
if [[ "${1:-}" == merge-check ]]; then
  case "$ATCT_MERGE_MODE" in
    fail) exit 1 ;;
    json) printf '%s' "$ATCT_MERGE_JSON"; exit 0 ;;
    *) exit 0 ;;
  esac
fi
exit 0
SCRIPT
  chmod +x "$path"
}

# One line per Codex hook handler: event, matcher, command, separated by the
# unit separator. Tab would collapse an empty matcher under bash's IFS.
codex_hook_rows() {
  python3 - "$REPO_ROOT/hooks/codex-hooks.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    hooks = json.load(stream)["hooks"]
for event, groups in hooks.items():
    for group in groups:
        for handler in group["hooks"]:
            print("\x1f".join([event, group.get("matcher", ""), handler["command"]]))
PY
}

codex_merge_check_command() {
  codex_hook_rows | python3 -c 'import sys
for line in sys.stdin:
    event, matcher, command = line.rstrip("\n").split("\x1f", 2)
    if event == "PreToolUse" and matcher == "Bash":
        print(command)'
}

MERGE_DENY_JSON='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"ATCT merge-check failed"}}'
MERGE_INPUT='{"tool_name":"Bash","cwd":"/tmp","tool_input":{"command":"git merge wt/goal-999"}}'

test_codex_hook_commands_are_shell_commands() {
  local event matcher command count=0
  while IFS=$'\x1f' read -r event matcher command; do
    count=$((count + 1))
    [[ "$command" != \"* ]] || fail "Codex $event hook command is one quoted word, not a shell command"
    printf '%s\n' "$command" | sh -n || fail "Codex $event hook command does not parse as sh"
    assert_eq 1 "$(printf '%s' "$command" | grep -o 'required=' | wc -l | tr -d ' ')" \
      "Codex $event hook command must carry exactly one version marker"
  done < <(codex_hook_rows)
  [[ "$count" -gt 0 ]] || fail 'codex-hooks.json must declare hook handlers'
}

test_codex_hook_commands_dispatch_to_their_subcommand() {
  local fixture="$TEMP_ROOT/codex-dispatch" log="$TEMP_ROOT/codex-dispatch.log"
  local plugin_version event matcher command want status input
  plugin_version="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.codex-plugin/plugin.json")"
  write_merge_stub_atct "$fixture/atct"
  input='{"tool_name":"Bash","cwd":"/tmp","tool_input":{"command":"git status"},"session_id":"hook-session-1","stop_hook_active":false}'
  while IFS=$'\x1f' read -r event matcher command; do
    case "$event:$matcher" in
      SessionStart:) want='session-key --hook-input' ;;
      Stop:) want='stop-check --hook-input' ;;
      PreToolUse:) want='monitor-check --hook-input' ;;
      PreToolUse:Bash) want='merge-check --hook-input' ;;
      *) fail "unexpected Codex hook <$event> matcher <$matcher>" ;;
    esac
    : >"$log"
    status=0
    PATH="$fixture:/usr/bin:/bin" ATCT_TEST_VERSION="$plugin_version" ATCT_MERGE_MODE=allow \
      ATCT_HOOK_LOG="$log" sh -c "$command" <<< "$input" >/dev/null 2>&1 || status=$?
    assert_eq 0 "$status" "Codex $event:$matcher hook command must exit 0"
    assert_eq "$want"$'\n'"$input" "$(<"$log")" "Codex $event:$matcher hook command must call atct $want with stdin"
  done < <(codex_hook_rows)
}

test_codex_merge_check_fails_closed_to_deny() {
  local fixture="$TEMP_ROOT/codex-merge-fail" log="$TEMP_ROOT/codex-merge-fail.log"
  local plugin_version command output status=0
  plugin_version="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.codex-plugin/plugin.json")"
  command="$(codex_merge_check_command)"
  [[ -n "$command" ]] || fail 'codex-hooks.json must declare a PreToolUse Bash handler'
  write_merge_stub_atct "$fixture/atct"
  output="$(PATH="$fixture:/usr/bin:/bin" ATCT_TEST_VERSION="$plugin_version" ATCT_MERGE_MODE=fail \
    ATCT_HOOK_LOG="$log" sh -c "$command" <<< "$MERGE_INPUT")" || status=$?
  assert_eq 0 "$status" 'Codex merge-check must exit 0 when atct merge-check fails'
  assert_eq "$MERGE_DENY_JSON" "$output" 'Codex merge-check must deny when atct merge-check fails'
}

test_codex_merge_check_passes_through_atct_output() {
  local fixture="$TEMP_ROOT/codex-merge-pass" log="$TEMP_ROOT/codex-merge-pass.log"
  local plugin_version command output refusal
  plugin_version="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.codex-plugin/plugin.json")"
  command="$(codex_merge_check_command)"
  write_merge_stub_atct "$fixture/atct"
  refusal='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"refused by atct"}}'
  output="$(PATH="$fixture:/usr/bin:/bin" ATCT_TEST_VERSION="$plugin_version" ATCT_MERGE_MODE=json \
    ATCT_MERGE_JSON="$refusal" ATCT_HOOK_LOG="$log" sh -c "$command" <<< "$MERGE_INPUT")"
  assert_eq "$refusal" "$output" 'Codex merge-check must print atct output unchanged'
  output="$(PATH="$fixture:/usr/bin:/bin" ATCT_TEST_VERSION="$plugin_version" ATCT_MERGE_MODE=allow \
    ATCT_HOOK_LOG="$log" sh -c "$command" <<< "$MERGE_INPUT")"
  assert_eq '' "$output" 'Codex merge-check must be silent when atct allows the merge'
}

test_claude_hook_commands_resolve_to_executable_scripts() {
  local script count=0
  while IFS= read -r script; do
    count=$((count + 1))
    [[ -f "$script" ]] || fail "Claude hook script is missing: $script"
    [[ -x "$script" ]] || fail "Claude hook script is not executable: $script"
  done < <(python3 - "$REPO_ROOT" "$REPO_ROOT/hooks/claude-hooks.json" <<'PY'
import json
import shlex
import sys

repo, path = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as stream:
    hooks = json.load(stream)["hooks"]
for groups in hooks.values():
    for group in groups:
        for handler in group["hooks"]:
            command = handler["command"].replace("${CLAUDE_PLUGIN_ROOT}", repo)
            print(shlex.split(command)[0])
PY
)
  [[ "$count" -gt 0 ]] || fail 'claude-hooks.json must declare hook scripts'
}

test_claude_merge_check_fails_closed_to_deny() {
  local fixture="$TEMP_ROOT/claude-merge" log="$TEMP_ROOT/claude-merge.log" output status
  write_merge_stub_atct "$fixture/atct"

  : >"$log"
  status=0
  output="$(PATH="$fixture:/usr/bin:/bin" ATCT_MERGE_MODE=fail ATCT_HOOK_LOG="$log" \
    /bin/bash "$REPO_ROOT/hooks/merge-check" <<< "$MERGE_INPUT")" || status=$?
  assert_eq 0 "$status" 'Claude merge-check must exit 0 when atct merge-check fails'
  assert_eq "$MERGE_DENY_JSON" "$output" 'Claude merge-check must deny when atct merge-check fails'
  assert_eq $'merge-check --hook-input\n'"$MERGE_INPUT" "$(<"$log")" 'Claude merge-check must pass raw hook input to atct merge-check'

  refusal='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"refused by atct"}}'
  output="$(PATH="$fixture:/usr/bin:/bin" ATCT_MERGE_MODE=json ATCT_MERGE_JSON="$refusal" ATCT_HOOK_LOG="$log" \
    /bin/bash "$REPO_ROOT/hooks/merge-check" <<< "$MERGE_INPUT")"
  assert_eq "$refusal" "$output" 'Claude merge-check must print atct output unchanged'

  output="$(PATH="$fixture:/usr/bin:/bin" ATCT_MERGE_MODE=allow ATCT_HOOK_LOG="$log" \
    /bin/bash "$REPO_ROOT/hooks/merge-check" <<< "$MERGE_INPUT")"
  assert_eq '' "$output" 'Claude merge-check must be silent when atct allows the merge'
}

test_claude_monitor_check_fails_open() {
  local fixture="$TEMP_ROOT/claude-monitor" output status
  local monitor_input='{"session_id":"s1","tool_name":"Monitor","tool_input":{"command":"atct watch --monitor --token t1"}}'
  mkdir -p "$fixture"
  cat >"$fixture/atct" <<'SCRIPT'
#!/usr/bin/env bash
if [[ "${1:-}" == monitor-check ]]; then
  cat >/dev/null
  if [[ "${ATCT_MONITOR_MODE:-}" == fail ]]; then exit 1; fi
  printf '%s' "$ATCT_MONITOR_JSON"
fi
exit 0
SCRIPT
  chmod +x "$fixture/atct"

  # With no atct on PATH the Monitor is allowed: this gate never refuses what it cannot judge.
  status=0
  output="$(PATH="/usr/bin:/bin" /bin/bash "$REPO_ROOT/hooks/pre-monitor" <<< "$monitor_input")" || status=$?
  assert_eq 0 "$status" 'Claude pre-monitor must exit 0 when atct is missing'
  assert_eq '' "$output" 'Claude pre-monitor must be silent when atct is missing'

  # An atct whose monitor-check fails must not refuse the Monitor either.
  status=0
  output="$(PATH="$fixture:/usr/bin:/bin" ATCT_MONITOR_MODE=fail \
    /bin/bash "$REPO_ROOT/hooks/pre-monitor" <<< "$monitor_input")" || status=$?
  assert_eq 0 "$status" 'Claude pre-monitor must exit 0 when atct monitor-check fails'
  assert_eq '' "$output" 'Claude pre-monitor must be silent when atct monitor-check fails'

  # A refusal from atct is passed through unchanged.
  output="$(PATH="$fixture:/usr/bin:/bin" ATCT_MONITOR_JSON="$MERGE_DENY_JSON" \
    /bin/bash "$REPO_ROOT/hooks/pre-monitor" <<< "$monitor_input")"
  assert_eq "$MERGE_DENY_JSON" "$output" 'Claude pre-monitor must print atct monitor-check output unchanged'

  # claude-hooks.json routes the Monitor tool to this script.
  python3 - "$REPO_ROOT/hooks/claude-hooks.json" <<'TEST_PY' || fail 'claude-hooks.json must route the Monitor matcher to hooks/pre-monitor'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    groups = json.load(stream)["hooks"]["PreToolUse"]
matches = [g for g in groups if g.get("matcher") == "Monitor"]
if len(matches) != 1 or not any("hooks/pre-monitor" in h["command"] for h in matches[0]["hooks"]):
    raise SystemExit(1)
TEST_PY
}

test_real_atct_binary_denies_unapproved_goal_merge() {
  local bin_dir="$TEMP_ROOT/real-bin" home="$TEMP_ROOT/real-home" repo="$TEMP_ROOT/real-repo"
  local codex_command merge_input main_input output plugin_version
  # The hook's version floor moves with every release bump, so build the binary at the plugin's version.
  plugin_version="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.codex-plugin/plugin.json")"
  mkdir -p "$bin_dir" "$home" "$repo"
  (cd "$REPO_ROOT" && go build -ldflags "-X main.version=$plugin_version" -o "$bin_dir/atct" ./cmd/atct) \
    || fail 'atct must build for the merge-check hook test'
  git -C "$repo" init -q -b main || fail 'temporary repository must start on main'
  codex_command="$(codex_merge_check_command)"
  merge_input="{\"tool_name\":\"Bash\",\"cwd\":\"$repo\",\"tool_input\":{\"command\":\"git merge wt/goal-999\"}}"
  main_input="{\"tool_name\":\"Bash\",\"cwd\":\"$repo\",\"tool_input\":{\"command\":\"git merge main\"}}"

  output="$(HOME="$home" PATH="$bin_dir:/usr/bin:/bin" sh -c "$codex_command" <<< "$merge_input")"
  case "$output" in
    *'"permissionDecision":"deny"'*'has not been approved'*) ;;
    *) fail "Codex merge-check must deny an unapproved goal merge, got <$output>" ;;
  esac
  output="$(HOME="$home" PATH="$bin_dir:/usr/bin:/bin" /bin/bash "$REPO_ROOT/hooks/merge-check" <<< "$merge_input")"
  case "$output" in
    *'"permissionDecision":"deny"'*'has not been approved'*) ;;
    *) fail "Claude merge-check must deny an unapproved goal merge, got <$output>" ;;
  esac

  output="$(HOME="$home" PATH="$bin_dir:/usr/bin:/bin" sh -c "$codex_command" <<< "$main_input")"
  assert_eq '' "$output" 'Codex merge-check must be silent for a merge of main'
  output="$(HOME="$home" PATH="$bin_dir:/usr/bin:/bin" /bin/bash "$REPO_ROOT/hooks/merge-check" <<< "$main_input")"
  assert_eq '' "$output" 'Claude merge-check must be silent for a merge of main'
}

test_stop_hook_file_is_executable_but_other_hooks_remain() {
  [[ -x "$REPO_ROOT/hooks/stop" ]] || fail 'hooks/stop must be executable'
  [[ -f "$REPO_ROOT/hooks/pre-ask" ]] || fail 'hooks/pre-ask must remain'
  [[ -f "$REPO_ROOT/hooks/session-start" ]] || fail 'hooks/session-start must remain'
}

test_mcp_instructions_include_active_goal_permission() {
  assert_file_contains 'An active goal is permission to coordinate work.' "$REPO_ROOT/internal/mcpshim/instructions.go"
}

test_mcp_instructions_delegate_human_decisions_to_the_skill() {
  assert_file_contains 'For the human-decision rule, see the `atct` skill.' "$REPO_ROOT/internal/mcpshim/instructions.go"
}

test_session_start_is_silent_without_atct_wrapper() {
  local fixture="$TEMP_ROOT/session-start-no-wrapper"
  local hook="$fixture/hooks/session-start"
  local output

  mkdir -p "$(dirname "$hook")"
  cp "$REPO_ROOT/hooks/session-start" "$hook"

  output="$(PATH="" /bin/bash "$hook" 2>&1)" || fail 'hook failed without an atct wrapper'
  assert_eq '' "$output" 'missing atct wrapper must keep the hook silent'
}

test_session_start_hook_uses_shared_session_key_message() {
  local plugin_version
  plugin_version="$(awk -F '"' '/"version"[[:space:]]*:/ { print $4; exit }' "$REPO_ROOT/.claude-plugin/plugin.json")"
  local fixture="$TEMP_ROOT/session-start-shared-message"
  local hook="$fixture/hooks/session-start"
  local fake="$fixture/bin/atct"
  local log="$fixture/atct.log"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$fake")" "$fixture/.claude-plugin"
  cp "$REPO_ROOT/hooks/session-start" "$hook"
  printf '{"version":"%s"}\n' "$plugin_version" >"$fixture/.claude-plugin/plugin.json"
  cat >"$fake" <<'SCRIPT'
#!/usr/bin/env bash
if [[ "${1:-}" == version ]]; then
  printf 'version\n' >>"$ATCT_SESSION_START_LOG"
  printf '%s\n' "$ATCT_TEST_VERSION"
  exit 0
fi
if [[ "$*" == 'context -brief' ]]; then
  printf 'context -brief\n' >>"$ATCT_SESSION_START_LOG"
  exit 0
fi
if [[ "$*" == 'session-key --hook-input' ]]; then
  input="$(cat)"
  printf '%s\n%s' "$*" "$input" >>"$ATCT_SESSION_START_LOG"
  printf '%s' 'ATCT session key: hook-session-1. Before any ATCT work, run `atct execution-flow` and follow its procedure.'
  exit 0
fi
exit 1
SCRIPT
  chmod +x "$hook" "$fake"
  export ATCT_TEST_VERSION="$plugin_version"

  output="$(PATH="$(dirname "$fake"):$PATH" ATCT_SESSION_START_LOG="$log" /bin/bash "$hook" <<< '{"session_id":"hook-session-1"}')"
  assert_eq 'ATCT session key: hook-session-1. Before any ATCT work, run `atct execution-flow` and follow its procedure.' "$output" 'Claude SessionStart hook must use the shared session-key output'
  assert_eq $'version\ncontext -brief\nsession-key --hook-input\n{"session_id":"hook-session-1"}' "$(<"$log")" 'Claude SessionStart hook must pass raw input through the shared CLI'
}

delegate_goal_section() {
  sed -n '/^## Delegate a goal$/,$p' \
    "$REPO_ROOT/skills/commander/SKILL.md"
}

delegate_goal_section_contains() {
  local needle="$1"
  local section
  section="$(delegate_goal_section)"
  grep -Fq -- "$needle" <<<"$section" ||
    fail "goal delegation section does not contain <$needle>"
}

delegate_goal_section_not_contains() {
  local needle="$1"
  local section
  section="$(delegate_goal_section)"
  if grep -Fq -- "$needle" <<<"$section"; then
    fail "goal delegation section must not contain <$needle>"
  fi
}

test_goal_handoff_watch_contract_is_explicit() {
  delegate_goal_section_contains 'Then, in Claude Code only, attach `atct watch --monitor --token'
  delegate_goal_section_contains 'Use the exact token'
  delegate_goal_section_contains 'The server-derived assignment limits this Watch to the received goal.'
}

test_goal_handoff_watch_contract_omits_unsafe_variants() {
	delegate_goal_section_not_contains 'Then attach `atct watch` to a background stream'
	delegate_goal_section_not_contains 'atct watch -goal'
	delegate_goal_section_not_contains 'atct watch -project'
	delegate_goal_section_not_contains 'attach `atct watch` for the whole project'
  delegate_goal_section_not_contains 'Invoke the `start` skill'
  delegate_goal_section_not_contains 'The delegator relays the detections for this goal'
}

test_goal_handoff_watch_contract_has_required_order() {
  local lineno
  local role
  local watch
  local fin

  lineno() { delegate_goal_section | grep -n -F -- "$1" | head -1 | cut -d: -f1; }
  role="$(lineno 'Then invoke the `atct_role` MCP tool with `expected_role` set to')"
	watch="$(lineno 'Then, in Claude Code only, attach `atct watch --monitor --token')"
  fin="$(lineno 'When the work is done and verified, record the goal review request by calling')"

  [[ -n "$role" && -n "$watch" && -n "$fin" ]] ||
    fail 'goal handoff watch order requires role, watch, and review-request paragraphs'
  (( role < watch && watch < fin )) ||
    fail "goal handoff watch paragraphs are in the wrong order: role=$role watch=$watch review_request=$fin"
}

test_goal_handoff_forbids_upward_design_questions() {
  delegate_goal_section_contains "Decide this goal's design yourself. Do not bring the delegator a design"
  delegate_goal_section_contains "reading of this goal's code. Send the delegator nothing until the completion"
}

test_goal_handoff_names_the_single_upward_message() {
  delegate_goal_section_contains 'into the record instead: `surprises` and'
  delegate_goal_section_contains '`needs_review` for what you found, and `next_goal_ids` for the goals that'

  local section
  section="$(unsent_report_section)"
  grep -Fq -- '| the goal is ready for commander review | `atct_goal_handoff_review_request`, the one message |' <<<"$section" ||
    fail 'unsent report section must name the single upward review message'
}

test_goal_handoff_forbids_the_executor_decision_ask() {
  delegate_goal_section_contains 'should follow. You cannot call `atct_decision_ask`: the commander files a'
  delegate_goal_section_contains 'decision for the human, not you. For anything that needs the human, append a'
}

test_goal_handoff_routes_decisions_through_a_request_entry() {
  delegate_goal_section_contains '`review_requested` entry to your goal handoff with'
  delegate_goal_section_contains '`atct_goal_handoff_entry_append`, in the format `atct:atct` gives under'
  delegate_goal_section_contains "\`### An executor's decision request\`."
}

test_goal_handoff_routes_cross_goal_facts_through_the_request_entry() {
  delegate_goal_section_contains 'A fact that spans another goal is not an exception. Raise it the same way;'
  delegate_goal_section_contains 'the commander files the decision and replies on the same handoff with a'
  delegate_goal_section_contains '`review_received` entry, which your own watch delivers as'
  delegate_goal_section_contains '`handoff_entry_added`. Read it with `atct_goal_handoff_entry_history`.'
}

test_goal_handoff_silence_has_required_order() {
  local lineno
  local watch
  local silence
  local fin

  lineno() { delegate_goal_section | grep -n -F -- "$1" | head -1 | cut -d: -f1; }
	watch="$(lineno 'Then, in Claude Code only, attach `atct watch --monitor --token')"
  silence="$(lineno "Decide this goal's design yourself. Do not bring the delegator a design")"
  fin="$(lineno 'When the work is done and verified, record the goal review request by calling')"

  [[ -n "$watch" && -n "$silence" && -n "$fin" ]] ||
    fail 'goal handoff silence order requires watch, silence, and review-request paragraphs'
  (( watch < silence && silence < fin )) ||
    fail "goal handoff silence paragraphs are in the wrong order: watch=$watch silence=$silence review_request=$fin"
}

test_goal_handoff_preamble_does_not_invite_upward_reports() {
  delegate_goal_section_not_contains 'report progress to the delegator'
  delegate_goal_section_not_contains 'keep the delegator informed'
  delegate_goal_section_not_contains 'Report to the delegator when'
  delegate_goal_section_not_contains 'share your design with the delegator'
}

test_goal_delegation_requires_the_adjacent_goal_boundary() {
  delegate_goal_section_contains 'Name in the request every adjacent goal that touches the same files and say'
  delegate_goal_section_contains 'goals, and a boundary left unstated becomes a question the executor'
}

test_goal_delegation_keeps_the_delegator_out_until_completion() {
  delegate_goal_section_contains '6. Stay out until the completion report. After waking the executor, the'
  delegate_goal_section_contains 'arrive from `atct watch` rather than from the executor: a goal whose tasks'
  delegate_goal_section_contains 'creates no tasks raises none of them. For such a goal, check its branch'
  delegate_goal_section_contains '`atct_goal_handoff_review_request` lands; that report is the entry point.'
}

test_delegator_answers_are_balanced() {
  local section
  local delegator_heading
  local executor_heading
  local delegator_count
  local executor_count

  section="$(delegator_answers_section)"
  delegator_heading="$(grep -n -F -- 'Four kinds of question belong to the delegator' <<<"$section" | head -1 | cut -d: -f1)"
  executor_heading="$(grep -n -F -- 'Four kinds look similar and belong to the executor' <<<"$section" | head -1 | cut -d: -f1)"

  [[ -n "$delegator_heading" && -n "$executor_heading" ]] ||
    fail 'delegator answer balance requires both question headings'

  delegator_count="$(awk -v start="$delegator_heading" -v end="$executor_heading" '
    NR > start && NR < end && /^- / { count++ }
    END { print count + 0 }
  ' <<<"$section")"
  executor_count="$(awk -v start="$executor_heading" '
    NR > start && /^- / { count++ }
    END { print count + 0 }
  ' <<<"$section")"

  assert_eq '4' "$delegator_count" 'delegator answers must list four question kinds'
  assert_eq '4' "$executor_count" 'executor answers must list four question kinds'
  assert_eq "$delegator_count" "$executor_count" 'delegator and executor question counts must match'
}

test_delegator_answers_names_the_wrong_answers_measurement() {
  local section
  section="$(delegator_answers_section)"

  grep -Fq -- 'On 2026-08-27 a commander answered two such' <<<"$section" ||
    fail 'delegator answers section must name the wrong-answers measurement'
  grep -Fq -- 'tool was reachable from MCP, and that `wakeup.go` read a file it does not read.' <<<"$section" ||
    fail 'delegator answers section must name both wrong answers'
}

test_unsent_report_table_covers_every_spoken_kind() {
  local section
  local table_rows
  section="$(unsent_report_section)"

  for needle in \
    '| receipt of the goal |' \
    '| progress on the work |' \
    '| the design and why |' \
    '| something found inside this goal |' \
    '| something found that is another goal |' \
    '| what was left undone |' \
    '| the goal is ready for commander review |'; do
    grep -Fq -- "$needle" <<<"$section" ||
      fail "unsent report table does not cover <$needle>"
  done

  table_rows="$(awk '
    /^\| What used to be spoken \| Where it goes \|$/ { in_table=1; next }
    in_table && /^\| / && /\|$/ { count++ }
    END { print count + 0 }
  ' <<<"$section")"
  assert_eq '7' "$table_rows" 'unsent report table must contain seven data rows'
}

test_unsent_report_names_the_stall_detection() {
  local section
  section="$(unsent_report_section)"

  grep -Fq -- "committed, each raises a Wakeup on the delegator's watch. On 2026-08-27 goal" <<<"$section" ||
    fail 'unsent report section must name the stall detection'
  grep -Fq -- '172 stalled with three tasks still `todo` and eight files uncommitted, and goal' <<<"$section" ||
    fail 'unsent report section must name goal 172'
  grep -Fq -- 'Wakeups had already fired; nobody had been told to read them.' <<<"$section" ||
    fail 'unsent report section must name the missed detection read'
}

delegator_answers_section() {
  sed -n '/^## What the delegator answers$/,/^## Where an unsent report goes$/p' \
    "$REPO_ROOT/skills/atct/SKILL.md"
}

unsent_report_section() {
  sed -n '/^## Where an unsent report goes$/,/^## Fill in a report on a handoff that is already closed$/p' \
    "$REPO_ROOT/skills/atct/SKILL.md"
}

recovery_section() {
  sed -n '/^## Recover when your role comes back wrong$/,/^## Completion$/p' \
    "$REPO_ROOT/skills/atct/SKILL.md"
}

recovery_section_contains() {
  local needle="$1"
  local section
  section="$(recovery_section)"
  grep -Fq -- "$needle" <<<"$section" ||
    fail "recovery section does not contain <$needle>"
}

recovery_section_not_contains() {
  local needle="$1"
  local section
  section="$(recovery_section)"
  if grep -Fq -- "$needle" <<<"$section"; then
    fail "recovery section must not contain <$needle>"
  fi
}

test_recovery_section_has_role_entry() {
  recovery_section_contains 'If `atct_role` returns `none` while you still hold work that should be yours, stop working and read this section.'
}

test_recovery_section_prioritizes_session_identify() {
  recovery_section_contains 'The first recovery path is `atct_session_identify`; follow `### Session keys` first.'
}

test_recovery_section_has_project_path() {
  recovery_section_contains '- project: `atct_project_release` → `atct_project_claim`'
}

test_recovery_section_has_goal_path() {
  recovery_section_contains '- goal: `atct_goal_handoff_complete` → `atct_goal_handoff_request` (legacy/out-of-order recovery; the commander must issue the handoff again)'
  recovery_section_contains 'the executor cannot reissue it, and the goal waits on the'
}

test_recovery_section_has_task_path_and_non_repair_note() {
  recovery_section_contains 'have the session that sent it request a fresh `atct_task_handoff_request`'
  recovery_section_contains 'Rejection is automatic, so the goal step above that asks the commander to reissue the handoff is needed only for the last trigger.'
}

test_handoff_completion_reports_are_explicit() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  local executor_skill="$REPO_ROOT/skills/executor/SKILL.md"
  # The executor's review-report contract lives in the executor skill: the goal
  # review request report names the commands run, the checks it could not run
  # and the changed paths, and the task_worker section keeps the old contract.
  assert_file_contains 'with the `goal_id` provided in this request' "$commander_skill"
  assert_file_contains 'every command you ran and its result, the checks you could not' "$executor_skill"
  assert_file_contains 'run (say "could not run"), and the changed paths.' "$executor_skill"
  assert_file_contains 'Submit a review report stating the work, verification, unavailable checks, and' "$executor_skill"
  assert_file_contains 'changed paths with `atct_task_handoff_review_request`.' "$executor_skill"
}

test_handoff_report_repair_is_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  assert_file_contains '## Fill in a report on a handoff that is already closed' "$atct_skill"
  assert_file_contains '`atct_task_handoff_report_amend` with the specific `handoff_id`' "$atct_skill"
  assert_file_contains 'It is not part of normal executor completion.' "$atct_skill"
}

# The two checks below measure the lists themselves, not the surrounding
# section. Searching the whole section is what let the lists rot: every allowed
# name and some forbidden ones also appear in the section's prose and quoted
# blocks, so deleting a name from its list still found it elsewhere and passed.
# Do not widen these back to `delegate_task_section`.
# Names are matched with their backticks so a name can never match inside a
# longer one: `atct_task_handoff_complete` would otherwise hit
# `atct_goal_handoff_complete`, which sits on the opposite list.
test_executor_skill_states_the_blocked_output() {
  # goal 375: the blocked output is stated once, in the executor skill.
  assert_file_contains 'blocked: no live Monitor' "$REPO_ROOT/skills/executor/SKILL.md"
}

test_executor_skill_sets_up_the_watch_between_receive_and_role() {
  local skill receive_line watch_line role_line

  skill="$REPO_ROOT/skills/executor/SKILL.md"
  receive_line="$(grep -nF 'atct_goal_handoff_receive`' "$skill" | head -1 | cut -d: -f1 || true)"
  watch_line="$(grep -nF '## Watch' "$skill" | head -1 | cut -d: -f1 || true)"
  role_line="$(grep -nF 'atct_role` with `expected_role' "$skill" | head -1 | cut -d: -f1 || true)"

  [[ -n "$receive_line" && -n "$watch_line" && -n "$role_line" ]] ||
    fail "executor entry must name receive, watch, and role check: receive=$receive_line watch=$watch_line role=$role_line"
  (( receive_line < watch_line && watch_line < role_line )) ||
    fail "executor watch step must sit between receive and role check: receive=$receive_line watch=$watch_line role=$role_line"
  assert_file_not_contains 'persistent' "$skill"
  assert_file_contains '`atct_task_handoff_review_reject_receive`' "$skill"
}

test_recovery_section_explains_why_the_role_drops() {
  recovery_section_contains "Closing an executor's goal handoff drops that executor to \`none\`"
}

test_orchestration_skill_has_no_blanket_atct_ban() {
  # The orchestration skill lives in the dotfiles repository, which this one
  # cannot change, so this check has two states: the file is absent and there is
  # nothing to inspect, or it is present and every assertion runs.
  #
  # The path is overridable so a mutation test can point at a throwaway copy.
  # The real file under ~/.claude is read by every agent, so it must not be
  # damaged just to prove this check fails when it should.
  local orchestration="${ORCHESTRATION_SKILL_PATH:-$HOME/.claude/skills/orchestration/SKILL.md}"

  # No dotfiles checkout, as in CI. A file belonging to another repository being
  # absent is not a failure of this one.
  if [[ ! -f "$orchestration" ]]; then
    printf 'skip: %s is absent (it belongs to dotfiles, a separate repository)\n' "$orchestration"
    return 0
  fi

  # This used to also require orchestration to carry the ATCT allowlist, the
  # prohibition list, and two call orderings. dotfiles a009868 removed all of
  # them on purpose: doc/specs/2026-09-13-orchestration-skill-boundary.md makes
  # `atct` the only place that states roles, handoffs, and tool order, and
  # orchestration only starts the workers. Asserting the list here would pull the
  # duplicate back, which is what that spec set out to remove.
  #
  # The blanket ban must still be gone: it is the regression that made every
  # ATCT tool unreachable from a delegated worker, and no other file forbids it.
  assert_file_not_contains '**ATCT ツールの呼び出し**' "$orchestration"
}

test_goal_handoff_completion_keeps_one_normal_path() {
  local goal_section
  local completion_step
  goal_section="$(sed -n '/^## Delegate a goal$/,/^### Session keys$/p' "$REPO_ROOT/skills/commander/SKILL.md")"
  completion_step="$(sed -n '/When the work is done and verified, record the goal review request by calling/,/^$/p' <<<"$goal_section")"
  ! grep -Fq -- 'atct_goal_handoff_report_amend' <<<"$goal_section" || fail 'normal goal completion must not name the repair tool'
  grep -Fq -- '`atct_goal_handoff_review_request`' <<<"$completion_step" || fail 'goal completion must start with a review request'
  grep -Fq -- 'the completion report while the handoff remains open' <<<"$goal_section" || fail 'goal review request must record the completion report while the handoff remains open'
}

test_handoff_report_repair_follows_goal_delegation() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local reporting
  local repair
  local recovery
  # Goal delegation left this skill in 6979a33, so the section the repair used
  # to follow is no longer here. What the order has to keep is the reading path:
  # send a report, then repair one that is already closed, then recovery.
  reporting="$(grep -n '^## Where an unsent report goes$' "$atct_skill" | cut -d: -f1)"
  repair="$(grep -n '^## Fill in a report on a handoff that is already closed$' "$atct_skill" | cut -d: -f1)"
  recovery="$(grep -n '^## Recover when your role comes back wrong$' "$atct_skill" | cut -d: -f1)"
  (( reporting < repair && repair < recovery )) || fail 'handoff report repair must follow the reporting section and precede recovery'
}

test_recovery_section_omits_session_header() {
  recovery_section_not_contains 'Mcp-Session-Id'
}

test_recovery_section_omits_agent_sessions() {
  recovery_section_not_contains 'agent_sessions'
}

test_recovery_section_omits_task_release() {
  recovery_section_not_contains 'atct_task_release'
}

test_recovery_section_omits_task_update() {
  recovery_section_not_contains 'atct_task_update'
}

test_recovery_section_omits_goal_release() {
  recovery_section_not_contains 'atct_goal_release'
}

test_recovery_section_names_existing_tools() {
  local section
  local tool_names
  local tool

  section="$(recovery_section)"
  tool_names="$(grep -oE 'atct_[a-z_]+' <<<"$section" | sort -u || true)"
  [[ -n "$tool_names" ]] || fail 'recovery section names no tools'

  for tool in $tool_names; do
    grep -Eq "Name:[[:space:]]+\"$tool\"" \
      "$REPO_ROOT/internal/mcpshim/tools.go" ||
      fail "recovery section names a tool that does not exist: $tool"
  done
}

test_one_workspace_per_goal_section_exists() {
  # The shared skill only names where the section went; the rule itself, and so
  # this assertion, belongs to commander.
  assert_file_contains '## One workspace per goal' "$REPO_ROOT/skills/commander/SKILL.md"
}

test_one_workspace_per_goal_binds_a_workspace_to_one_goal() {
  assert_file_contains 'A workspace belongs to one goal' "$REPO_ROOT/skills/commander/SKILL.md"
}

test_one_workspace_per_goal_closes_on_approval() {
  assert_file_contains 'approving the review requested' "$REPO_ROOT/skills/commander/SKILL.md"
  assert_file_contains 'atct_goal_review_complete' "$REPO_ROOT/skills/commander/SKILL.md"
}

test_executor_uses_the_advisor_before_approach_and_review_request() {
  local skill="$REPO_ROOT/skills/executor/SKILL.md"
  assert_file_contains 'Use the advisor, when the advisor tool is available in your session' "$skill"
  assert_file_contains 'before you settle a design or implementation' "$skill"
  assert_file_contains 'again before you call `atct_goal_handoff_review_request`' "$skill"
  assert_file_contains 'what you asked the advisor and what you changed because of it' "$skill"
  assert_file_not_contains 'opus sub-agent' "$skill"
}

test_commander_keeps_the_advisor_enabled_and_the_independent_check() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  assert_file_contains 'Start the executor'"'"'s session with the advisor available' "$commander_skill"
  assert_file_contains 'do not disable the' "$commander_skill"
  assert_file_contains 'advisor tool that the user'"'"'s own settings provide' "$commander_skill"
  assert_file_contains 'it does not' "$commander_skill"
  assert_file_contains 'replace this independent check' "$commander_skill"
  assert_file_contains 'Start an Agent with `model` set to `opus`' "$commander_skill"
  # No model name or launch flag belongs in the skill.
  assert_file_not_contains 'CLAUDE_CODE_DISABLE_ADVISOR_TOOL' "$commander_skill"
  assert_file_not_contains 'advisorModel' "$commander_skill"
}

test_one_workspace_per_goal_forbids_reuse() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  assert_file_contains 'do not hand it a second goal' "$commander_skill"
  assert_file_contains 'A closed workspace is not reopened' "$commander_skill"
}

test_one_workspace_per_goal_names_the_only_exception() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  assert_file_contains "The \`commander\`'s own workspace is the exception, and there is no other." "$commander_skill"
  assert_file_contains 'A rejected completion is the same goal' "$commander_skill"
}

test_one_workspace_per_goal_sits_between_worktree_and_commit() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  local worktree
  local workspace
  local delegate
  # Both rules moved to commander in 6979a33; `## Commit safely` stayed in the
  # shared skill, so the trailing bound is the section that now follows them.
  worktree="$(grep -n '^## One worktree per goal$' "$commander_skill" | cut -d: -f1)"
  workspace="$(grep -n '^## One workspace per goal$' "$commander_skill" | cut -d: -f1)"
  delegate="$(grep -n '^## Delegate a goal$' "$commander_skill" | cut -d: -f1)"
  (( worktree < workspace && workspace < delegate )) ||
    fail 'one workspace per goal must follow the worktree rule and precede goal delegation'
}

test_delegated_claim_contract_is_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"
  # 6979a33 moved the role-only sections out of the shared skill. The contract is
  # still stated once; it is stated in the skill of the role that has to follow it.
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"

  assert_file_contains 'Hold the parent, not the goal.' "$commander_skill"
  assert_file_contains '## Delegate a goal' "$commander_skill"
  assert_file_contains 'Then record receipt of the goal handoff by calling' "$commander_skill"
  assert_file_contains 'The executor must receive its goal handoff before touching the work' "$atct_skill"
  assert_file_contains 'a received, uncompleted goal handoff' "$atct_skill"
  assert_file_contains '`atct_goal_handoff_receive` before starting it.' "$start_skill"
  assert_file_contains 'The loop coordinates delegated work.' "$start_skill"
  assert_file_contains 'The delegator must call `atct_goal_handoff_request`' "$commander_skill"
  assert_file_contains 'with the `goal_id` provided in this request and the exact `session_key` from SessionStart.' "$commander_skill"
  assert_file_contains '`handoff_id` and `monitor_token` are optional' "$commander_skill"
  assert_file_contains 'passing only the `goal_id` and `handoff_id`' "$commander_skill"
  assert_file_contains 'never pass `session_key` or `monitor_token`' "$commander_skill"
  assert_file_contains '`atct_goal_review_complete` with the `goal_id` provided in this request.' "$commander_skill"
  assert_file_not_contains '`atct_goal_review_complete` with the `goal_id` provided in this request and a `complete_report`' "$atct_skill"
  assert_file_contains 'Do this before starting work.' "$commander_skill"
  assert_file_contains 'An executor must not claim the project' "$commander_skill"
  assert_file_contains 'An executor must not call `atct_goal_release`' "$commander_skill"
  assert_file_contains "commander's job." "$commander_skill"
  assert_file_contains '`atct_task_handoff_review_request`' "$REPO_ROOT/skills/executor/SKILL.md"
  assert_file_not_contains 'Call `atct_task_claim` before working on a task.' "$atct_skill"
  assert_file_not_contains 'A delegated worker must not claim the task; the delegator owns the claim.' "$atct_skill"
  assert_file_not_contains 'Do not call `atct_task_claim` for a delegated task.' "$atct_skill"
  assert_file_not_contains '## Delegate a claimed task' "$atct_skill"
  assert_file_not_contains '## Delegate a claimed goal' "$atct_skill"
  assert_file_not_contains 'task handoff task is unclaimed' "$REPO_ROOT/internal/store/task_handoff.go"
  assert_file_not_contains 'goal handoff goal is unclaimed' "$REPO_ROOT/internal/store/goal_handoff.go"
  assert_file_not_contains 'A two-layer delegation keeps the commander role while the commander delegates the goal tasks.' "$atct_skill"
  assert_file_not_contains '1. Claim the task before handing it off.' "$atct_skill"
  assert_file_not_contains 'Claim the goal with `atct_goal_claim` before handing it off.' "$atct_skill"
  assert_file_not_contains 'First invoke the `atct_role` MCP tool with `expected_role` set to one of' "$atct_skill"
  assert_file_not_contains 'The daemon derives the role from claims:' "$atct_skill"
  assert_file_not_contains '`subcommander`: the agent holds a goal claim but no project claim.' "$atct_skill"
  assert_file_not_contains 'A delegated worker does not claim the delegated task or follow the claim step;' "$start_skill"
  assert_file_not_contains '4. **Take one.** Call `atct_task_claim`.' "$start_skill"
  assert_file_not_contains '2. Wake the worker through the environment.' "$atct_skill"
  assert_file_not_contains 'The worker must run this check itself before doing any work.' "$atct_skill"
  assert_file_not_contains "the delegator's identity" "$atct_skill"
  assert_file_not_contains 'Do this whenever convenient.' "$atct_skill"
  assert_file_not_contains 'atct_goal_handoff_receive` with only the `goal_id` provided in this request.' "$atct_skill"
  assert_file_not_contains 'Do not pass a handoff ID or session; ATCT' "$atct_skill"
}

test_declared_task_content_fix_contract_is_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local task_section

  task_section="$(sed -n '/^## Fix a declared task$/,/^## Receive before you start$/p' "$atct_skill")"
  grep -Fq -- 'atct_task_update_content' <<<"$task_section" ||
    fail 'declared task content fix section omits atct_task_update_content'
  grep -Fq -- 'todo' <<<"$task_section" ||
    fail 'declared task content fix section omits todo'
  grep -Fq -- 'done' <<<"$task_section" ||
    fail 'declared task content fix section omits done'
  grep -Fq -- 'idempotency_key' <<<"$task_section" ||
    fail 'declared task content fix section omits idempotency_key'
}

test_start_identifies_before_monitor() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"
  local identify_line
  local monitor_line

  identify_line="$(grep -n '^## First step: identify' "$start_skill" | head -1 | cut -d: -f1)"
  monitor_line="$(grep -n '^## .*Claude Code.*Watch' "$start_skill" | head -1 | cut -d: -f1)"
  [[ -n "$identify_line" && -n "$monitor_line" ]] ||
    fail 'start order requires identify and monitor headings'
  (( identify_line < monitor_line )) ||
    fail "session identification must precede Monitor: identify=$identify_line monitor=$monitor_line"
}

test_start_session_key_contract_is_explicit() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'call `atct_session_identify`' "$start_skill"
  assert_file_contains '`session_key` and `monitor_token`' "$start_skill"
  assert_file_contains 'atct_goal_handoff_receive` with its `goal_id`,' "$start_skill"
  assert_file_contains 'using the exact `session_key` and optional `handoff_id` and `monitor_token` from' "$start_skill"
  assert_file_contains 'SessionStart → `atct_role`' "$start_skill"
  assert_file_not_contains 'handoff receipt with its `task_id` only' "$start_skill"
  assert_file_contains '<project>-<unit>-<role>' "$start_skill"
  assert_file_contains 'sessions into one row.' "$start_skill"
}

test_start_forces_commander_claim() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'atct_project_claim` with `force` set to `true`' "$start_skill"
  assert_file_contains 'atct_role` with `expected_role` set to `commander`' "$start_skill"
  assert_file_contains 'atct:commander' "$start_skill"
}

test_role_skills_are_routed_from_shared_atct() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local role

  for role in commander executor; do
    assert_file_contains "name: $role" "$REPO_ROOT/skills/$role/SKILL.md"
    assert_file_contains "expected_role\` set to \`$role\`" "$REPO_ROOT/skills/$role/SKILL.md"
    assert_file_contains 'atct:atct' "$REPO_ROOT/skills/$role/SKILL.md"
    assert_file_contains "atct:$role" "$atct_skill"
  done
  # atct:subcommander is only a pointer for delegation text written before the
  # roles merged; it must send the reader to atct:executor and carry no procedure.
  assert_file_contains 'name: subcommander' "$REPO_ROOT/skills/subcommander/SKILL.md"
  assert_file_contains 'Use `atct:executor`.' "$REPO_ROOT/skills/subcommander/SKILL.md"
  assert_file_not_contains 'atct_goal_handoff_receive' "$REPO_ROOT/skills/subcommander/SKILL.md"
}

test_role_entry_receives_with_credentials_before_role_check() {
  local skill receipt_line role_line

  skill="$REPO_ROOT/skills/executor/SKILL.md"
  assert_file_contains 'atct_goal_handoff_receive`' "$skill"
  assert_file_contains 'exact `session_key` from SessionStart' "$skill"
  assert_file_contains 'optional `handoff_id` and' "$skill"
  assert_file_contains '`monitor_token` may be included' "$skill"
  receipt_line="$(grep -nF 'atct_goal_handoff_receive`' "$skill" | head -1 | cut -d: -f1)"
  role_line="$(grep -nF 'atct_role` with `expected_role' "$skill" | head -1 | cut -d: -f1)"
  [[ -n "$receipt_line" && -n "$role_line" ]] || fail 'executor entry must name receipt and role check'
  (( receipt_line < role_line )) || fail 'executor must receive the goal handoff before checking its role'

  # The task_worker section keeps the task-handoff entry for a session that
  # received its handoff before the roles merged.
  assert_file_contains 'atct_task_handoff_receive`' "$skill"
  assert_file_contains '## task_worker: finishing a task handoff received before the two-layer change' "$skill"
  assert_file_contains '`expected_role` set to `task_worker`' "$skill"
  assert_file_contains '`task_id` and `handoff_id` provided in the handoff' "$skill"
}

test_atct_skill_requires_execution_flow() {
  assert_file_contains 'Before any ATCT work, run `atct execution-flow` and follow its procedure.' "$REPO_ROOT/skills/atct/SKILL.md"
}

# The tool takes cwd, but nothing fills it unless a skill says to. An optional
# field nobody passes is how ATCT_AGENT_SESSION_ID stayed dead for months.
test_identify_is_told_to_pass_its_cwd() {
  assert_file_contains 'Pass the current directory as `cwd` in the same call.' "$REPO_ROOT/skills/start/SKILL.md"
  assert_file_contains 'passing your current directory as `cwd`' "$REPO_ROOT/skills/commander/SKILL.md"
}

test_start_explains_claim_recovery_boundary() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'claim taken before the key was registered is not restored' "$start_skill"
}

test_start_explains_mcp_reconnect_gap() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'new version has just' "$start_skill"
  assert_file_contains 'MCP has not reconnected' "$start_skill"
  assert_file_contains 'recovery section of the `atct:atct` skill' "$start_skill"
}

test_start_monitor_is_not_first_step() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_not_contains '## First step: attach the Claude Code Monitor' "$start_skill"
}

test_start_does_not_branch_on_session_attachment() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_not_contains 'reattached' "$start_skill"
}

test_start_does_not_duplicate_delegated_worker_preamble() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_not_contains 'First call `atct_session_identify` with a stable session key' "$start_skill"
}

test_start_keeps_monitor_persistence_requirement() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'Always set `persistent: true`' "$start_skill"
  assert_file_contains 'ATCT answer watch' "$start_skill"
  assert_file_contains '`## Watch`' "$start_skill"
}

test_atct_skill_defines_the_background_watch() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains '## Watch' "$atct_skill"
  assert_file_contains '--once' "$atct_skill"
  assert_file_contains 'run_in_background' "$atct_skill"
  assert_file_contains 'Monitor' "$atct_skill"
  assert_file_contains 'events delivered' "$atct_skill"
}

test_start_documents_liveness_authority_boundary() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'A one-minute liveness line means' "$start_skill"
  assert_file_contains 'An executor has its plan accepted first' "$start_skill"
  assert_file_contains 'executor process through `atct codex monitor -- <codex args>`' "$start_skill"
  assert_file_contains 'requests the goal review; it does not cross the' "$start_skill"
}

test_start_uses_monitor_watch_for_claude_actions() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'atct watch --monitor --token <monitor_token>' "$start_skill"
  assert_file_not_contains 'atct watch --monitor -goal' "$start_skill"
  assert_file_not_contains 'atct watch --monitor -project' "$start_skill"
  assert_file_contains 'Plain `atct watch` is for human diagnostics' "$start_skill"
  assert_file_contains 'Reconnect, keepalive, and ensure diagnostics are never agent actions' "$start_skill"
}

test_readme_documents_liveness_contract() {
  local readme="$REPO_ROOT/README.md"

  assert_file_contains '### Monitor liveness and recovery' "$readme"
  assert_file_contains 'independent one-minute' "$readme"
  assert_file_contains 'bounded `atct monitor liveness:` recheck line' "$readme"
  assert_file_contains 'A liveness line is a recheck signal, not authority' "$readme"
}

test_goal_completion_is_commander_owned() {
  local section
  local flow
  section="$(delegate_goal_section)"

  flow="$(sed -n '/The goal completion order is/,/^$/p' <<<"$section" | tr '\n' ' ')"
  [[ -n "$flow" ]] || fail 'goal delegation section omits the goal completion order'
  [[ "$flow" == *'atct_goal_handoff_review_request`'*'atct_goal_handoff_review_receive`'* ]] ||
    fail 'goal handoff review request must precede commander review receive'
  [[ "$flow" == *'atct_goal_handoff_review_receive`'*'atct_goal_review_request`'* ]] ||
    fail 'commander review receipt must precede human goal review'
  [[ "$flow" == *'atct_goal_review_request`'*'atct_goal_review_complete`'* ]] ||
    fail 'human goal review must precede atomic finalization'
  ! grep -Fq -- 'atct_goal_handoff_complete` → `atct_goal_review_request`' <<<"$flow" ||
    fail 'normal lifecycle must not close the handoff before human review'

  grep -Fq -- 'only the commander may call `atct_goal_review_request`' <<<"$section" ||
    fail 'goal delegation section must assign human goal review request to commander'
  grep -Fq -- 'only the commander may call `atct_goal_review_complete`' <<<"$section" ||
    fail 'goal delegation section must assign atomic goal review completion to commander'
  assert_file_not_contains 'record completion by calling `atct_goal_complete` and then `atct_goal_handoff_complete`' "$REPO_ROOT/skills/atct/SKILL.md"
}

test_goal_handoff_cause_is_preserved() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local goal_section

  goal_section="$(sed -n '/^## Delegate a goal$/,$p' "$REPO_ROOT/skills/commander/SKILL.md")"
  grep -Fq -- 'Claiming the goal first always' <<<"$goal_section" ||
    fail 'goal delegation section lost the claim-to-handoff refusal cause'
}

test_role_contract_is_documented() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local documented_rows
  local role
  local row
  local does
  local does_not

  assert_file_contains '| Layer | Does | Does not |' "$atct_skill"
  documented_rows="$(
    sed -n '/^## Roles$/,/^## Declare before you work$/p' "$atct_skill" |
      sed -nE 's/^\|[[:space:]]*`(commander|executor|task_worker)`[[:space:]]*\|[[:space:]]*([^|]+)[[:space:]]*\|[[:space:]]*([^|]+)[[:space:]]*\|[[:space:]]*$/\1|\2|\3/p'
  )"
  assert_eq $'commander\nexecutor\ntask_worker' "$(printf '%s\n' "$documented_rows" | cut -d'|' -f1 | sort)" \
    'role boundary table must contain exactly one row for each role'

  for role in commander executor task_worker; do
    row="$(printf '%s\n' "$documented_rows" | awk -F'|' -v role="$role" '$1 == role { print; exit }')"
    [[ -n "$row" ]] || fail "role boundary table is missing $role"
    does="${row#*|}"
    does="${does%%|*}"
    does_not="${row##*|}"
    [[ -n "$(tr -d '[:space:]' <<<"$does")" ]] || fail "$role boundary table has no does value"
    [[ -n "$(tr -d '[:space:]' <<<"$does_not")" ]] || fail "$role boundary table has no does_not value"
  done
}

test_role_response_exposes_boundary_fields() {
  local handler_go="$REPO_ROOT/internal/daemon/handler.go"
  local tools_go="$REPO_ROOT/internal/mcpshim/tools.go"

  assert_file_matches '^[[:space:]]*Does[[:space:]]+\[\]string[[:space:]]+`json:"does"`' "$handler_go"
  assert_file_matches '^[[:space:]]*DoesNot[[:space:]]+\[\]string[[:space:]]+`json:"does_not"`' "$handler_go"
  assert_file_matches '^[[:space:]]*Does[[:space:]]+\[\]string[[:space:]]+`json:"does"`' "$tools_go"
  assert_file_matches '^[[:space:]]*DoesNot[[:space:]]+\[\]string[[:space:]]+`json:"does_not"`' "$tools_go"
}

test_role_contract_uses_neutral_language() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local roles_section

  roles_section="$(sed -n '/^## Roles$/,/^## Declare before you work$/p' "$atct_skill")"
  if grep -Eiq 'space|worktree|git|harness|multiplexer' <<<"$roles_section"; then
    fail 'role boundary table must use neutral language'
  fi
}

test_role_boundaries_cover_version_control_and_delegation_direction() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local handler_go="$REPO_ROOT/internal/daemon/handler.go"

  assert_file_contains "design the goal / implement and test the goal's work / use sub-agents as it sees fit" "$atct_skill"
  assert_file_contains "commit the goal's work" "$atct_skill"
  assert_file_contains 'write internal version-control details' "$atct_skill"
  assert_file_contains "use sub-agents as it sees fit" "$handler_go"
  assert_file_matches '^[[:space:]]*"task_worker":[[:space:]]*\{Does: \[\]string\{"implement", "test", "close the task it was given"\}, DoesNot: \[\]string\{"make design decisions", "re-delegate", "commit", "write internal version-control details"\}\}' "$handler_go"
}

test_task_worker_boundary_includes_task_closure() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains 'close the task it was given' "$atct_skill"
}

test_executor_boundary_includes_goal_commit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains "commit the goal's work" "$atct_skill"
}

test_commit_workflow_requires_explicit_paths() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains 'name the paths explicitly; never use `git add -A`' "$atct_skill"
}

test_task_worker_boundary_keeps_commit_exclusion() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains '| `task_worker` | implement / test / close the task it was given | make design decisions / re-delegate / commit / write internal version-control details |' "$atct_skill"
}

test_start_does_not_claim_delegate_cannot_close() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_not_contains 'the delegate cannot' "$start_skill"
}

test_start_does_not_claim_delegate_lacks_claim() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_not_contains 'does not hold the claim' "$start_skill"
}

test_role_table_has_no_task_update_procedure() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local roles_section

  roles_section="$(sed -n '/^## Roles$/,/^## Declare before you work$/p' "$atct_skill")"
  if grep -Fq -- 'atct_task_update' <<<"$roles_section"; then
    fail 'role boundary table must not prescribe atct_task_update procedure'
  fi
}

test_role_contract_matches_implementation() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local handler_go="$REPO_ROOT/internal/daemon/handler.go"
  local tools_go="$REPO_ROOT/internal/mcpshim/tools.go"
  local implementation_roles
  local documented_roles
  local implementation_boundaries
  local documented_boundaries

  implementation_roles="$(
    sed -n 's/.*expected_role must be one of \([^\"]*\).*/\1/p' "$tools_go" |
      tr ',' '\n' |
      sed 's/^ *//; /^$/d'
  )"
  documented_roles="$(
    sed -n 's/^Role values for `expected_role`: \(.*\)\.$/\1/p' "$atct_skill" |
      tr -d '`' |
      tr ',' '\n' |
      sed 's/^ *//; /^$/d'
  )"

  assert_file_contains 'Role values for `expected_role`: `commander`, `executor`, `subcommander`, `task_worker`.' "$atct_skill"
  if [[ -z "$implementation_roles" || -z "$documented_roles" ]]; then
    printf 'role contract could not be extracted\n' >&2
    return 1
  fi
  if [[ "$implementation_roles" != "$documented_roles" ]]; then
    printf 'role contract differs from implementation: implementation=%s documented=%s\n' \
      "$implementation_roles" "$documented_roles" >&2
    return 1
  fi

  implementation_boundaries="$(
    sed -nE 's/^[[:space:]]*"((commander|subcommander|executor|task_worker))":[[:space:]]*\{[[:space:]]*Does:[[:space:]]*\[\]string\{([^}]*)\},[[:space:]]*DoesNot:[[:space:]]*\[\]string\{([^}]*)\},?[[:space:]]*\},?[[:space:]]*$/\1|\3|\4/p' "$handler_go" |
      sed -E 's/"//g; s/[[:space:]]*,[[:space:]]*/ \/ /g; s/[[:space:]]*\|[[:space:]]*/|/g; s/[[:space:]]+/ /g; s/^[[:space:]]+|[[:space:]]+$//g'
  )"
  documented_boundaries="$(
    sed -nE 's/^\|[[:space:]]*`(commander|executor|task_worker)`[[:space:]]*\|[[:space:]]*([^|]+)[[:space:]]*\|[[:space:]]*([^|]+)[[:space:]]*\|[[:space:]]*$/\1|\2|\3/p' "$atct_skill" |
      sed -E 's/[[:space:]]*\/[[:space:]]*/ \/ /g; s/[[:space:]]*\|[[:space:]]*/|/g; s/[[:space:]]+/ /g; s/^[[:space:]]+|[[:space:]]+$//g'
  )"
  if [[ -z "$implementation_boundaries" || -z "$documented_boundaries" ]]; then
    printf 'role boundary contract could not be extracted\n' >&2
    return 1
  fi
  if [[ "$implementation_boundaries" != "$documented_boundaries" ]]; then
    printf 'role boundary contract differs from implementation: implementation=%s documented=%s\n' \
      "$implementation_boundaries" "$documented_boundaries" >&2
    return 1
  fi
}

test_worktree_rule_defers_to_superpowers() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## One workspace per goal$/p' "$commander_skill")"
  grep -Fq -- 'superpowers:using-git-worktrees' <<<"$worktree_section" ||
    fail 'worktree rule must refer to superpowers:using-git-worktrees'
  if grep -Fq -- 'git worktree add' <<<"$worktree_section"; then
    fail 'worktree rule must not copy the git worktree add procedure'
  fi
  if grep -Fq -- 'git check-ignore' <<<"$worktree_section"; then
    fail 'worktree rule must not copy the git check-ignore procedure'
  fi
}

test_worktree_rule_names_who_creates_it() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## One workspace per goal$/p' "$commander_skill")"
  for role in commander executor; do
    grep -Fq -- "$role" <<<"$worktree_section" ||
      fail "worktree rule must name $role"
  done
  grep -Eiq -- 'executor.*(does not|never|must not) create' <<<"$worktree_section" ||
    fail 'worktree rule must say that the executor does not create worktrees'
}

test_worktree_rule_allows_the_primary_checkout() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## One workspace per goal$/p' "$commander_skill")"
  grep -Fq -- '### When the primary checkout is right' <<<"$worktree_section" ||
    fail 'worktree rule must name when the primary checkout is right'
  grep -Eiq -- 'commander.*review' <<<"$worktree_section" ||
    fail 'primary checkout exceptions must include commander review'
  grep -Eiq -- 'commander.*release' <<<"$worktree_section" ||
    fail 'primary checkout exceptions must include commander release'
}

test_worktree_rule_chooses_the_setup_script_over_a_native_tool() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## One workspace per goal$/p' "$commander_skill")"
  for term in script/worktree-setup.sh EnterWorktree web/node_modules web/dist; do
    grep -Fq -- "$term" <<<"$worktree_section" ||
      fail "worktree rule must mention $term"
  done
}

test_worktree_rule_lists_what_is_not_separated() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## One workspace per goal$/p' "$commander_skill")"
  grep -Fq -- '### What a worktree does not separate' <<<"$worktree_section" ||
    fail 'worktree rule must name what a worktree does not separate'
  grep -Fq -- '~/.atct/atct.db' <<<"$worktree_section" ||
    fail 'worktree rule must mention the shared ATCT database'
  grep -Fq -- 'daemon' <<<"$worktree_section" ||
    fail 'worktree rule must mention the shared daemon'
  grep -Fq -- 'GOCACHE' <<<"$worktree_section" || fail 'worktree rule must mention the shared Go build cache'
}

test_worktree_paths_match_the_setup_script() {
  local commander_skill="$REPO_ROOT/skills/commander/SKILL.md"
  local setup_script="$REPO_ROOT/script/worktree-setup.sh"
  local worktree_section
  local setup_worktree
  local setup_branch
  local documented_worktree
  local documented_branch

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## One workspace per goal$/p' "$commander_skill")"
  setup_worktree="$(sed -nE 's/^worktree="\$repo\/(.*)"/\1/p' "$setup_script")"
  setup_branch="$(sed -nE 's/^branch="(.*)"/\1/p' "$setup_script")"
  documented_worktree="$(sed 's/\${goal_id}/<goal-id>/g' <<<"$setup_worktree")"
  documented_branch="$(sed 's/\${goal_id}/<goal-id>/g' <<<"$setup_branch")"
  [[ -n "$documented_worktree" ]] || fail 'setup script worktree path could not be extracted'
  [[ -n "$documented_branch" ]] || fail 'setup script branch name could not be extracted'
  grep -Fq -- "$documented_worktree" <<<"$worktree_section" ||
    fail 'worktree rule must document the setup script worktree path'
  grep -Fq -- "$documented_branch" <<<"$worktree_section" ||
    fail 'worktree rule must document the setup script branch name'
}

test_role_response_does_not_leak_other_boundaries() {
  local handler_go="$REPO_ROOT/internal/daemon/handler.go"
  local role_response
  local selected_role
  local response_block

  role_response="$(sed -n '/^func roleResponseFor(assignment roleAssignment, uiLocale string) any {$/,/^}$/p' "$handler_go")"
  [[ -n "$role_response" ]] || fail 'role response function could not be extracted'

  selected_role="$(sed -nE 's/^[[:space:]]*boundary := roleBoundaries\[([^]]+)\][[:space:]]*$/\1/p' <<<"$role_response")"
  assert_eq 'visible' "$selected_role" 'role response must select the boundary for its current role'

  for response_type in commanderRole goalExecutorRole executorRole; do
    response_block="$(sed -n "/return ${response_type}{/,/^[[:space:]]*}[[:space:]]*$/p" <<<"$role_response")"
    [[ -n "$response_block" ]] || fail "$response_type response block could not be extracted"
    grep -Eq -- 'Does:[[:space:]]+does,' <<<"$response_block" ||
      fail "$response_type response must use the selected boundary's Does"
    grep -Eq -- 'DoesNot:[[:space:]]+doesNot,' <<<"$response_block" ||
      fail "$response_type response must use the selected boundary's DoesNot"
  done
}

test_skill_numbering_is_contiguous() {
  local skill
  local violations

  for skill in "${NUMBERED_SKILLS[@]/#/$REPO_ROOT/skills/}"; do
    skill="$skill/SKILL.md"
    violations="$(numbering_violations "$skill")"
    [[ -z "$violations" ]] || fail "numbered lists are broken:"$'\n'"$violations"
  done
}

test_ordered_sections_name_the_out_of_order_consequence() {
  local skill
  local violations

  for skill in "${ORDERED_SECTION_SKILLS[@]/#/$REPO_ROOT/skills/}"; do
    violations="$(out_of_order_violations "$skill/SKILL.md")"
    [[ -z "$violations" ]] || fail "numbered sections do not name the cost of running out of order:"$'\n'"$violations"
  done
}

test_ordered_sections_are_numbered() {
  local entry
  local skill
  local heading
  local numbered

  for entry in "${ORDERED_SECTIONS[@]}"; do
    skill="$REPO_ROOT/skills/${entry%%|*}/SKILL.md"
    heading="${entry#*|}"
    grep -Fxq -- "$heading" "$skill" ||
      fail "<$skill> has no section titled <$heading>"
    numbered="$(
      awk -v want="$heading" '
        $0 == want { inside = 1; next }
        /^## |^### / { inside = 0 }
        inside && /^[0-9]+\. / { found++ }
        END { print found + 0 }
      ' "$skill"
    )"
    [[ "$numbered" -gt 0 ]] ||
      fail "<$heading> is an ordered section but numbers none of its steps"
  done
}

test_numbering_check_catches_a_broken_list() {
  local sample_dir="$TEMP_ROOT/numbering-samples"
  local sample
  local violations

  mkdir -p "$sample_dir"

  cat >"$sample_dir/gap.md" <<'MARKDOWN'
## Skips a number

1. Claim the task.
2. Do the work.
4. Close the task.

**Out of order:** Nothing lands.
MARKDOWN

  cat >"$sample_dir/duplicate.md" <<'MARKDOWN'
## Repeats a number

1. Claim the task.
1. Do the work.
2. Close the task.

**Out of order:** Nothing lands.
MARKDOWN

  cat >"$sample_dir/not-first.md" <<'MARKDOWN'
## Does not start at 1

2. Do the work.
3. Close the task.

**Out of order:** Nothing lands.
MARKDOWN

  cat >"$sample_dir/single.md" <<'MARKDOWN'
## Numbers a single step

1. Close the task.

**Out of order:** Nothing lands.
MARKDOWN

  cat >"$sample_dir/quote-gap.md" <<'MARKDOWN'
## Skips a number inside a quoted request

1. Put these exact instructions at the beginning of the request:

   > 1. commit the work
   > 3. close every task

2. Wake the worker.

**Out of order:** Nothing lands.
MARKDOWN

  for sample in gap duplicate not-first single quote-gap; do
    violations="$(numbering_violations "$sample_dir/$sample.md")"
    [[ -n "$violations" ]] ||
      fail "numbering_violations reported nothing for the <$sample> sample"
  done
}

test_out_of_order_check_catches_a_missing_consequence() {
  local sample_dir="$TEMP_ROOT/out-of-order-samples"
  local violations

  mkdir -p "$sample_dir"

  cat >"$sample_dir/missing.md" <<'MARKDOWN'
## Numbers its steps and says nothing about their order

1. Claim the task.
2. Do the work.
3. Close the task.
MARKDOWN

  violations="$(out_of_order_violations "$sample_dir/missing.md")"
  [[ -n "$violations" ]] ||
    fail 'out_of_order_violations reported nothing for the <missing> sample'
}

test_static_contract
test_delegated_claim_contract_is_explicit
test_declared_task_content_fix_contract_is_explicit
test_start_identifies_before_monitor
test_start_session_key_contract_is_explicit
test_start_forces_commander_claim
test_role_skills_are_routed_from_shared_atct
test_role_entry_receives_with_credentials_before_role_check
test_atct_skill_requires_execution_flow
test_identify_is_told_to_pass_its_cwd
test_start_explains_claim_recovery_boundary
test_start_explains_mcp_reconnect_gap
test_start_monitor_is_not_first_step
test_start_does_not_branch_on_session_attachment
test_start_does_not_duplicate_delegated_worker_preamble
test_start_keeps_monitor_persistence_requirement
test_atct_skill_defines_the_background_watch
test_start_documents_liveness_authority_boundary
test_start_uses_monitor_watch_for_claude_actions
test_readme_documents_liveness_contract
test_goal_handoff_watch_contract_is_explicit
test_goal_handoff_watch_contract_omits_unsafe_variants
test_goal_handoff_watch_contract_has_required_order
test_goal_handoff_forbids_upward_design_questions
test_goal_handoff_names_the_single_upward_message
test_goal_handoff_forbids_the_executor_decision_ask
test_goal_handoff_routes_decisions_through_a_request_entry
test_goal_handoff_routes_cross_goal_facts_through_the_request_entry
test_goal_handoff_silence_has_required_order
test_goal_handoff_preamble_does_not_invite_upward_reports
test_goal_delegation_requires_the_adjacent_goal_boundary
test_goal_delegation_keeps_the_delegator_out_until_completion
test_delegator_answers_are_balanced
test_delegator_answers_names_the_wrong_answers_measurement
test_unsent_report_table_covers_every_spoken_kind
test_unsent_report_names_the_stall_detection
test_goal_handoff_cause_is_preserved
test_goal_completion_is_commander_owned
test_role_contract_is_documented
test_role_response_exposes_boundary_fields
test_role_contract_uses_neutral_language
test_role_boundaries_cover_version_control_and_delegation_direction
test_task_worker_boundary_includes_task_closure
test_executor_boundary_includes_goal_commit
test_commit_workflow_requires_explicit_paths
test_task_worker_boundary_keeps_commit_exclusion
test_start_does_not_claim_delegate_cannot_close
test_start_does_not_claim_delegate_lacks_claim
test_role_table_has_no_task_update_procedure
test_role_contract_matches_implementation
test_role_response_does_not_leak_other_boundaries
test_worktree_rule_defers_to_superpowers
test_worktree_rule_names_who_creates_it
test_worktree_rule_allows_the_primary_checkout
test_worktree_rule_chooses_the_setup_script_over_a_native_tool
test_worktree_rule_lists_what_is_not_separated
test_worktree_paths_match_the_setup_script
test_recovery_section_has_role_entry
test_recovery_section_prioritizes_session_identify
test_recovery_section_has_project_path
test_recovery_section_has_goal_path
test_recovery_section_has_task_path_and_non_repair_note
test_handoff_completion_reports_are_explicit
test_handoff_report_repair_is_explicit
test_executor_skill_states_the_blocked_output
test_executor_skill_sets_up_the_watch_between_receive_and_role
test_recovery_section_explains_why_the_role_drops
test_orchestration_skill_has_no_blanket_atct_ban
test_goal_handoff_completion_keeps_one_normal_path
test_handoff_report_repair_follows_goal_delegation
test_recovery_section_omits_session_header
test_recovery_section_omits_agent_sessions
test_recovery_section_omits_task_release
test_recovery_section_omits_task_update
test_recovery_section_omits_goal_release
test_recovery_section_names_existing_tools
test_one_workspace_per_goal_section_exists
test_one_workspace_per_goal_binds_a_workspace_to_one_goal
test_one_workspace_per_goal_closes_on_approval
test_executor_uses_the_advisor_before_approach_and_review_request
test_commander_keeps_the_advisor_enabled_and_the_independent_check
test_one_workspace_per_goal_forbids_reuse
test_one_workspace_per_goal_names_the_only_exception
test_one_workspace_per_goal_sits_between_worktree_and_commit
test_stop_hooks_share_server_check
test_claude_hooks_json_keeps_session_start_and_pre_tool_use_sections
test_codex_hook_commands_are_shell_commands
test_codex_hook_commands_dispatch_to_their_subcommand
test_codex_merge_check_fails_closed_to_deny
test_codex_merge_check_passes_through_atct_output
test_claude_hook_commands_resolve_to_executable_scripts
test_claude_merge_check_fails_closed_to_deny
test_claude_monitor_check_fails_open
test_real_atct_binary_denies_unapproved_goal_merge
test_stop_hook_file_is_executable_but_other_hooks_remain
test_mcp_instructions_include_active_goal_permission
test_mcp_instructions_delegate_human_decisions_to_the_skill
test_skill_numbering_is_contiguous
test_ordered_sections_name_the_out_of_order_consequence
test_ordered_sections_are_numbered
test_numbering_check_catches_a_broken_list
test_out_of_order_check_catches_a_missing_consequence
printf 'PASS wrapper tests\n'
