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
ORDERED_SECTIONS=(
  '## Declare before you work'
  '## Receive before you start'
  '## Delegate a task'
  '### Two-layer delegation'
  '## Delegate a goal'
  '## Fill in a report on a handoff that is already closed'
  '## Recover when your role comes back wrong'
  '## Close a task the moment it is finished'
  '## Report completion in six parts'
  '## Apply what you were told'
  '## Finishing'
)

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
  printf '%s' 'ATCT session key: hook-session-1'
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
  assert_eq 'ATCT session key: hook-session-1' "$output" 'Codex SessionStart hook must use the PATH CLI'
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

delegate_goal_section() {
  sed -n '/^## Delegate a goal$/,/^## Recover when your role comes back wrong$/p' \
    "$REPO_ROOT/skills/atct/SKILL.md"
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
  fin="$(lineno 'When all task handoffs are accepted, record the goal review request by calling')"

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
  delegate_goal_section_contains '`next_steps` for what you left, and `atct_decision_ask` for anything that'

  local section
  section="$(unsent_report_section)"
  grep -Fq -- '| the goal is ready for commander review | `atct_goal_handoff_review_request`, the one message |' <<<"$section" ||
    fail 'unsent report section must name the single upward review message'
}

test_goal_handoff_routes_cross_goal_facts_to_the_human() {
  delegate_goal_section_contains 'A fact that spans another goal is not an exception. Raise it with'
  delegate_goal_section_contains 'passing through the delegator.'
}

test_goal_handoff_silence_has_required_order() {
  local lineno
  local watch
  local silence
  local fin

  lineno() { delegate_goal_section | grep -n -F -- "$1" | head -1 | cut -d: -f1; }
	watch="$(lineno 'Then, in Claude Code only, attach `atct watch --monitor --token')"
  silence="$(lineno "Decide this goal's design yourself. Do not bring the delegator a design")"
  fin="$(lineno 'When all task handoffs are accepted, record the goal review request by calling')"

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
  delegate_goal_section_contains 'goals, and a boundary left unstated becomes a question the subcommander'
}

test_goal_delegation_keeps_the_delegator_out_until_completion() {
  delegate_goal_section_contains '6. Stay out until the completion report. After waking the subcommander, the'
  delegate_goal_section_contains 'arrive from `atct watch` rather than from the subcommander: a goal with no'
  delegate_goal_section_contains '`atct_goal_handoff_review_request` lands; that report is the entry point.'
}

test_delegator_answers_are_balanced() {
  local section
  local delegator_heading
  local subcommander_heading
  local delegator_count
  local subcommander_count

  section="$(delegator_answers_section)"
  delegator_heading="$(grep -n -F -- 'Four kinds of question belong to the delegator' <<<"$section" | head -1 | cut -d: -f1)"
  subcommander_heading="$(grep -n -F -- 'Four kinds look similar and belong to the subcommander' <<<"$section" | head -1 | cut -d: -f1)"

  [[ -n "$delegator_heading" && -n "$subcommander_heading" ]] ||
    fail 'delegator answer balance requires both question headings'

  delegator_count="$(awk -v start="$delegator_heading" -v end="$subcommander_heading" '
    NR > start && NR < end && /^- / { count++ }
    END { print count + 0 }
  ' <<<"$section")"
  subcommander_count="$(awk -v start="$subcommander_heading" '
    NR > start && /^- / { count++ }
    END { print count + 0 }
  ' <<<"$section")"

  assert_eq '4' "$delegator_count" 'delegator answers must list four question kinds'
  assert_eq '4' "$subcommander_count" 'subcommander answers must list four question kinds'
  assert_eq "$delegator_count" "$subcommander_count" 'delegator and subcommander question counts must match'
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

test_task_delegation_preamble_is_untouched_by_upward_silence() {
  local task_section
  task_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$REPO_ROOT/skills/atct/SKILL.md")"

  for needle in \
    'Send the delegator nothing until the completion' \
    'Stay out until the completion report' \
    'Name in the request every adjacent goal'; do
    if grep -Fq -- "$needle" <<<"$task_section"; then
      fail "goal 181 owns the task delegation preamble: <$needle>"
    fi
  done
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
  sed -n '/^## Recover when your role comes back wrong$/,/^## Close a task/p' \
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
  recovery_section_contains 'If `atct_role` returns `executor` while you still hold work that should be yours, stop working and read this section.'
}

test_recovery_section_prioritizes_session_identify() {
  recovery_section_contains 'The first recovery path is `atct_session_identify`; follow `### Session keys` first.'
}

test_recovery_section_has_project_path() {
  recovery_section_contains '- project: `atct_project_release` → `atct_project_claim`'
}

test_recovery_section_has_goal_path() {
  recovery_section_contains '- goal: `atct_goal_handoff_complete` → `atct_goal_handoff_request` (legacy/out-of-order recovery; the commander must issue the handoff again)'
  recovery_section_contains 'the subcommander cannot reissue it, and the goal waits on the'
}

test_recovery_section_has_task_path_and_non_repair_note() {
  recovery_section_contains 'have the subcommander request a fresh `atct_task_handoff_request`'
  recovery_section_contains 'Rejection is automatic, so the goal step above that asks the commander to reissue the handoff is needed only for the last trigger.'
}

test_handoff_completion_reports_are_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  assert_file_contains 'with the `handoff_id`, `task_id`, and a `complete_report`' "$atct_skill"
  assert_file_contains 'with the `goal_id` provided in this request' "$atct_skill"
  assert_file_contains 'what was done, what was verified, what could not' "$atct_skill"
  assert_file_contains 'be verified, and paths changed' "$atct_skill"
}

