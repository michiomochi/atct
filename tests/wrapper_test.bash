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

write_fake_tools() {
  local fake_bin="$1"
  mkdir -p "$fake_bin"

  cat >"$fake_bin/curl" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

output=''
url=''
while (($# > 0)); do
  case "$1" in
    --output|-o)
      output="$2"
      shift 2
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

printf '%s\n' "$url" >>"$CURL_LOG"
if [[ "${CURL_FAIL:-0}" == 1 ]]; then
  printf 'network should not have been used\n' >&2
  exit 99
fi

name="${url##*/}"
cp "$FIXTURES_DIR/$name" "$output"
SCRIPT
  chmod +x "$fake_bin/curl"

  cat >"$fake_bin/uname" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  -s) printf '%s\n' "${FAKE_OS:-Darwin}" ;;
  -m) printf '%s\n' "${FAKE_ARCH:-arm64}" ;;
  *) printf '%s\n' "${FAKE_ARCH:-arm64}" ;;
esac
SCRIPT
  chmod +x "$fake_bin/uname"
}

wrapper_version() {
  sed -n 's/^VERSION="\([0-9][0-9.]*\)"/\1/p' "$REPO_ROOT/bin/_resolve" | head -1
}

make_archives() {
  local fixture_dir="$1"
  local checksum_value="$2"
  local version
  version="$(wrapper_version)"
  local atct_archive="$fixture_dir/atct_${version}_darwin_arm64.tar.gz"

  mkdir -p "$fixture_dir/payload"
  cat >"$fixture_dir/payload/atct" <<'SCRIPT'
#!/usr/bin/env bash
printf 'fake atct'
for arg in "$@"; do
  printf ' <%s>' "$arg"
done
printf '\n'
SCRIPT
  cat >"$fixture_dir/payload/atct-mcp" <<'SCRIPT'
#!/usr/bin/env bash
if [[ -n "${ATCT_ATCT_BIN_LOG:-}" ]]; then
  printf '%s' "${ATCT_ATCT_BIN:-}" >"$ATCT_ATCT_BIN_LOG"
fi
printf 'fake mcp\n' >&2
SCRIPT
  chmod +x "$fixture_dir/payload/atct" "$fixture_dir/payload/atct-mcp"

  tar -czf "$atct_archive" -C "$fixture_dir/payload" atct atct-mcp

  : >"$fixture_dir/checksums.txt"
  if [[ "$checksum_value" == bad ]]; then
    hash='0000000000000000000000000000000000000000000000000000000000000000'
  else
    hash="$(shasum -a 256 "$atct_archive" | awk '{print $1}')"
  fi
  printf '%s  %s\n' "$hash" "${atct_archive##*/}" >>"$fixture_dir/checksums.txt"
}

test_static_contract() {
  [[ -x "$REPO_ROOT/bin/atct" ]] || fail 'bin/atct is not executable'
  [[ -x "$REPO_ROOT/bin/atct-mcp" ]] || fail 'bin/atct-mcp is not executable'
  assert_file_contains '#!/usr/bin/env bash' "$REPO_ROOT/bin/atct"
  assert_file_contains 'set -euo pipefail' "$REPO_ROOT/bin/atct"
  assert_file_contains 'set -euo pipefail' "$REPO_ROOT/bin/atct-mcp"
  assert_file_contains '"type": "http"' "$REPO_ROOT/.mcp.json"
  assert_file_contains '"url": "http://127.0.0.1:8787/mcp"' "$REPO_ROOT/.mcp.json"
  if grep -Fq 'CLAUDE_PLUGIN_ROOT' "$REPO_ROOT/.mcp.json"; then
    fail '.mcp.json must not name CLAUDE_PLUGIN_ROOT'
  fi
  assert_file_contains '"source": "./"' "$REPO_ROOT/.claude-plugin"/marketplace.json
  # Pin the two version declarations to each other rather than to a literal, so a
  # release does not silently leave this test asserting the previous version.
  local plugin_version resolve_version
  plugin_version="$(sed -n 's/.*"version": "\([0-9][0-9.]*\)".*/\1/p' "$REPO_ROOT/.claude-plugin"/plugin.json | head -1)"
  resolve_version="$(sed -n 's/^VERSION="\([0-9][0-9.]*\)"/\1/p' "$REPO_ROOT/bin/_resolve" | head -1)"
  [[ -n "$plugin_version" ]] || fail 'plugin.json has no version'
  [[ -n "$resolve_version" ]] || fail '_resolve has no VERSION'
  assert_eq "$plugin_version" "$resolve_version" 'plugin.json and _resolve must declare the same version'
  assert_file_contains 'RELEASE_BASE="https://github.com/michiomochi/atct/releases/download/v${VERSION}"' "$REPO_ROOT/bin/_resolve"
  assert_file_contains 'ARCHIVE_NAME="atct_${VERSION}_${OS}_${ARCH}.tar.gz"' "$REPO_ROOT/bin/_resolve"
  [[ -f "$REPO_ROOT/.mcp.json" ]] || fail 'repository root must contain .mcp.json'
  if grep -Fq 'latest' "$REPO_ROOT/bin/_resolve"; then
    fail 'wrapper must not use the latest release'
  fi
}

