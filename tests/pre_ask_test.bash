#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/atct-pre-ask-test.XXXXXX")"
trap 'rm -rf -- "$TEMP_ROOT"' EXIT

ORIGINAL_PATH="$PATH"
RUN_OUTPUT=''
RUN_ERROR=''
RUN_STATUS=0
FAKE_BIN=''
PLUGIN_VERSION="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.claude-plugin/plugin.json")"
FAKE_VERSION="$PLUGIN_VERSION"

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

assert_contains() {
  local needle="$1"
  local haystack="$2"
  local message="${3:-value does not contain expected text}"
  [[ "$haystack" == *"$needle"* ]] || fail "$message: missing <$needle> in <$haystack>"
}

assert_empty() {
  [[ -z "$1" ]] || fail "expected empty output, got <$1>"
}

assert_file_contains() {
  local needle="$1"
  local file="$2"
  grep -Fq -- "$needle" "$file" || fail "<$file> does not contain <$needle>"
}

make_fake_atct() {
  local fake_bin="$1"
  local home="$2"
  mkdir -p "$fake_bin"

  cat >"$fake_bin/atct" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  version)
    printf '%s\n' "${FAKE_ATCT_VERSION:-0.63.1}"
    exit 0
    ;;
  context)
    ;;
  *)
    exit 90
    ;;
esac
printf '%s|%s\n' "$PWD" "${1:-}" >"$FAKE_ATCT_LOG"

case "${FAKE_ATCT_MODE:-}" in
  managed)
    printf 'managed context\n'
    ;;
  empty)
    :
    ;;
  failure)
    exit 42
    ;;
  *)
    exit 91
    ;;
esac
SCRIPT
  chmod +x "$fake_bin/atct"
}

make_hook_without_wrapper() {
  local hook_root="$1"
  mkdir -p "$hook_root/hooks" "$hook_root/.claude-plugin"
  cp "$REPO_ROOT/hooks/pre-ask" "$hook_root/hooks/pre-ask"
  cp "$REPO_ROOT/.claude-plugin/plugin.json" "$hook_root/.claude-plugin/plugin.json"
  chmod +x "$hook_root/hooks/pre-ask"
}

run_hook() {
  local hook="$1"
  local home="$2"
  local input="$3"
  local mode="$4"
  local log="$5"
  local error_file="$log.stderr"

  if RUN_OUTPUT="$(
    HOME="$home" \
      PATH="$FAKE_BIN:$ORIGINAL_PATH" \
      FAKE_ATCT_MODE="$mode" \
      FAKE_ATCT_VERSION="$FAKE_VERSION" \
      FAKE_ATCT_LOG="$log" \
      "$hook" <<<"$input" 2>"$error_file"
  )"; then
    RUN_STATUS=0
  else
    RUN_STATUS=$?
  fi
  RUN_ERROR="$(<"$error_file")"
}

test_managed_ask_is_denied() {
  local home="$TEMP_ROOT/managed-home"
  local fake_bin="$TEMP_ROOT/managed-fake-bin"
  local log="$TEMP_ROOT/managed-atct.log"
  local project="$TEMP_ROOT/managed-project"
  mkdir -p "$home" "$project"
  make_fake_atct "$fake_bin" "$home"
  FAKE_BIN="$fake_bin"

  local input
  input="$(printf '{"tool_name":"AskUserQuestion","cwd":"%s"}' "$project")"
  run_hook "$REPO_ROOT/hooks/pre-ask" "$home" "$input" managed "$log"

  assert_eq 0 "$RUN_STATUS" 'managed AskUserQuestion hook status'
  assert_contains '"hookEventName":"PreToolUse"' "$RUN_OUTPUT"
  assert_contains '"permissionDecision":"deny"' "$RUN_OUTPUT"
  assert_contains 'atct_decision_ask' "$RUN_OUTPUT"
  assert_contains 'option A' "$RUN_OUTPUT"
  assert_contains 'option B' "$RUN_OUTPUT"
  assert_contains 'wait_ms=0' "$RUN_OUTPUT"
  assert_contains 'default_option' "$RUN_OUTPUT"
  local expected_cwd
  expected_cwd="$(cd -- "$project" && pwd)"
  assert_eq "$expected_cwd|context" "$(<"$log")" 'atct context must run from hook cwd'
  assert_file_contains '"PreToolUse"' "$REPO_ROOT/hooks/claude-hooks.json"
  assert_file_contains '"matcher": "AskUserQuestion"' "$REPO_ROOT/hooks/claude-hooks.json"
}

test_other_tool_is_ignored() {
  local home="$TEMP_ROOT/other-tool-home"
  local fake_bin="$TEMP_ROOT/other-tool-fake-bin"
  local log="$TEMP_ROOT/other-tool-atct.log"
  local project="$TEMP_ROOT/other-tool-project"
  mkdir -p "$home" "$project"
  make_fake_atct "$fake_bin" "$home"
  FAKE_BIN="$fake_bin"

  local input
  input="$(printf '{"tool_name":"Bash","cwd":"%s"}' "$project")"
  run_hook "$REPO_ROOT/hooks/pre-ask" "$home" "$input" managed "$log"

  assert_eq 0 "$RUN_STATUS" 'non-AskUserQuestion hook status'
  assert_empty "$RUN_OUTPUT"
  [[ ! -e "$log" ]] || fail 'non-AskUserQuestion must not invoke atct context'
}