test_task_delegation_verification_contract_is_explicit() {
  local task_section
  task_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$REPO_ROOT/skills/atct/SKILL.md" | tr '\n' ' ' | tr -s ' ')"

  grep -Fq -- 'verification commands the worker can run.' <<<"$task_section" ||
    fail 'SKILL.md Delegate a task must require the delegator to name worker-runnable verification commands'
  grep -Fq -- '`go test ./...` in the request.' <<<"$task_section" ||
    fail 'SKILL.md Delegate a task must prohibit broad `go test ./...` requests'
  grep -Eq -- 'worker sandbox.*delegator sandbox' <<<"$task_section" ||
    fail 'SKILL.md Delegate a task must explain that worker and delegator sandboxes differ'
  grep -Fq -- 'The delegator runs every verification not named for the worker and includes it in review.' <<<"$task_section" ||
    fail 'SKILL.md Delegate a task must make the delegator run unnamed verification and include it in review'
  grep -Fq -- 'It must say "could not run" in its completion report.' <<<"$task_section" ||
    fail 'SKILL.md Delegate a task must require workers to report verification they could not run'
  grep -Fq -- 'Do not use `--version` or `--help` to determine availability:' <<<"$task_section" ||
    fail 'SKILL.md Delegate a task must prohibit using `--version` or `--help` to determine whether a worker can use a tool'
}

test_handoff_report_repair_is_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  assert_file_contains '## Fill in a report on a handoff that is already closed' "$atct_skill"
  assert_file_contains '`atct_task_handoff_report_amend` with the specific `handoff_id`' "$atct_skill"
  assert_file_contains 'It is not part of normal executor completion.' "$atct_skill"
}

test_handoff_completion_keeps_one_normal_path() {
  local normal_section
  local completion_step
  normal_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$REPO_ROOT/skills/atct/SKILL.md")"
  completion_step="$(sed -n '/The task handoff review order is/,/^$/p' <<<"$normal_section")"
  ! grep -Fq -- 'atct_task_handoff_report_amend' <<<"$normal_section" || fail 'normal task completion must not name the repair tool'
  grep -Fq -- '`atct_task_handoff_complete`' <<<"$completion_step" || fail 'task review order must name reviewer completion'
  grep -Fq -- '`complete_report`' <<<"$normal_section" || fail 'task completion must require complete_report'
}

delegate_task_section() {
  sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' \
    "$REPO_ROOT/skills/atct/SKILL.md"
}

delegate_task_section_contains() {
  local needle="$1"
  local section
  section="$(delegate_task_section)"
  grep -Fq -- "$needle" <<<"$section" ||
    fail "delegate task section does not contain <$needle>"
}

# The two checks below measure the lists themselves, not the surrounding
# section. Searching the whole section is what let the lists rot: every allowed
# name and some forbidden ones also appear in the section's prose and quoted
# blocks, so deleting a name from its list still found it elsewhere and passed.
# Do not widen these back to `delegate_task_section`.
delegate_task_allowlist_line() {
  delegate_task_section |
    sed -n '/An executor may call only these atct tools:/{n;p;}'
}

delegate_task_forbidden_block() {
  delegate_task_section |
    sed -n '/An executor must not call/,/Spell the names out/p'
}

# Names are matched with their backticks so a name can never match inside a
# longer one: `atct_task_handoff_complete` would otherwise hit
# `atct_goal_handoff_complete`, which sits on the opposite list.
test_delegation_names_the_atct_tools_an_executor_may_call() {
  local allowlist
  local tool

  delegate_task_section_contains 'An executor may call only these atct tools:'

  # An empty extraction means the heading moved and every check below would
  # pass vacuously. A check that always passes is the hole itself.
  allowlist="$(delegate_task_allowlist_line)"
  [[ -n "$allowlist" ]] ||
    fail 'delegate task section has no allowlist line after `An executor may call only these atct tools:`'

  for tool in atct_session_identify atct_task_handoff_receive atct_role \
    atct_task_handoff_review_request; do
    grep -Fq -- "\`$tool\`" <<<"$allowlist" ||
      fail "the allowlist line does not allow <$tool>"
  done

  for tool in atct_goal_handoff_complete atct_goal_handoff_receive \
    atct_goal_handoff_request atct_goal_claim atct_goal_release \
    atct_goal_complete atct_goal_update_content atct_project_claim \
    atct_project_release atct_task_handoff_request \
    atct_task_handoff_review_receive atct_task_handoff_complete \
    atct_task_handoff_review_reject atct_task_update \
    atct_task_create atct_decision_ask; do
    ! grep -Fq -- "\`$tool\`" <<<"$allowlist" ||
      fail "the allowlist line must not allow the forbidden <$tool>"
  done
}