test_stop_hook_only_reports() {
  if python3 - "$REPO_ROOT/hooks/hooks.json" <<'PY'
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
    raise SystemExit(f"Stop hook registration is not report-only: {stop!r}")
PY
  then
    :
  else
    fail 'hooks.json must register only the report-only Stop hook'
  fi

  local fixture="$TEMP_ROOT/stop-hook"
  local hook="$fixture/hooks/stop"
  local adjacent="$fixture/bin/atct"
  local log="$fixture/atct.log"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")"
  cp "$REPO_ROOT/hooks/stop" "$hook"
  cat >"$adjacent" <<'SCRIPT'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$ATCT_STOP_LOG"
if [[ "${ATCT_STOP_FAIL:-0}" == 1 ]]; then
  exit 1
fi
SCRIPT
  chmod +x "$hook" "$adjacent"

  output="$(ATCT_STOP_LOG="$log" ATCT_TASK_ID=task-1 /bin/bash "$hook" <<< '{"stop_hook_active": false}')"
  assert_eq '' "$output" 'Stop hook must not print the yielded event'
  assert_eq 'handoff yielded task-1' "$(<"$log")" 'Stop hook must only yield the task'

  : >"$log"
  output="$(ATCT_STOP_LOG="$log" ATCT_TASK_ID=task-1 ATCT_STOP_FAIL=1 /bin/bash "$hook" <<< '{"stop_hook_active": false}')"
  assert_eq '' "$output" 'Stop hook must stay silent when the CLI fails'
  assert_eq 'handoff yielded task-1' "$(<"$log")" 'Stop hook must not run other commands on CLI failure'

  : >"$log"
  output="$(ATCT_STOP_LOG="$log" ATCT_TASK_ID=task-1 /bin/bash "$hook" <<< '{"stop_hook_active": true}')"
  assert_eq '' "$output" 'active Stop hook must be ignored'
  assert_empty_file "$log"

  : >"$log"
  output="$(ATCT_STOP_LOG="$log" ATCT_TASK_ID= /bin/bash "$hook" <<< '{}')"
  assert_eq '' "$output" 'Stop hook without ATCT_TASK_ID must be silent'
  assert_empty_file "$log"
}

test_hooks_json_keeps_session_start_and_pre_tool_use_sections() {
  if python3 - "$REPO_ROOT/hooks/hooks.json" <<'PY'
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
  fail 'hooks.json must keep SessionStart and PreToolUse'
}

test_stop_hook_file_is_executable_but_other_hooks_remain() {
  [[ -x "$REPO_ROOT/hooks/stop" ]] || fail 'hooks/stop must be executable'
  [[ -f "$REPO_ROOT/hooks/pre-ask" ]] || fail 'hooks/pre-ask must remain'
  [[ -f "$REPO_ROOT/hooks/session-start" ]] || fail 'hooks/session-start must remain'
}

