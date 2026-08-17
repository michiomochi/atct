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

make_archives() {
  local fixture_dir="$1"
  local checksum_value="$2"
  local atct_archive="$fixture_dir/atct_0.7.0_darwin_arm64.tar.gz"

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
  [[ -x "$REPO_ROOT/plugin/bin/atct" ]] || fail 'plugin/bin/atct is not executable'
  [[ -x "$REPO_ROOT/plugin/bin/atct-mcp" ]] || fail 'plugin/bin/atct-mcp is not executable'
  assert_file_contains '#!/usr/bin/env bash' "$REPO_ROOT/plugin/bin/atct"
  assert_file_contains 'set -euo pipefail' "$REPO_ROOT/plugin/bin/atct"
  assert_file_contains 'set -euo pipefail' "$REPO_ROOT/plugin/bin/atct-mcp"
  assert_file_contains '"command": "${CLAUDE_PLUGIN_ROOT}/bin/atct-mcp"' "$REPO_ROOT/plugin/.mcp.json"
  assert_file_contains '"source": "./plugin"' "$REPO_ROOT/.claude-plugin/marketplace.json"
  assert_file_contains '"version": "0.7.0"' "$REPO_ROOT/plugin/.claude-plugin/plugin.json"
  assert_file_contains 'VERSION="0.7.0"' "$REPO_ROOT/plugin/bin/_resolve"
  assert_file_contains 'RELEASE_BASE="https://github.com/michiomochi/atct/releases/download/v${VERSION}"' "$REPO_ROOT/plugin/bin/_resolve"
  assert_file_contains 'ARCHIVE_NAME="atct_${VERSION}_${OS}_${ARCH}.tar.gz"' "$REPO_ROOT/plugin/bin/_resolve"
  [[ ! -e "$REPO_ROOT/.mcp.json" ]] || fail 'repository root must not contain .mcp.json'
  if grep -Fq 'latest' "$REPO_ROOT/plugin/bin/_resolve"; then
    fail 'wrapper must not use the latest release'
  fi
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

  first_out="$(HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct" project list)"
  assert_eq 'fake atct <project> <list>' "$first_out" 'first wrapper execution'
  assert_file_contains 'https://github.com/michiomochi/atct/releases/download/v0.7.0/atct_0.7.0_darwin_arm64.tar.gz' "$curl_log"
  assert_file_contains 'https://github.com/michiomochi/atct/releases/download/v0.7.0/checksums.txt' "$curl_log"
  [[ -x "$home/.atct/bin/atct-0.7.0" ]] || fail 'versioned atct cache is missing'
  for candidate in "$home/.atct/bin"/.download.*; do
    [[ ! -e "$candidate" ]] || fail "download directory remained after success: $candidate"
  done

  HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" ATCT_ATCT_BIN_LOG="$atct_wrapper_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct-mcp" </dev/null >"$mcp_stdout" 2>"$mcp_stderr"
  assert_empty_file "$mcp_stdout"
  assert_file_contains 'fake mcp' "$mcp_stderr"
  assert_eq "$REPO_ROOT/plugin/bin/atct" "$(<"$atct_wrapper_log")" 'MCP wrapper must select the matching atct wrapper'
  [[ -x "$home/.atct/bin/atct-mcp-0.7.0" ]] || fail 'versioned atct-mcp cache is missing'

  mkdir -p "$home/.atct/bin/.download.stale"
  printf 'stale\n' >"$home/.atct/bin/.download.stale/file"
  touch -t 200001010000 "$home/.atct/bin/.download.stale"
  mkdir -p "$home/.atct/bin/.download.active"
  printf 'active\n' >"$home/.atct/bin/.download.active/file"
  printf 'database\n' >"$home/.atct/atct.db"
  first_out="$(HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" CURL_FAIL=1 FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct" project list)"
  assert_eq 'fake atct <project> <list>' "$first_out" 'cached execution after stale cleanup'
  [[ -e "$home/.atct/bin/.download.active/file" ]] || fail 'just-created download directory was removed'
  [[ ! -e "$home/.atct/bin/.download.stale" ]] || fail 'stale download directory was not removed'
  assert_file_contains 'database' "$home/.atct/atct.db"

  before="$(wc -l <"$curl_log" | tr -d ' ')"
  first_out="$(HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" CURL_FAIL=1 FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct" project list)"
  after="$(wc -l <"$curl_log" | tr -d ' ')"
  assert_eq "$before" "$after" 'cached execution must not use the network'
  assert_eq 'fake atct <project> <list>' "$first_out" 'cached wrapper execution'

  HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" CURL_FAIL=1 FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct-mcp" </dev/null >"$mcp_stdout" 2>"$mcp_stderr"
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
  local archive="$fixtures/atct_0.7.0_darwin_arm64.tar.gz"
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
    "$REPO_ROOT/plugin/bin/atct" context --check >"$stdout" 2>"$stderr" || status=$?

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
  cat >"$home/.atct/bin/atct-0.7.0" <<'SCRIPT'