test_delegation_names_the_atct_tools_an_executor_must_not_call() {
  local forbidden
  local tool

  delegate_task_section_contains 'An executor must not call `atct_goal_handoff_complete`'

  # Same reason as above: an empty block would let every name through.
  forbidden="$(delegate_task_forbidden_block)"
  [[ -n "$forbidden" ]] ||
    fail 'delegate task section has no forbidden block from `An executor must not call` to `Spell the names out`'

  for tool in atct_goal_handoff_complete atct_goal_handoff_receive \
    atct_goal_handoff_request atct_goal_claim atct_goal_release \
    atct_goal_complete atct_goal_update_content atct_project_claim \
    atct_project_release atct_task_handoff_request \
    atct_task_handoff_review_receive atct_task_handoff_complete \
    atct_task_handoff_review_reject atct_task_update \
    atct_task_create atct_decision_ask; do
    grep -Fq -- "\`$tool\`" <<<"$forbidden" ||
      fail "the forbidden block does not forbid <$tool> by name"
  done

  for tool in atct_session_identify atct_task_handoff_receive atct_role \
    atct_task_handoff_review_request; do
    ! grep -Fq -- "\`$tool\`" <<<"$forbidden" ||
      fail "the forbidden block must not forbid the allowed <$tool>"
  done
}

test_delegation_requests_review_before_reviewer_closes_the_task() {
  local section
  local request_line
  local receive_line
  local complete_line

  delegate_task_section_contains 'Report the review request before the reviewer closes the task'

  section="$(delegate_task_section)"
  request_line="$(grep -nF -- 'record the review request by calling `atct_task_handoff_review_request`' \
    <<<"$section" | head -1 | cut -d: -f1 || true)"
  receive_line="$(grep -nF -- 'receives that review with `atct_task_handoff_review_receive`' \
    <<<"$section" | head -1 | cut -d: -f1 || true)"
  complete_line="$(grep -nF -- 'then calls `atct_task_handoff_complete`' \
    <<<"$section" | head -1 | cut -d: -f1 || true)"

  [[ -n "$request_line" ]] || fail 'delegate task section never says to request review'
  [[ -n "$receive_line" ]] || fail 'delegate task section never says the reviewer receives the review'
  [[ -n "$complete_line" ]] || fail 'delegate task section never says the reviewer completes the handoff'
  (( request_line < receive_line && receive_line < complete_line )) ||
    fail "task review must be requested, received, and completed in order: request=$request_line receive=$receive_line complete=$complete_line"
}

test_recovery_section_explains_why_the_role_drops() {
  recovery_section_contains "Closing a subcommander's goal handoff drops that subcommander to \`executor\`"
}

test_orchestration_skill_has_no_blanket_atct_ban() {
  # The orchestration skill lives in the dotfiles repository, which this one
  # cannot change, so this check has two states: the file is absent and there is
  # nothing to inspect, or it is present and every assertion runs.
  #
  # There used to be a third state, "present but not updated yet", selected by
  # grepping the file for `atct_session_identify`. That grep was the hole: when
  # the allowlist disappears and the blanket ban comes back -- the very
  # regression this test exists to catch -- the grep goes false, both assertions
  # are skipped, and the test passes. "Updated" and "regressed" looked alike.
  # The dotfiles change has since landed, so the "not updated" branch guards
  # nothing; the assertions now run unconditionally and `atct_session_identify`
  # is one of them rather than the thing deciding whether to check.
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

  # The blanket ban must be gone.
  assert_file_not_contains '**ATCT ツールの呼び出し**' "$orchestration"

  # Named allowlist and prohibition, one tool at a time so a failure says which
  # one went missing. Backticks keep a short name from matching inside a longer
  # one, such as `atct_handoff_complete` inside `atct_goal_handoff_complete`.
  local allowed=(
    atct_session_identify
    atct_handoff_receive
    atct_role
    atct_handoff_complete
    atct_task_update
  )
  local forbidden=(
    atct_goal_handoff_complete
    atct_goal_handoff_receive
    atct_goal_handoff_request
    atct_goal_claim
    atct_goal_release
    atct_goal_complete
    atct_goal_update_content
    atct_project_claim
    atct_project_release
    atct_task_claim
    atct_handoff_request
    atct_task_declare
    atct_decision_ask
  )
  local tool
  for tool in "${allowed[@]}" "${forbidden[@]}"; do
    assert_file_contains "\`$tool\`" "$orchestration"
  done

  # Both orderings that a wrong sequence silently destroys.
  assert_file_contains '`atct_handoff_complete` を先に呼び、`atct_task_update(status="done")` を後に呼ぶ' "$orchestration"
  assert_file_contains '`atct_task_claim` を先に呼ばない' "$orchestration"
}

test_goal_handoff_completion_keeps_one_normal_path() {
  local goal_section
  local completion_step
  goal_section="$(sed -n '/^## Delegate a goal$/,/^### Session keys$/p' "$REPO_ROOT/skills/atct/SKILL.md")"
  completion_step="$(sed -n '/When all task handoffs are accepted, record the goal review request by calling/,/^$/p' <<<"$goal_section")"
  ! grep -Fq -- 'atct_goal_handoff_report_amend' <<<"$goal_section" || fail 'normal goal completion must not name the repair tool'
  grep -Fq -- '`atct_goal_handoff_review_request`' <<<"$completion_step" || fail 'goal completion must start with a review request'
  grep -Fq -- 'the completion report while the handoff remains open' <<<"$goal_section" || fail 'goal review request must record the completion report while the handoff remains open'
}