test_download_cache_and_mcp_stdout() {
  local fixtures="$TEMP_ROOT/fixtures"
  local fake_bin="$TEMP_ROOT/fake-bin"
  local home="$TEMP_ROOT/home"
  local curl_log="$TEMP_ROOT/curl.log"
  local mcp_stdout="$TEMP_ROOT/mcp.stdout"
  local mcp_stderr="$TEMP_ROOT/mcp.stderr"
  local atct_wrapper_log="$TEMP_ROOT/atct-wrapper.log"
  local first_out
  local before
  local after

  mkdir -p "$home"
  : >"$curl_log"
  make_archives "$fixtures" good
  write_fake_tools "$fake_bin"

  first_out="$(HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/bin/atct" project list)"
  assert_eq 'fake atct <project> <list>' "$first_out" 'first wrapper execution'
  local version
  version="$(wrapper_version)"
  assert_file_contains "https://github.com/michiomochi/atct/releases/download/v${version}/atct_${version}_darwin_arm64.tar.gz" "$curl_log"
  assert_file_contains "https://github.com/michiomochi/atct/releases/download/v${version}/checksums.txt" "$curl_log"
  [[ -x "$home/.atct/bin/atct-${version}" ]] || fail 'versioned atct cache is missing'
  for candidate in "$home/.atct/bin"/.download.*; do
    [[ ! -e "$candidate" ]] || fail "download directory remained after success: $candidate"
  done

  HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" ATCT_ATCT_BIN_LOG="$atct_wrapper_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/bin/atct-mcp" </dev/null >"$mcp_stdout" 2>"$mcp_stderr"
  assert_empty_file "$mcp_stdout"
  assert_file_contains 'fake mcp' "$mcp_stderr"
  assert_eq "$REPO_ROOT/bin/atct" "$(<"$atct_wrapper_log")" 'MCP wrapper must select the matching atct wrapper'
  [[ -x "$home/.atct/bin/atct-mcp-$(wrapper_version)" ]] || fail 'versioned atct-mcp cache is missing'

  mkdir -p "$home/.atct/bin/.download.stale"
  printf 'stale\n' >"$home/.atct/bin/.download.stale/file"
  touch -t 200001010000 "$home/.atct/bin/.download.stale"
  mkdir -p "$home/.atct/bin/.download.active"
  printf 'active\n' >"$home/.atct/bin/.download.active/file"
  printf 'database\n' >"$home/.atct/atct.db"
  first_out="$(HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" CURL_FAIL=1 FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/bin/atct" project list)"
  assert_eq 'fake atct <project> <list>' "$first_out" 'cached execution after stale cleanup'
  [[ -e "$home/.atct/bin/.download.active/file" ]] || fail 'just-created download directory was removed'
  [[ ! -e "$home/.atct/bin/.download.stale" ]] || fail 'stale download directory was not removed'
  assert_file_contains 'database' "$home/.atct/atct.db"

  before="$(wc -l <"$curl_log" | tr -d ' ')"
  first_out="$(HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" CURL_FAIL=1 FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/bin/atct" project list)"
  after="$(wc -l <"$curl_log" | tr -d ' ')"
  assert_eq "$before" "$after" 'cached execution must not use the network'
  assert_eq 'fake atct <project> <list>' "$first_out" 'cached wrapper execution'

  HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" CURL_FAIL=1 FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/bin/atct-mcp" </dev/null >"$mcp_stdout" 2>"$mcp_stderr"
  assert_empty_file "$mcp_stdout"
  assert_file_contains 'fake mcp' "$mcp_stderr"
  assert_eq 4 "$(wc -l <"$curl_log" | tr -d ' ')" 'first executions should download archive and checksums once per binary'
}

test_context_check_preserves_exit_code() {
  local fixtures="$TEMP_ROOT/context-check-fixtures"
  local fake_bin="$TEMP_ROOT/context-check-fake-bin"
  local home="$TEMP_ROOT/context-check-home"
  local curl_log="$TEMP_ROOT/context-check-curl.log"
  local stdout="$TEMP_ROOT/context-check.stdout"
  local stderr="$TEMP_ROOT/context-check.stderr"
  local archive="$fixtures/atct_$(wrapper_version)_darwin_arm64.tar.gz"
  local checksum
  local status=0

  mkdir -p "$fixtures/payload" "$home"
  cat >"$fixtures/payload/atct" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == context && "${2:-}" == --check ]]; then
  exit 1
fi
exit 0
SCRIPT
  cat >"$fixtures/payload/atct-mcp" <<'SCRIPT'
#!/usr/bin/env bash
exit 0
SCRIPT
  chmod +x "$fixtures/payload/atct" "$fixtures/payload/atct-mcp"
  tar -czf "$archive" -C "$fixtures/payload" atct atct-mcp
  checksum="$(shasum -a 256 "$archive" | awk '{print $1}')"
  printf '%s  %s\n' "$checksum" "${archive##*/}" >"$fixtures/checksums.txt"
  : >"$curl_log"
  write_fake_tools "$fake_bin"

  HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 \
    "$REPO_ROOT/bin/atct" context --check >"$stdout" 2>"$stderr" || status=$?

  assert_eq 1 "$status" 'context --check exit status must pass through the shell wrapper'
  assert_empty_file "$stdout"
  assert_empty_file "$stderr"
}