#!/usr/bin/env bash
printf 'cached after cleanup failure\n'
SCRIPT
  chmod +x "$home/.atct/bin/atct-0.7.0"

  mkdir -p "$stale_dir"
  printf 'stale\n' >"$stale_dir/file"
  touch -t 200001010000 "$stale_dir"

  cat >"$fake_bin/rm" <<'SCRIPT'
#!/usr/bin/env bash
printf 'rm invoked\n' >>"$RM_LOG"
exit 1
SCRIPT
  chmod +x "$fake_bin/rm"

  if ! output="$(HOME="$home" PATH="$fake_bin:$PATH" RM_LOG="$rm_log" "$REPO_ROOT/plugin/bin/atct" project list)"; then
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

  if HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct" project list >"$stdout" 2>"$stderr"; then
    fail 'checksum mismatch unexpectedly succeeded'
  fi
  assert_empty_file "$stdout"
  assert_file_contains 'Checksum verification failed' "$stderr"
  [[ ! -e "$home/.atct/bin/atct-0.7.0" ]] || fail 'checksum mismatch left an executable cache'
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

  if HOME="$home" PATH="$restricted_path" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct" project list >/dev/null 2>"$stderr"; then
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

  if HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=FreeBSD FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct" project list >/dev/null 2>"$stderr"; then
    fail 'unsupported platform unexpectedly succeeded'
  fi
  assert_file_contains 'Unsupported platform' "$stderr"
  assert_empty_file "$curl_log"
}

test_session_start_uses_adjacent_context_wrapper() {
  local fixture="$TEMP_ROOT/session-start-path"
  local hook="$fixture/plugin/hooks/session-start"
  local adjacent="$fixture/plugin/bin/atct"
  local path_atct="$fixture/path/atct"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")" "$(dirname "$path_atct")"
  cp "$REPO_ROOT/plugin/hooks/session-start" "$hook"
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
  local hook="$fixture/plugin/hooks/session-start"
  local adjacent="$fixture/plugin/bin/atct"
  local no_wrapper_hook="$fixture/no-wrapper/plugin/hooks/session-start"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")" "$(dirname "$no_wrapper_hook")"
  cp "$REPO_ROOT/plugin/hooks/session-start" "$hook"
  cp "$REPO_ROOT/plugin/hooks/session-start" "$no_wrapper_hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
if [[ "${1:-}" == context && -n "${FAKE_CONTEXT:-}" ]]; then
  printf '%s\n' "$FAKE_CONTEXT"
fi
SCRIPT
  chmod +x "$adjacent"

  output="$(FAKE_CONTEXT='hook context' PATH="/usr/bin:/bin" bash "$hook")"
  [[ "$output" == hook\ context* ]] || fail 'context was not printed before the boilerplate'
  [[ "$output" == *'An active goal is permission to work.'* ]] || fail 'active-goal permission guidance was removed'
  [[ "$output" == *'Stop only before what cannot be undone:'* ]] || fail 'irreversible-change guidance was removed'
  [[ "$output" == *'See the `atct` skill for details.'* ]] || fail 'existing boilerplate was changed'

  output="$(PATH="/usr/bin:/bin" bash "$hook")"
  assert_eq '' "$output" 'empty context must keep the hook silent'

  output="$(PATH="" /bin/bash "$no_wrapper_hook")"
  assert_eq '' "$output" 'missing atct must keep the hook silent'
}