test_handoff_report_repair_follows_goal_delegation() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local goal_end
  local repair
  local recovery
  goal_end="$(grep -n '^### Session keys$' "$atct_skill" | cut -d: -f1)"
  repair="$(grep -n '^## Fill in a report on a handoff that is already closed$' "$atct_skill" | cut -d: -f1)"
  recovery="$(grep -n '^## Recover when your role comes back wrong$' "$atct_skill" | cut -d: -f1)"
  (( goal_end < repair && repair < recovery )) || fail 'handoff report repair must follow goal delegation and precede recovery'
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

test_one_space_per_goal_section_exists() {
  assert_file_contains '## One space per goal' "$REPO_ROOT/skills/atct/SKILL.md"
}

test_one_space_per_goal_binds_a_space_to_one_goal() {
  assert_file_contains 'A space belongs to one goal' "$REPO_ROOT/skills/atct/SKILL.md"
}

test_one_space_per_goal_closes_on_approval() {
  assert_file_contains 'approving the completion' "$REPO_ROOT/skills/atct/SKILL.md"
}

test_one_space_per_goal_forbids_reuse() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  assert_file_contains 'do not hand it a second goal' "$atct_skill"
  assert_file_contains 'A closed space is not reopened' "$atct_skill"
}

test_one_space_per_goal_names_the_only_exception() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  assert_file_contains "The \`commander\`'s own space is the exception, and there is no other." "$atct_skill"
  assert_file_contains 'A rejected completion is the same goal' "$atct_skill"
}

test_one_space_per_goal_sits_between_worktree_and_commit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local worktree
  local space
  local commit
  worktree="$(grep -n '^## One worktree per goal$' "$atct_skill" | cut -d: -f1)"
  space="$(grep -n '^## One space per goal$' "$atct_skill" | cut -d: -f1)"
  commit="$(grep -n '^## Commit safely$' "$atct_skill" | cut -d: -f1)"
  (( worktree < space && space < commit )) ||
    fail 'one space per goal must follow the worktree rule and precede commit safely'
}

test_delegated_claim_contract_is_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'Hold the parent, not the task.' "$atct_skill"
  assert_file_contains 'Hold the parent, not the goal.' "$atct_skill"
  assert_file_contains '## Delegate a task' "$atct_skill"
  assert_file_contains '## Delegate a goal' "$atct_skill"
  assert_file_contains 'First record receipt of the handoff by calling `atct_task_handoff_receive`' "$atct_skill"
  assert_file_contains 'Then record receipt of the goal handoff by calling' "$atct_skill"
  assert_file_contains 'Every implementation task is delegated.' "$atct_skill"
  assert_file_contains 'Delegating a task requires a received goal handoff, not a project claim.' "$atct_skill"
  assert_file_contains 'For two-layer delegation, the commander calls `atct_goal_claim` to create a goal handoff addressed to itself.' "$atct_skill"
  assert_file_contains 'a received, uncompleted goal handoff' "$atct_skill"
  assert_file_contains 'with `atct_task_handoff_receive` before starting it.' "$start_skill"
  assert_file_contains 'The loop coordinates delegated work.' "$start_skill"
  assert_file_contains 'Record the handoff before waking the worker.' "$atct_skill"
  assert_file_contains 'The delegator must call `atct_task_handoff_request`' "$atct_skill"
  assert_file_contains 'the `task_id` and `handoff_id` provided in this request' "$atct_skill"
  assert_file_contains '`session_key` (plus the optional `monitor_token`, when emitted) from SessionStart.' "$atct_skill"
  assert_file_contains 'The delegator must call `atct_goal_handoff_request`' "$atct_skill"
  assert_file_contains 'with the `goal_id` provided in this request and the exact `session_key` from SessionStart.' "$atct_skill"
  assert_file_contains '`handoff_id` and `monitor_token` are optional' "$atct_skill"
  assert_file_contains 'passing only the `goal_id` and `handoff_id`' "$atct_skill"
  assert_file_contains 'never pass `session_key` or `monitor_token`' "$atct_skill"
  assert_file_contains '`atct_goal_review_complete` with the `goal_id` provided in this request.' "$atct_skill"
  assert_file_not_contains '`atct_goal_review_complete` with the `goal_id` provided in this request and a `complete_report`' "$atct_skill"
  assert_file_contains 'and a `complete_report`' "$atct_skill"
  assert_file_contains 'Do this before starting work.' "$atct_skill"
  assert_file_contains 'When the work is complete, record the review request by calling `atct_task_handoff_review_request`' "$atct_skill"
  assert_file_contains 'A subcommander must not claim the project' "$atct_skill"
  assert_file_contains 'A subcommander must not call `atct_goal_release`' "$atct_skill"
  assert_file_contains "commander's job." "$atct_skill"
  assert_file_contains 'The worker must perform both instructions itself before doing any work.' "$atct_skill"
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
  monitor_line="$(grep -n '^## .*Claude Code.*Monitor' "$start_skill" | head -1 | cut -d: -f1)"
  [[ -n "$identify_line" && -n "$monitor_line" ]] ||
    fail 'start order requires identify and monitor headings'
  (( identify_line < monitor_line )) ||
    fail "session identification must precede Monitor: identify=$identify_line monitor=$monitor_line"
}