test_cleanup_failure_is_best_effort() {
  local home="$TEMP_ROOT/cleanup-failure-home"
  local fake_bin="$TEMP_ROOT/cleanup-failure-fake-bin"
  local rm_log="$TEMP_ROOT/cleanup-failure-rm.log"
  local stale_dir="$home/.atct/bin/.download.unremovable"
  local output

  mkdir -p "$home/.atct/bin" "$fake_bin"
  cat >"$home/.atct/bin/atct-$(wrapper_version)" <<'SCRIPT'
#!/usr/bin/env bash
printf 'cached after cleanup failure\n'
SCRIPT
  chmod +x "$home/.atct/bin/atct-$(wrapper_version)"

  mkdir -p "$stale_dir"
  printf 'stale\n' >"$stale_dir/file"
  touch -t 200001010000 "$stale_dir"

  cat >"$fake_bin/rm" <<'SCRIPT'
#!/usr/bin/env bash
printf 'rm invoked\n' >>"$RM_LOG"
exit 1
SCRIPT
  chmod +x "$fake_bin/rm"

  if ! output="$(HOME="$home" PATH="$fake_bin:$PATH" RM_LOG="$rm_log" "$REPO_ROOT/bin/atct" project list)"; then
    fail 'cleanup failure prevented cached execution'
  fi
  assert_eq 'cached after cleanup failure' "$output" 'cached execution after cleanup failure'
  assert_file_contains 'rm invoked' "$rm_log"
}

test_checksum_failure() {
  local fixtures="$TEMP_ROOT/bad-fixtures"
  local fake_bin="$TEMP_ROOT/bad-fake-bin"
  local home="$TEMP_ROOT/bad-home"
  local curl_log="$TEMP_ROOT/bad-curl.log"
  local stdout="$TEMP_ROOT/bad.stdout"
  local stderr="$TEMP_ROOT/bad.stderr"

  mkdir -p "$home"
  : >"$curl_log"
  make_archives "$fixtures" bad
  write_fake_tools "$fake_bin"

  if HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/bin/atct" project list >"$stdout" 2>"$stderr"; then
    fail 'checksum mismatch unexpectedly succeeded'
  fi
  assert_empty_file "$stdout"
  assert_file_contains 'Checksum verification failed' "$stderr"
  [[ ! -e "$home/.atct/bin/atct-$(wrapper_version)" ]] || fail 'checksum mismatch left an executable cache'
  for candidate in "$home/.atct/bin"/.download.*; do
    [[ ! -e "$candidate" ]] || fail "download directory remained after failure: $candidate"
  done
}

test_missing_checksum_tool_fails() {
  local fixtures="$TEMP_ROOT/no-checksum-fixtures"
  local fake_bin="$TEMP_ROOT/no-checksum-fake-bin"
  local restricted_path="$TEMP_ROOT/restricted-path"
  local home="$TEMP_ROOT/no-checksum-home"
  local curl_log="$TEMP_ROOT/no-checksum-curl.log"
  local stderr="$TEMP_ROOT/no-checksum.stderr"
  local tool

  mkdir -p "$home" "$restricted_path"
  : >"$curl_log"
  make_archives "$fixtures" good
  write_fake_tools "$fake_bin"

  for tool in bash env dirname mkdir mktemp rm tar chmod mv awk tr cp; do
    ln -s "$(command -v "$tool")" "$restricted_path/$tool"
  done
  ln -s "$fake_bin/curl" "$restricted_path/curl"
  ln -s "$fake_bin/uname" "$restricted_path/uname"

  if HOME="$home" PATH="$restricted_path" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/bin/atct" project list >/dev/null 2>"$stderr"; then
    fail 'missing checksum tool unexpectedly succeeded'
  fi
  assert_file_contains 'shasum or sha256sum' "$stderr"
}

