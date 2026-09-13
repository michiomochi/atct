#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/atct-session-start-test.XXXXXX")"
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

new_fixture() {
  local fixture="$1"
  mkdir -p "$fixture/hooks" "$fixture/bin" "$fixture/.claude-plugin"
  cp "$REPO_ROOT/hooks/session-start" "$fixture/hooks/session-start"
  chmod +x "$fixture/hooks/session-start"
  printf '{"version":"0.63.1"}\n' >"$fixture/.claude-plugin/plugin.json"
  assert_eq '0.63.1' "$(awk -F '"' '/"version"[[:space:]]*:/ { print $4; exit }' "$fixture/.claude-plugin/plugin.json")" 'fixture plugin version'
  cat >"$fixture/bin/atct" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-} ${2:-}" in
  "version ")
    printf '%s\n' "${ATCT_FAKE_VERSION:-0.63.1}"
    ;;
  "context -brief")
    case "$ATCT_FAKE_MODE" in
      schema-mismatch)
        printf 'database records unknown schema migration "9999_future.sql"\n' >&2
        exit 3
        ;;
      ordinary-error)
        exit 1
        ;;
      normal)
        printf 'ATCT context for test\n'
        exit 0
        ;;
      *)
        printf 'unknown fake mode\n' >&2
        exit 99
        ;;
    esac
    ;;
  "daemon start")
    exit 0
    ;;
  *)
    printf 'unexpected fake command: %s\n' "$*" >&2
    exit 99
    ;;
esac
SCRIPT
  chmod +x "$fixture/bin/atct"
}

run_hook() {
  local fixture="$1"
  local mode="$2"
  local stdout_file="$3"
  local stderr_file="$4"
  local fake_version="${5:-0.63.1}"
  if PATH="$fixture/bin:$PATH" ATCT_FAKE_MODE="$mode" ATCT_FAKE_VERSION="$fake_version" "$fixture/hooks/session-start" >"$stdout_file" 2>"$stderr_file"; then
    HOOK_STATUS=0
  else
    HOOK_STATUS=$?
  fi
}

test_schema_mismatch_prints_restart_hint_and_succeeds() {
  local fixture="$TEMP_ROOT/schema-mismatch"
  local stdout_file="$fixture/stdout"
  local stderr_file="$fixture/stderr"
  new_fixture "$fixture"

  run_hook "$fixture" schema-mismatch "$stdout_file" "$stderr_file"

  assert_eq 0 "$HOOK_STATUS" 'schema mismatch hook status'
  assert_eq '' "$(<"$stdout_file")" 'schema mismatch stdout'
  assert_eq "ATCT: this session's atct cannot open the database (it predates a release). Restart the session." "$(<"$stderr_file")" 'schema mismatch stderr'
}

test_ordinary_error_stays_silent_and_succeeds() {
  local fixture="$TEMP_ROOT/ordinary-error"
  local stdout_file="$fixture/stdout"
  local stderr_file="$fixture/stderr"
  new_fixture "$fixture"

  run_hook "$fixture" ordinary-error "$stdout_file" "$stderr_file"

  assert_eq 0 "$HOOK_STATUS" 'ordinary error hook status'
  assert_eq '' "$(<"$stdout_file")" 'ordinary error stdout'
  assert_eq '' "$(<"$stderr_file")" 'ordinary error stderr'
}

test_normal_context_output_is_preserved() {
  local fixture="$TEMP_ROOT/normal"
  local stdout_file="$fixture/stdout"
  local stderr_file="$fixture/stderr"
  new_fixture "$fixture"

  run_hook "$fixture" normal "$stdout_file" "$stderr_file"

  assert_eq 0 "$HOOK_STATUS" 'normal hook status'
  assert_eq 'ATCT context for test' "$(<"$stdout_file")" 'normal context output'
  assert_eq '' "$(<"$stderr_file")" 'normal context stderr'
}

test_missing_cli_prints_homebrew_install_instruction() {
  local fixture="$TEMP_ROOT/missing-cli"
  local stdout_file="$fixture/stdout"
  local stderr_file="$fixture/stderr"
  new_fixture "$fixture"
  rm "$fixture/bin/atct"

  if PATH="/usr/bin:/bin" "$fixture/hooks/session-start" >"$stdout_file" 2>"$stderr_file"; then
    HOOK_STATUS=0
  else
    HOOK_STATUS=$?
  fi

  assert_eq 0 "$HOOK_STATUS" 'missing CLI hook status'
  assert_eq '' "$(<"$stdout_file")" 'missing CLI stdout'
  assert_eq 'ATCT: install or upgrade the CLI with: brew install --cask michiomochi/tap/atct' "$(<"$stderr_file")" 'missing CLI stderr'
}

test_older_cli_prints_homebrew_upgrade_instruction() {
  local fixture="$TEMP_ROOT/older-cli"
  local stdout_file="$fixture/stdout"
  local stderr_file="$fixture/stderr"
  new_fixture "$fixture"

  run_hook "$fixture" normal "$stdout_file" "$stderr_file" '0.62.0'

  assert_eq 0 "$HOOK_STATUS" 'older CLI hook status'
  assert_eq '' "$(<"$stdout_file")" 'older CLI stdout'
  assert_eq 'ATCT: upgrade the CLI with: brew upgrade --cask michiomochi/tap/atct' "$(<"$stderr_file")" 'older CLI stderr'
}

test_missing_manifest_prints_homebrew_upgrade_instruction() {
  local fixture="$TEMP_ROOT/missing-manifest"
  local stdout_file="$fixture/stdout"
  local stderr_file="$fixture/stderr"
  new_fixture "$fixture"
  rm "$fixture/.claude-plugin/plugin.json"

  run_hook "$fixture" normal "$stdout_file" "$stderr_file"

  assert_eq 0 "$HOOK_STATUS" 'missing manifest hook status'
  assert_eq '' "$(<"$stdout_file")" 'missing manifest stdout'
  assert_eq 'ATCT: upgrade the CLI with: brew upgrade --cask michiomochi/tap/atct' "$(<"$stderr_file")" 'missing manifest stderr'
}

test_schema_mismatch_prints_restart_hint_and_succeeds
test_ordinary_error_stays_silent_and_succeeds
test_normal_context_output_is_preserved
test_missing_cli_prints_homebrew_install_instruction
test_older_cli_prints_homebrew_upgrade_instruction
test_missing_manifest_prints_homebrew_upgrade_instruction
printf 'PASS session start tests\n'