test_start_session_key_contract_is_explicit() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'call `atct_session_identify`' "$start_skill"
  assert_file_contains '`session_key` and `monitor_token`' "$start_skill"
  assert_file_contains 'atct_task_handoff_receive` with its `task_id`' "$start_skill"
  assert_file_contains 'and `handoff_id`, using the exact' "$start_skill"
  assert_file_contains 'using the exact `session_key` and optional `monitor_token` from' "$start_skill"
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

  for role in commander subcommander executor; do
    assert_file_contains "name: $role" "$REPO_ROOT/skills/$role/SKILL.md"
    assert_file_contains "expected_role\` set to \`$role\`" "$REPO_ROOT/skills/$role/SKILL.md"
    assert_file_contains 'atct:atct' "$REPO_ROOT/skills/$role/SKILL.md"
    assert_file_contains "atct:$role" "$atct_skill"
  done
}

test_role_entry_receives_with_credentials_before_role_check() {
  local skill receipt_line role_line

  skill="$REPO_ROOT/skills/subcommander/SKILL.md"
  assert_file_contains 'atct_goal_handoff_receive`' "$skill"
  assert_file_contains 'exact `session_key` from SessionStart' "$skill"
  assert_file_contains 'optional `handoff_id` and' "$skill"
  assert_file_contains '`monitor_token` may be included' "$skill"
  receipt_line="$(grep -nF 'atct_goal_handoff_receive`' "$skill" | head -1 | cut -d: -f1)"
  role_line="$(grep -nF 'atct_role` with `expected_role' "$skill" | head -1 | cut -d: -f1)"
  [[ -n "$receipt_line" && -n "$role_line" ]] || fail 'subcommander entry must name receipt and role check'
  (( receipt_line < role_line )) || fail 'subcommander must receive the goal handoff before checking its role'

  skill="$REPO_ROOT/skills/executor/SKILL.md"
  assert_file_contains 'atct_task_handoff_receive`' "$skill"
  assert_file_contains 'the `task_id` and `handoff_id` provided' "$skill"
  assert_file_contains 'exact `session_key` from SessionStart' "$skill"
  assert_file_contains 'and optional' "$skill"
  assert_file_contains '`monitor_token`' "$skill"
  receipt_line="$(grep -nF 'atct_task_handoff_receive`' "$skill" | head -1 | cut -d: -f1)"
  role_line="$(grep -nF 'atct_role` with `expected_role' "$skill" | head -1 | cut -d: -f1)"
  [[ -n "$receipt_line" && -n "$role_line" ]] || fail 'executor entry must name receipt and role check'
  (( receipt_line < role_line )) || fail 'executor must receive the task handoff before checking its role'
}

test_atct_skill_requires_execution_flow() {
  assert_file_contains 'Follow `doc/execution-flow.md` for the ATCT execution flow.' "$REPO_ROOT/skills/atct/SKILL.md"
}

test_start_explains_claim_recovery_boundary() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'claim taken before the key was registered is not restored' "$start_skill"
}

test_start_explains_mcp_reconnect_gap() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'new version has just' "$start_skill"
  assert_file_contains 'MCP has not reconnected' "$start_skill"
  assert_file_contains 'recovery section in `skills/atct/SKILL.md`' "$start_skill"
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
}

test_start_documents_liveness_authority_boundary() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'A one-minute liveness line means' "$start_skill"
  assert_file_contains 'A subcommander accepts the plan first' "$start_skill"
  assert_file_contains 'run ... atct codex monitor -- <codex args>' "$start_skill"
  assert_file_contains 'submits the task for review; it does not commit' "$start_skill"
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

test_task_handoff_recreation_cause_is_documented() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local task_section

  task_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$atct_skill")"
  grep -Fq -- 'Hold the parent, not the task.' <<<"$task_section" ||
    fail 'task delegation section omits the parent-handoff requirement'
  ! grep -Fq -- 'atct_task_claim' <<<"$task_section" ||
    fail 'task delegation section retains the removed claim API'
}

test_task_handoff_recreation_uses_new_id() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local task_section

  task_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$atct_skill")"
  grep -Fq -- 'new `handoff_id`' <<<"$task_section" ||
    fail 'task delegation section omits new handoff_id recreation'
}

test_task_handoff_recreation_keeps_worker_identity() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local task_section

  task_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$atct_skill")"
  grep -Fq -- 'does not mean a different worker' <<<"$task_section" ||
    fail 'task delegation section changes worker identity for a follow-up'
}