test_unsupported_platform_fails() {
  local fixtures="$TEMP_ROOT/unsupported-fixtures"
  local fake_bin="$TEMP_ROOT/unsupported-fake-bin"
  local home="$TEMP_ROOT/unsupported-home"
  local curl_log="$TEMP_ROOT/unsupported-curl.log"
  local stderr="$TEMP_ROOT/unsupported.stderr"

  mkdir -p "$home"
  : >"$curl_log"
  make_archives "$fixtures" good
  write_fake_tools "$fake_bin"

  if HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=FreeBSD FAKE_ARCH=arm64 "$REPO_ROOT/bin/atct" project list >/dev/null 2>"$stderr"; then
    fail 'unsupported platform unexpectedly succeeded'
  fi
  assert_file_contains 'Unsupported platform' "$stderr"
  assert_empty_file "$curl_log"
}

test_session_start_uses_adjacent_context_wrapper() {
  local fixture="$TEMP_ROOT/session-start-path"
  local hook="$fixture/hooks/session-start"
  local adjacent="$fixture/bin/atct"
  local path_atct="$fixture/path/atct"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")" "$(dirname "$path_atct")"
  cp "$REPO_ROOT/hooks/session-start" "$hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
if [[ "${1:-}" == context ]]; then
  printf 'adjacent context\n'
fi
SCRIPT
  cat >"$path_atct" <<'SCRIPT'
#!/bin/bash
printf 'PATH context\n'
SCRIPT
  chmod +x "$adjacent" "$path_atct"

  output="$(PATH="" /bin/bash "$hook")"
  [[ "$output" == adjacent\ context* ]] || fail 'session-start did not prepend adjacent atct context'
  [[ "$output" != *'PATH context'* ]] || fail 'session-start resolved atct from PATH'
  if grep -Fq 'command -v atct' "$hook" || grep -Fq 'jq' "$hook"; then
    fail 'session-start still depends on PATH lookup or jq'
  fi
}

test_session_start_preserves_context_and_silence() {
  local fixture="$TEMP_ROOT/session-start-output"
  local hook="$fixture/hooks/session-start"
  local adjacent="$fixture/bin/atct"
  local no_wrapper_hook="$fixture/no-wrapper/hooks/session-start"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")" "$(dirname "$no_wrapper_hook")"
  cp "$REPO_ROOT/hooks/session-start" "$hook"
  cp "$REPO_ROOT/hooks/session-start" "$no_wrapper_hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
if [[ "${1:-}" == context && -n "${FAKE_CONTEXT:-}" ]]; then
  printf '%s\n' "$FAKE_CONTEXT"
fi
SCRIPT
  chmod +x "$adjacent"

  output="$(FAKE_CONTEXT='hook context' PATH="/usr/bin:/bin" bash "$hook")"
  [[ "$output" == hook\ context* ]] || fail 'context was not printed before the boilerplate'
  [[ "$output" != *'An active goal is permission to work.'* ]] || fail 'fixed instructions leaked into the session-start hook'
  [[ "$output" != *'Stop only before what cannot be undone:'* ]] || fail 'fixed instructions leaked into the session-start hook'
  [[ "$output" != *'See the `atct` skill for details.'* ]] || fail 'fixed instructions leaked into the session-start hook'
  assert_file_contains 'An active goal is permission to work.' "$REPO_ROOT/internal/mcpshim/instructions.go"
  assert_file_contains 'Stop only before what cannot be undone:' "$REPO_ROOT/internal/mcpshim/instructions.go"
  assert_file_contains 'See the `atct` skill for details.' "$REPO_ROOT/internal/mcpshim/instructions.go"

  output="$(PATH="/usr/bin:/bin" bash "$hook")"
  assert_eq '' "$output" 'empty context must keep the hook silent'

  output="$(PATH="" /bin/bash "$no_wrapper_hook")"
  assert_eq '' "$output" 'missing atct must keep the hook silent'
}

test_mcp_instructions_include_active_goal_permission() {
  assert_file_contains 'An active goal is permission to work.' "$REPO_ROOT/internal/mcpshim/instructions.go"
}

test_mcp_instructions_include_undo_boundary() {
  assert_file_contains 'Stop only before what cannot be undone:' "$REPO_ROOT/internal/mcpshim/instructions.go"
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
  delegate_goal_section_contains 'Then attach `atct watch -goal <goal_id>` to a background stream the way'
  delegate_goal_section_contains 'Pass no other goal; a subcommander must not inspect other goals.'
  delegate_goal_section_contains 'Codex has no Monitor, so a Codex reader'
}