test_session_start_mentions_active_goal_permission() {
  local fixture="$TEMP_ROOT/session-start-active-goal"
  local hook="$fixture/plugin/hooks/session-start"
  local adjacent="$fixture/plugin/bin/atct"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")"
  cp "$REPO_ROOT/plugin/hooks/session-start" "$hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
if [[ "${1:-}" == context ]]; then
  printf 'goal context\n'
fi
SCRIPT
  chmod +x "$adjacent"

  output="$(PATH="" /bin/bash "$hook")"
  [[ "$output" == *'An active goal is permission to work.'* ]] || fail 'hook omitted active-goal permission guidance'
}

test_session_start_mentions_undo_boundary() {
  local fixture="$TEMP_ROOT/session-start-undo-boundary"
  local hook="$fixture/plugin/hooks/session-start"
  local adjacent="$fixture/plugin/bin/atct"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")"
  cp "$REPO_ROOT/plugin/hooks/session-start" "$hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
if [[ "${1:-}" == context ]]; then
  printf 'goal context\n'
fi
SCRIPT
  chmod +x "$adjacent"

  output="$(PATH="" /bin/bash "$hook")"
  [[ "$output" == *'cannot be undone'* ]] || fail 'hook omitted cannot-be-undone boundary'
}

test_session_start_is_silent_without_atct_wrapper() {
  local fixture="$TEMP_ROOT/session-start-no-wrapper"
  local hook="$fixture/plugin/hooks/session-start"
  local output

  mkdir -p "$(dirname "$hook")"
  cp "$REPO_ROOT/plugin/hooks/session-start" "$hook"

  output="$(PATH="" /bin/bash "$hook" 2>&1)" || fail 'hook failed without an atct wrapper'
  assert_eq '' "$output" 'missing atct wrapper must keep the hook silent'
}

test_stop_hook_active_is_silent() {
  local fixture="$TEMP_ROOT/stop-hook-active"
  local hook="$fixture/plugin/hooks/stop"
  local adjacent="$fixture/plugin/bin/atct"
  local marker="$fixture/marker"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")"
  cp "$REPO_ROOT/plugin/hooks/stop" "$hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
printf 'called\n' >"$MARKER"
SCRIPT
  chmod +x "$adjacent"

  if ! output="$(MARKER="$marker" PATH="" /bin/bash "$hook" <<< '{"stop_hook_active":true}' 2>&1)"; then
    fail 'stop hook failed while stop_hook_active was true'
  fi
  assert_eq '' "$output" 'stop_hook_active must keep the hook silent'
  [[ ! -e "$marker" ]] || fail 'stop_hook_active must prevent wrapper execution'
}

test_stop_hook_is_silent_without_adjacent_wrapper() {
  local fixture="$TEMP_ROOT/stop-hook-no-wrapper"
  local hook="$fixture/plugin/hooks/stop"
  local output

  mkdir -p "$(dirname "$hook")"
  cp "$REPO_ROOT/plugin/hooks/stop" "$hook"

  if ! output="$(PATH="" /bin/bash "$hook" <<< '{}' 2>&1)"; then
    fail 'stop hook failed without an adjacent atct wrapper'
  fi
  assert_eq '' "$output" 'missing atct wrapper must keep the hook silent'
}