test_task_handoff_uses_canonical_review_order() {
  local section
  local flow
  section="$(delegate_task_section)"

  for needle in \
    'The delegator must call `atct_task_handoff_request`' \
    '`atct_task_handoff_receive`' \
    '`atct_task_handoff_review_request`' \
    '`atct_task_handoff_review_receive`' \
    '`atct_task_handoff_complete`' \
    '`atct_task_handoff_review_reject`'; do
    grep -Fq -- "$needle" <<<"$section" ||
      fail "task delegation section omits canonical review tool <$needle>"
  done

  flow="$(sed -n '/The task handoff review order is/,/^$/p' <<<"$section" | tr '\n' ' ')"
  [[ -n "$flow" ]] || fail 'task delegation section omits the task handoff review order'
  [[ "$flow" == *'atct_task_handoff_request`'*'atct_task_handoff_receive`'* ]] ||
    fail 'task handoff request must precede receive in the documented order'
  [[ "$flow" == *'atct_task_handoff_receive`'*'atct_task_handoff_review_request`'* ]] ||
    fail 'task handoff receive must precede review request in the documented order'
  [[ "$flow" == *'atct_task_handoff_review_request`'*'atct_task_handoff_review_receive`'* ]] ||
    fail 'task handoff review request must precede review receive in the documented order'
  [[ "$flow" == *'atct_task_handoff_review_receive`'*'atct_task_handoff_complete`'* ]] ||
    fail 'task handoff review receive must precede reviewer completion in the documented order'

  assert_file_not_contains 'The delegator must call `atct_handoff_request`' "$REPO_ROOT/skills/atct/SKILL.md"
  assert_file_not_contains 'record completion by calling `atct_handoff_complete`' "$REPO_ROOT/skills/atct/SKILL.md"
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

test_executor_reuse_contract_is_explicit() {
  local section
  section="$(delegate_task_section)"

  for needle in \
    'use an idle executor pane if one is available.' \
    'Create a new executor pane only for parallel work, worktree isolation,' \
    'When an executor finishes and unassigned tasks remain, reuse an idle executor for the next task.' \
    'A different task alone is not a reason to create a new executor.' \
    'Start a new executor pane only for parallel work, worktree isolation, context exhaustion, or a topic change.' \
    'If no unassigned tasks remain, close the idle executor.'; do
    grep -Fq -- "$needle" <<<"$section" ||
      fail "task delegation section omits executor reuse rule <$needle>"
  done

  assert_file_not_contains 'create a fresh worker pane after the request succeeds' "$REPO_ROOT/skills/atct/SKILL.md"
  assert_file_not_contains 'Start a new worker for a different task.' "$REPO_ROOT/skills/atct/SKILL.md"
}

test_executor_reuse_contract_preserves_idle_worker_and_reason_gated_pane() {
  local section
  local idle_rule
  local reuse_rule
  local pane_rule
  local reason

  section="$(delegate_task_section)"
  idle_rule="$(grep -F -- 'use an idle executor pane if one is available.' <<<"$section" || true)"
  [[ -n "$idle_rule" ]] || fail 'task delegation section omits the idle executor choice'

  reuse_rule="$(grep -F -- 'When an executor finishes and unassigned tasks remain, reuse an idle executor for the next task.' <<<"$section" || true)"
  [[ -n "$reuse_rule" ]] || fail 'task delegation section omits reuse for a different unassigned task'
  grep -Fq -- 'A different task alone is not a reason to create a new executor.' <<<"$section" ||
    fail 'task delegation section permits a new executor for a different task alone'

  pane_rule="$(grep -F -- 'Start a new executor pane only for ' <<<"$section" | sed -E 's/^.*Start a new executor pane only for /Start a new executor pane only for /; s/[[:space:]]+If no unassigned tasks remain,.*$//' || true)"
  assert_eq \
    'Start a new executor pane only for parallel work, worktree isolation, context exhaustion, or a topic change.' \
    "$pane_rule" \
    'new executor pane rule must contain only the four permitted reasons'
  for reason in 'parallel work' 'worktree isolation' 'context exhaustion' 'a topic change'; do
    grep -Fq -- "$reason" <<<"$pane_rule" ||
      fail "new executor pane rule omits permitted reason <$reason>"
  done
}

test_goal_handoff_cause_is_preserved() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local goal_section

  goal_section="$(sed -n '/^## Delegate a goal$/,/^## Recover when your role comes back wrong$/p' "$atct_skill")"
  grep -Fq -- 'Claiming the goal first always' <<<"$goal_section" ||
    fail 'goal delegation section lost the claim-to-handoff refusal cause'
}

test_task_batch_record_context_reason_is_preserved() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local task_section

  task_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$atct_skill")"
  grep -Fq -- 'What breaks when you batch is the record, not the context.' <<<"$task_section" ||
    fail 'task delegation section lost the record/context reason'
}

test_task_batch_measurement_is_preserved() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local task_section

  task_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$atct_skill")"
  grep -Fq -- 'executor-33' <<<"$task_section" ||
    fail 'task delegation section lost the three-task measurement'
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
      sed -nE 's/^\|[[:space:]]*`(commander|subcommander|executor)`[[:space:]]*\|[[:space:]]*([^|]+)[[:space:]]*\|[[:space:]]*([^|]+)[[:space:]]*\|[[:space:]]*$/\1|\2|\3/p'
  )"
  assert_eq $'commander\nexecutor\nsubcommander' "$(printf '%s\n' "$documented_rows" | cut -d'|' -f1 | sort)" \
    'role boundary table must contain exactly one row for each role'

  for role in commander subcommander executor; do
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

  roles_section="$(sed -n '/^## Roles$/,/^## Role-specific skills$/p' "$atct_skill")"
  if grep -Eiq 'space|worktree|git|harness|multiplexer' <<<"$roles_section"; then
    fail 'role boundary table must use neutral language'
  fi
}

test_role_boundaries_cover_version_control_and_delegation_direction() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local handler_go="$REPO_ROOT/internal/daemon/handler.go"

  assert_file_contains "delegate the goal's work" "$atct_skill"
  assert_file_contains "commit the goal's work" "$atct_skill"
  assert_file_contains "close a task its worker cannot" "$atct_skill"
  assert_file_contains 'write internal version-control details' "$atct_skill"
  assert_file_contains "delegate the goal's work" "$handler_go"
  assert_file_contains '"executor":     {Does: []string{"implement", "test", "close the task it was given"}, DoesNot: []string{"make design decisions", "re-delegate", "commit", "write internal version-control details"}}' "$handler_go"
}