test_goal_handoff_watch_contract_omits_unsafe_variants() {
  delegate_goal_section_not_contains 'Then attach `atct watch` to a background stream'
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
  watch="$(lineno 'Then attach `atct watch -goal <goal_id>` to a background stream the way')"
  fin="$(lineno 'When the work is complete, record completion by calling')"

  [[ -n "$role" && -n "$watch" && -n "$fin" ]] ||
    fail 'goal handoff watch order requires role, watch, and completion paragraphs'
  (( role < watch && watch < fin )) ||
    fail "goal handoff watch paragraphs are in the wrong order: role=$role watch=$watch completion=$fin"
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
  recovery_section_contains '- goal: `atct_goal_handoff_complete` → `atct_goal_handoff_request` (the commander must issue the handoff again)'
  recovery_section_contains 'A subcommander cannot restore its own goal; ask the commander to issue the goal handoff again'
}

test_recovery_section_has_task_path_and_non_repair_note() {
  recovery_section_contains '- task: `atct_handoff_complete` (with only `task_id`) → `atct_task_claim`'
  recovery_section_contains 'This is a procedure, not a repair; it becomes unnecessary once the issue is fixed.'
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

test_delegated_claim_contract_is_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'Hold the parent, not the task.' "$atct_skill"
  assert_file_contains 'Hold the parent, not the goal.' "$atct_skill"
  assert_file_contains '## Delegate a task' "$atct_skill"
  assert_file_contains '## Delegate a goal' "$atct_skill"
  assert_file_contains 'Then record receipt of the handoff by calling `atct_handoff_receive` with only' "$atct_skill"
  assert_file_contains 'Then record receipt of the goal handoff by calling' "$atct_skill"
  assert_file_contains 'A delegated worker owns the task it was given.' "$atct_skill"
  assert_file_contains 'Delegating a task requires a received goal handoff, not a project claim.' "$atct_skill"
  assert_file_contains 'For two-layer delegation, the commander calls `atct_goal_claim` to create a goal handoff addressed to itself.' "$atct_skill"
  assert_file_contains 'a received, uncompleted goal handoff' "$atct_skill"
  assert_file_contains 'A delegated worker receives the task with `atct_handoff_receive`' "$start_skill"
  assert_file_contains 'The following loop is for self-directed work: find and take a task yourself.' "$start_skill"
  assert_file_contains 'Record the handoff before waking the worker.' "$atct_skill"
  assert_file_contains 'The delegator must call `atct_handoff_request`' "$atct_skill"
  assert_file_contains 'the `task_id` provided in this request.' "$atct_skill"
  assert_file_contains 'The delegator must call `atct_goal_handoff_request`' "$atct_skill"
  assert_file_contains 'with only the `goal_id` provided in this request.' "$atct_skill"
  assert_file_contains '`atct_goal_handoff_complete` with only the `goal_id` provided in this' "$atct_skill"
  assert_file_contains 'Do this before starting work.' "$atct_skill"
  assert_file_contains 'When the work is complete, record completion by calling `atct_handoff_complete`' "$atct_skill"
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
  assert_file_not_contains 'atct_goal_handoff_receive` with only the `handoff_id` provided in this request.' "$atct_skill"
}

test_declared_task_content_fix_contract_is_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local task_section

  task_section="$(sed -n '/^## Fix a declared task$/,/^## Claim before you start$/p' "$atct_skill")"
  grep -Fq -- 'atct_task_update_content' <<<"$task_section" ||
    fail 'declared task content fix section omits atct_task_update_content'
  grep -Fq -- 'todo' <<<"$task_section" ||
    fail 'declared task content fix section omits todo'
  grep -Fq -- 'done' <<<"$task_section" ||
    fail 'declared task content fix section omits done'
  grep -Fq -- 'idempotency_key' <<<"$task_section" ||
    fail 'declared task content fix section omits idempotency_key'
}

test_decision_guidance_names_done_guard() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local decision_section

  decision_section="$(sed -n '/^## Act on reversible choices, ask about irreversible ones$/,/^## Apply what you were told$/p' "$atct_skill")"
  grep -Fq -- 'default_after_ms=0' <<<"$decision_section" ||
    fail 'decision guidance omits immediate record defaults'
  grep -Fq -- 'blocks `done`' <<<"$decision_section" ||
    fail 'decision guidance omits the done guard for human-waiting questions'
}

test_irreversible_decision_still_omits_defaults() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"

  grep -Fq -- 'omit `default_option`' "$atct_skill" ||
    fail 'irreversible decision guidance no longer omits default_option'
}