test_stop_hook_is_silent_when_pending_is_empty() {
  local fixture="$TEMP_ROOT/stop-hook-empty"
  local hook="$fixture/plugin/hooks/stop"
  local adjacent="$fixture/plugin/bin/atct"
  local args_log="$fixture/args.log"
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")"
  cp "$REPO_ROOT/plugin/hooks/stop" "$hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
printf '%s\n' "$*" >"$ATCT_ARGS_LOG"
exit 1
SCRIPT
  chmod +x "$adjacent"

  if ! output="$(ATCT_ARGS_LOG="$args_log" PATH="" /bin/bash "$hook" <<< '{}' 2>&1)"; then
    fail 'stop hook failed when atct pending reported no answers'
  fi
  assert_eq '' "$output" 'empty pending output must keep the hook silent'
  assert_file_contains 'pending' "$args_log"
}

test_stop_hook_blocks_when_pending_exists() {
  local fixture="$TEMP_ROOT/stop-hook-pending"
  local hook="$fixture/plugin/hooks/stop"
  local adjacent="$fixture/plugin/bin/atct"
  local pending_output='- Review mode? (decision_id: d-1)'
  local output

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")"
  cp "$REPO_ROOT/plugin/hooks/stop" "$hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
if [[ "${1:-}" == pending ]]; then
  printf '%s' "$PENDING_OUTPUT"
fi
SCRIPT
  chmod +x "$adjacent"

  if ! output="$(PENDING_OUTPUT="$pending_output" PATH="" /bin/bash "$hook" <<< '{}' 2>&1)"; then
    fail 'stop hook failed when atct pending reported an answer'
  fi
  [[ "$output" == *'"decision":"block"'* ]] || fail 'pending answer must block the stop hook'
  [[ "$output" == *'"reason":'* ]] || fail 'pending answer must include a reason'
  [[ "$output" == *"$pending_output"* ]] || fail 'reason must include pending output'
}

test_stop_hook_escapes_pending_json_characters() {
  local fixture="$TEMP_ROOT/stop-hook-json-escape"
  local hook="$fixture/plugin/hooks/stop"
  local adjacent="$fixture/plugin/bin/atct"
  local output_file="$fixture/output.json"
  local pending_output
  local expected_reason

  pending_output=$'answer "quoted"\npath \\ root\tcolumn'
  expected_reason=$'A human answered a decision you parked. Call \x60atct_decision_poll\x60 with each decision_id below, then continue the work that was waiting on it.\n\n'"$pending_output"$'\n'

  mkdir -p "$(dirname "$hook")" "$(dirname "$adjacent")"
  cp "$REPO_ROOT/plugin/hooks/stop" "$hook"
  cat >"$adjacent" <<'SCRIPT'
#!/bin/bash
if [[ "${1:-}" == pending ]]; then
  printf '%s' "$PENDING_OUTPUT"
fi
SCRIPT
  chmod +x "$adjacent"

  if ! PENDING_OUTPUT="$pending_output" PATH="" /bin/bash "$hook" <<< '{}' >"$output_file" 2>&1; then
    fail 'stop hook failed while escaping pending output'
  fi
  if ! JSON_OUTPUT_FILE="$output_file" EXPECTED_REASON="$expected_reason" python3 -c '
import json
import os

with open(os.environ["JSON_OUTPUT_FILE"], encoding="utf-8") as stream:
    payload = json.load(stream)

if payload.get("decision") != "block":
    raise SystemExit("decision was not block")
if payload.get("reason") != os.environ["EXPECTED_REASON"]:
    raise SystemExit("reason did not round-trip exactly")
'; then
    fail 'stop hook output was not valid JSON or reason was altered'
  fi
}

test_static_contract
test_download_cache_and_mcp_stdout
test_context_check_preserves_exit_code
test_cleanup_failure_is_best_effort
test_checksum_failure
test_missing_checksum_tool_fails
test_unsupported_platform_fails
test_session_start_uses_adjacent_context_wrapper
test_session_start_preserves_context_and_silence
test_session_start_mentions_active_goal_permission
test_session_start_mentions_undo_boundary
test_session_start_is_silent_without_atct_wrapper
test_stop_hook_active_is_silent
test_stop_hook_is_silent_without_adjacent_wrapper
test_stop_hook_is_silent_when_pending_is_empty
test_stop_hook_blocks_when_pending_exists
test_stop_hook_escapes_pending_json_characters
printf 'PASS wrapper tests\n'