test_missing_atct_prints_homebrew_install_instruction_without_decision() {
  local home="$TEMP_ROOT/missing-atct-home"
  local fake_bin="$TEMP_ROOT/missing-atct-fake-bin"
  local hook_root="$TEMP_ROOT/missing-atct-plugin"
  local log="$TEMP_ROOT/missing-atct.log"
  local project="$TEMP_ROOT/missing-atct-project"
  mkdir -p "$home" "$project"
  make_fake_atct "$fake_bin" "$home"
  make_hook_without_wrapper "$hook_root"

  local input
  input="$(printf '{"tool_name":"AskUserQuestion","cwd":"%s"}' "$project")"
  local error_file="$log.stderr"
  if RUN_OUTPUT="$(HOME="$home" PATH="/usr/bin:/bin" "$hook_root/hooks/pre-ask" <<<"$input" 2>"$error_file")"; then
    RUN_STATUS=0
  else
    RUN_STATUS=$?
  fi
  RUN_ERROR="$(<"$error_file")"

  assert_eq 0 "$RUN_STATUS" 'missing atct hook status'
  assert_empty "$RUN_OUTPUT"
  assert_eq 'ATCT: install or upgrade the CLI with: brew install --cask michiomochi/tap/atct' "$RUN_ERROR" 'missing atct stderr'
}

test_older_atct_prints_homebrew_upgrade_instruction_without_decision() {
  local home="$TEMP_ROOT/older-atct-home"
  local fake_bin="$TEMP_ROOT/older-atct-fake-bin"
  local log="$TEMP_ROOT/older-atct.log"
  local project="$TEMP_ROOT/older-atct-project"
  mkdir -p "$home" "$project"
  make_fake_atct "$fake_bin" "$home"
  FAKE_BIN="$fake_bin"
  FAKE_VERSION='0.62.0'

  local input
  input="$(printf '{"tool_name":"AskUserQuestion","cwd":"%s"}' "$project")"
  run_hook "$REPO_ROOT/hooks/pre-ask" "$home" "$input" managed "$log"

  assert_eq 0 "$RUN_STATUS" 'older atct hook status'
  assert_empty "$RUN_OUTPUT"
  assert_eq 'ATCT: upgrade the CLI with: brew upgrade --cask michiomochi/tap/atct' "$RUN_ERROR" 'older atct stderr'
  FAKE_VERSION="$PLUGIN_VERSION"
}

test_context_failure_is_ignored() {
  local home="$TEMP_ROOT/context-failure-home"
  local fake_bin="$TEMP_ROOT/context-failure-fake-bin"
  local log="$TEMP_ROOT/context-failure-atct.log"
  local project="$TEMP_ROOT/context-failure-project"
  mkdir -p "$home" "$project"
  make_fake_atct "$fake_bin" "$home"
  FAKE_BIN="$fake_bin"

  local input
  input="$(printf '{"tool_name":"AskUserQuestion","cwd":"%s"}' "$project")"
  run_hook "$REPO_ROOT/hooks/pre-ask" "$home" "$input" failure "$log"

  assert_eq 0 "$RUN_STATUS" 'failed atct context hook status'
  assert_empty "$RUN_OUTPUT"
  local expected_cwd
  expected_cwd="$(cd -- "$project" && pwd)"
  assert_eq "$expected_cwd|context" "$(<"$log")" 'failed context must still be attempted'
}

test_empty_context_is_ignored() {
  local home="$TEMP_ROOT/empty-context-home"
  local fake_bin="$TEMP_ROOT/empty-context-fake-bin"
  local log="$TEMP_ROOT/empty-context-atct.log"
  local project="$TEMP_ROOT/empty-context-project"
  mkdir -p "$home" "$project"
  make_fake_atct "$fake_bin" "$home"
  FAKE_BIN="$fake_bin"

  local input
  input="$(printf '{"tool_name":"AskUserQuestion","cwd":"%s"}' "$project")"
  run_hook "$REPO_ROOT/hooks/pre-ask" "$home" "$input" empty "$log"

  assert_eq 0 "$RUN_STATUS" 'empty context hook status'
  assert_empty "$RUN_OUTPUT"
  local expected_cwd
  expected_cwd="$(cd -- "$project" && pwd)"
  assert_eq "$expected_cwd|context" "$(<"$log")" 'empty context must still be attempted'
}

[[ -f "$REPO_ROOT/hooks/pre-ask" ]] || fail 'pre-ask hook is missing'
test_managed_ask_is_denied
test_other_tool_is_ignored
test_missing_atct_prints_homebrew_install_instruction_without_decision
test_older_atct_prints_homebrew_upgrade_instruction_without_decision
test_context_failure_is_ignored
test_empty_context_is_ignored
printf 'PASS: pre-ask hook 6 behavior cases\n'