test_start_identifies_before_monitor() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"
  local identify_line
  local monitor_line

  identify_line="$(grep -n '^## First step: identify' "$start_skill" | head -1 | cut -d: -f1)"
  monitor_line="$(grep -n '^## .*Claude Code Monitor' "$start_skill" | head -1 | cut -d: -f1)"
  [[ -n "$identify_line" && -n "$monitor_line" ]] ||
    fail 'start order requires identify and monitor headings'
  (( identify_line < monitor_line )) ||
    fail "session identification must precede Monitor: identify=$identify_line monitor=$monitor_line"
}

test_start_session_key_contract_is_explicit() {
  local start_skill="$REPO_ROOT/skills/start/SKILL.md"

  assert_file_contains 'atct_session_identify` with `session_key`' "$start_skill"
  assert_file_contains '<project>-<unit>-<role>' "$start_skill"
  assert_file_contains 'rather than only the role' "$start_skill"
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

test_task_handoff_recreation_cause_is_documented() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local task_section

  task_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$atct_skill")"
  grep -Fq -- 'Claiming' <<<"$task_section" ||
    fail 'task delegation section omits the claim-to-handoff refusal cause'
  grep -Fq -- 'task first always' <<<"$task_section" ||
    fail 'task delegation section omits the claim-to-handoff refusal cause'
  grep -Fq -- 'already writes an open handoff' <<<"$task_section" ||
    fail 'task delegation section omits the open handoff cause'
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

  roles_section="$(sed -n '/^## Roles$/,/^## Declare before you work$/p' "$atct_skill")"
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

test_role_response_does_not_leak_other_boundaries() {
  local handler_go="$REPO_ROOT/internal/daemon/handler.go"
  local selected_role

  selected_role="$(sed -nE 's/^[[:space:]]*boundary := roleBoundaries\[([^]]+)\][[:space:]]*$/\1/p' "$handler_go")"
  assert_eq 'response.Role' "$selected_role" 'role response must select the boundary for its current role'
  assert_file_contains 'response.Does = boundary.Does' "$handler_go"
  assert_file_contains 'response.DoesNot = boundary.DoesNot' "$handler_go"
}

test_static_contract
test_delegated_claim_contract_is_explicit
test_declared_task_content_fix_contract_is_explicit
test_decision_guidance_names_done_guard
test_irreversible_decision_still_omits_defaults
test_start_identifies_before_monitor
test_start_session_key_contract_is_explicit
test_start_explains_claim_recovery_boundary
test_start_explains_mcp_reconnect_gap
test_start_monitor_is_not_first_step
test_start_does_not_branch_on_session_attachment
test_start_does_not_duplicate_delegated_worker_preamble
test_start_keeps_monitor_persistence_requirement
test_goal_handoff_watch_contract_is_explicit
test_goal_handoff_watch_contract_omits_unsafe_variants
test_goal_handoff_watch_contract_has_required_order
test_task_handoff_recreation_cause_is_documented
test_task_handoff_recreation_uses_new_id
test_task_handoff_recreation_keeps_worker_identity
test_goal_handoff_cause_is_preserved
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
test_role_response_does_not_leak_other_boundaries
test_recovery_section_has_role_entry
test_recovery_section_prioritizes_session_identify
test_recovery_section_has_project_path
test_recovery_section_has_goal_path
test_recovery_section_has_task_path_and_non_repair_note
test_recovery_section_omits_session_header
test_recovery_section_omits_agent_sessions
test_recovery_section_omits_task_release
test_recovery_section_omits_task_update
test_recovery_section_omits_goal_release
test_recovery_section_names_existing_tools
test_stop_hook_only_reports
test_hooks_json_keeps_session_start_and_pre_tool_use_sections
test_stop_hook_file_is_executable_but_other_hooks_remain
test_download_cache_and_mcp_stdout
test_context_check_preserves_exit_code
test_cleanup_failure_is_best_effort
test_checksum_failure
test_missing_checksum_tool_fails
test_unsupported_platform_fails
test_session_start_uses_adjacent_context_wrapper
test_session_start_preserves_context_and_silence
test_mcp_instructions_include_active_goal_permission
test_mcp_instructions_include_undo_boundary
test_session_start_is_silent_without_atct_wrapper
printf 'PASS wrapper tests\n'