test_executor_boundary_includes_task_closure() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains 'close the task it was given' "$atct_skill"
}

test_subcommander_boundary_includes_goal_commit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains "commit the goal's work" "$atct_skill"
}

test_commit_workflow_requires_explicit_paths() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains 'name the paths explicitly; never use `git add -A`' "$atct_skill"
}

test_executor_boundary_keeps_commit_exclusion() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  assert_file_contains '| `executor` | implement / test / close the task it was given | make design decisions / re-delegate / commit / write internal version-control details |' "$atct_skill"
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

  assert_file_contains 'Role values for `expected_role`: `commander`, `subcommander`, `executor`.' "$atct_skill"
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
    sed -nE 's/^[[:space:]]*"((commander|subcommander|executor))":[[:space:]]*\{[[:space:]]*Does:[[:space:]]*\[\]string\{([^}]*)\},[[:space:]]*DoesNot:[[:space:]]*\[\]string\{([^}]*)\},?[[:space:]]*\},?[[:space:]]*$/\1|\3|\4/p' "$handler_go" |
      sed -E 's/"//g; s/[[:space:]]*,[[:space:]]*/ \/ /g; s/[[:space:]]*\|[[:space:]]*/|/g; s/[[:space:]]+/ /g; s/^[[:space:]]+|[[:space:]]+$//g'
  )"
  documented_boundaries="$(
    sed -nE 's/^\|[[:space:]]*`(commander|subcommander|executor)`[[:space:]]*\|[[:space:]]*([^|]+)[[:space:]]*\|[[:space:]]*([^|]+)[[:space:]]*\|[[:space:]]*$/\1|\2|\3/p' "$atct_skill" |
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

test_task_close_uses_handoff_review() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local close_section

  close_section="$(sed -n '/^## Close a task the moment it is finished$/,/^## Keep going$/p' "$atct_skill")"
  grep -Fq -- 'atct_task_handoff_complete' <<<"$close_section" ||
    fail 'closing a task must use handoff review'
}

test_worktree_rule_defers_to_superpowers() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## Commit safely$/p' "$atct_skill")"
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
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## Commit safely$/p' "$atct_skill")"
  for role in commander subcommander executor; do
    grep -Fq -- "$role" <<<"$worktree_section" ||
      fail "worktree rule must name $role"
  done
  grep -Eiq -- 'executor.*(does not|never|must not) create' <<<"$worktree_section" ||
    fail 'worktree rule must say that the executor does not create worktrees'
}

test_worktree_rule_allows_the_primary_checkout() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## Commit safely$/p' "$atct_skill")"
  grep -Fq -- '### When the primary checkout is right' <<<"$worktree_section" ||
    fail 'worktree rule must name when the primary checkout is right'
  grep -Eiq -- 'commander.*review' <<<"$worktree_section" ||
    fail 'primary checkout exceptions must include commander review'
  grep -Eiq -- 'commander.*release' <<<"$worktree_section" ||
    fail 'primary checkout exceptions must include commander release'
}

test_worktree_rule_chooses_the_setup_script_over_a_native_tool() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## Commit safely$/p' "$atct_skill")"
  for term in script/worktree-setup.sh EnterWorktree web/node_modules web/dist; do
    grep -Fq -- "$term" <<<"$worktree_section" ||
      fail "worktree rule must mention $term"
  done
}

test_worktree_rule_lists_what_is_not_separated() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local worktree_section

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## Commit safely$/p' "$atct_skill")"
  grep -Fq -- '### What a worktree does not separate' <<<"$worktree_section" ||
    fail 'worktree rule must name what a worktree does not separate'
  grep -Fq -- '~/.atct/atct.db' <<<"$worktree_section" ||
    fail 'worktree rule must mention the shared ATCT database'
  grep -Fq -- 'daemon' <<<"$worktree_section" ||
    fail 'worktree rule must mention the shared daemon'
  grep -Fq -- 'GOCACHE' <<<"$worktree_section" || fail 'worktree rule must mention the shared Go build cache'
}

test_worktree_paths_match_the_setup_script() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local setup_script="$REPO_ROOT/script/worktree-setup.sh"
  local worktree_section
  local setup_worktree
  local setup_branch
  local documented_worktree
  local documented_branch

  worktree_section="$(sed -n '/^## One worktree per goal$/,/^## Commit safely$/p' "$atct_skill")"
  setup_worktree="$(sed -nE 's/^worktree="\$repo\/(.*)"/\1/p' "$setup_script")"
  setup_branch="$(sed -nE 's/^branch="(.*)"/\1/p' "$setup_script")"
  documented_worktree="$(sed 's/\${goal8}/<goal8>/g' <<<"$setup_worktree")"
  documented_branch="$(sed 's/\${goal8}/<goal8>/g' <<<"$setup_branch")"
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

  role_response="$(sed -n '/^func roleResponseFor(assignment roleAssignment) any {$/,/^}$/p' "$handler_go")"
  [[ -n "$role_response" ]] || fail 'role response function could not be extracted'

  selected_role="$(sed -nE 's/^[[:space:]]*boundary := roleBoundaries\[([^]]+)\][[:space:]]*$/\1/p' <<<"$role_response")"
  assert_eq 'assignment.Role' "$selected_role" 'role response must select the boundary for its current role'

  for response_type in commanderRole subcommanderRole executorRole; do
    response_block="$(sed -n "/return ${response_type}{/,/^[[:space:]]*}[[:space:]]*$/p" <<<"$role_response")"
    [[ -n "$response_block" ]] || fail "$response_type response block could not be extracted"
    grep -Fq -- 'Does:    boundary.Does' <<<"$response_block" ||
      grep -Fq -- 'Does:      boundary.Does' <<<"$response_block" ||
      fail "$response_type response must use the selected boundary's Does"
    grep -Fq -- 'DoesNot: boundary.DoesNot' <<<"$response_block" ||
      grep -Fq -- 'DoesNot:   boundary.DoesNot' <<<"$response_block" ||
      fail "$response_type response must use the selected boundary's DoesNot"
  done
}

