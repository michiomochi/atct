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
  mkdir -p "$fixture/hooks" "$fixture/bin"
  cp "$REPO_ROOT/hooks/session-start" "$fixture/hooks/session-start"
  chmod +x "$fixture/hooks/session-start"
  cat >"$fixture/bin/atct" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

case "$1 $2" in
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
  if ATCT_FAKE_MODE="$mode" "$fixture/hooks/session-start" >"$stdout_file" 2>"$stderr_file"; then
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

test_schema_mismatch_prints_restart_hint_and_succeeds
test_ordinary_error_stays_silent_and_succeeds
test_normal_context_output_is_preserved
printf 'PASS session start tests\n'