test_skill_numbering_is_contiguous() {
  local skill
  local violations

  for skill in "$REPO_ROOT/skills/atct/SKILL.md" "$REPO_ROOT/skills/start/SKILL.md"; do
    violations="$(numbering_violations "$skill")"
    [[ -z "$violations" ]] || fail "numbered lists are broken:"$'\n'"$violations"
  done
}

test_ordered_sections_name_the_out_of_order_consequence() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local violations

  violations="$(out_of_order_violations "$atct_skill")"
  [[ -z "$violations" ]] || fail "numbered sections do not name the cost of running out of order:"$'\n'"$violations"
}

test_ordered_sections_are_numbered() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local heading
  local numbered

  for heading in "${ORDERED_SECTIONS[@]}"; do
    grep -Fxq -- "$heading" "$atct_skill" ||
      fail "<$atct_skill> has no section titled <$heading>"
    numbered="$(
      awk -v want="$heading" '
        $0 == want { inside = 1; next }
        /^## |^### / { inside = 0 }
        inside && /^[0-9]+\. / { found++ }
        END { print found + 0 }
      ' "$atct_skill"
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
test_start_explains_claim_recovery_boundary
test_start_explains_mcp_reconnect_gap
test_start_monitor_is_not_first_step
test_start_does_not_branch_on_session_attachment
test_start_does_not_duplicate_delegated_worker_preamble
test_start_keeps_monitor_persistence_requirement
test_start_documents_liveness_authority_boundary
test_start_uses_monitor_watch_for_claude_actions
test_readme_documents_liveness_contract
test_goal_handoff_watch_contract_is_explicit
test_goal_handoff_watch_contract_omits_unsafe_variants
test_goal_handoff_watch_contract_has_required_order
test_goal_handoff_forbids_upward_design_questions
test_goal_handoff_names_the_single_upward_message
test_goal_handoff_routes_cross_goal_facts_to_the_human
test_goal_handoff_silence_has_required_order
test_goal_handoff_preamble_does_not_invite_upward_reports
test_goal_delegation_requires_the_adjacent_goal_boundary
test_goal_delegation_keeps_the_delegator_out_until_completion
test_delegator_answers_are_balanced
test_delegator_answers_names_the_wrong_answers_measurement
test_unsent_report_table_covers_every_spoken_kind
test_unsent_report_names_the_stall_detection
test_task_delegation_preamble_is_untouched_by_upward_silence
test_task_handoff_recreation_cause_is_documented
test_task_handoff_recreation_uses_new_id
test_task_handoff_recreation_keeps_worker_identity
test_task_handoff_uses_canonical_review_order
test_executor_reuse_contract_is_explicit
test_executor_reuse_contract_preserves_idle_worker_and_reason_gated_pane
test_goal_handoff_cause_is_preserved
test_goal_completion_is_commander_owned
test_task_batch_record_context_reason_is_preserved
test_task_batch_measurement_is_preserved
test_role_contract_is_documented
test_role_response_exposes_boundary_fields
test_role_contract_uses_neutral_language
test_role_boundaries_cover_version_control_and_delegation_direction
test_executor_boundary_includes_task_closure
test_subcommander_boundary_includes_goal_commit
test_commit_workflow_requires_explicit_paths
test_executor_boundary_keeps_commit_exclusion
test_start_does_not_claim_delegate_cannot_close
test_start_does_not_claim_delegate_lacks_claim
test_role_table_has_no_task_update_procedure
test_role_contract_matches_implementation
test_task_close_uses_handoff_review
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
test_task_delegation_verification_contract_is_explicit
test_handoff_report_repair_is_explicit
test_handoff_completion_keeps_one_normal_path
test_delegation_names_the_atct_tools_an_executor_may_call
test_delegation_names_the_atct_tools_an_executor_must_not_call
test_delegation_requests_review_before_reviewer_closes_the_task
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
test_one_space_per_goal_section_exists
test_one_space_per_goal_binds_a_space_to_one_goal
test_one_space_per_goal_closes_on_approval
test_one_space_per_goal_forbids_reuse
test_one_space_per_goal_names_the_only_exception
test_one_space_per_goal_sits_between_worktree_and_commit
test_stop_hooks_share_server_check
test_claude_hooks_json_keeps_session_start_and_pre_tool_use_sections
test_stop_hook_file_is_executable_but_other_hooks_remain
test_mcp_instructions_include_active_goal_permission
test_mcp_instructions_delegate_human_decisions_to_the_skill
test_skill_numbering_is_contiguous
test_ordered_sections_name_the_out_of_order_consequence
test_ordered_sections_are_numbered
test_numbering_check_catches_a_broken_list
test_out_of_order_check_catches_a_missing_consequence
printf 'PASS wrapper tests\n'
