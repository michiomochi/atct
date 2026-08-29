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
  '## Claim before you start'
  '## Delegate a task'
  '### Two-layer delegation'
  '## Delegate a goal'
  '## Fill in a report on a handoff that is already closed'
  '## Recover when your role comes back wrong'
  '## Close a task the moment it is finished'
  '## Report completion in six parts'
  '## Act on reversible choices, ask about irreversible ones'
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
  # Parse the manifest as JSON. A sed match on the first "version" line breaks as
  # soon as another key carries a version, such as a dependency constraint.
  plugin_version="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$REPO_ROOT/.claude-plugin"/plugin.json)"
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

test_goal_handoff_forbids_upward_design_questions() {
  delegate_goal_section_contains "Decide this goal's design yourself. Do not bring the delegator a design"
  delegate_goal_section_contains "reading of this goal's code. Send the delegator nothing until the completion"
}

test_goal_handoff_names_the_single_upward_message() {
  delegate_goal_section_contains '`next_steps` for what you left, and `atct_decision_ask` for anything that'

  local section
  section="$(unsent_report_section)"
  grep -Fq -- '| the goal is finished | `atct_goal_handoff_complete`, the one message |' <<<"$section" ||
    fail 'unsent report section must name the single upward completion message'
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
  watch="$(lineno 'Then attach `atct watch -goal <goal_id>` to a background stream the way')"
  silence="$(lineno "Decide this goal's design yourself. Do not bring the delegator a design")"
  fin="$(lineno 'When the work is complete, record completion by calling')"

  [[ -n "$watch" && -n "$silence" && -n "$fin" ]] ||
    fail 'goal handoff silence order requires watch, silence, and completion paragraphs'
  (( watch < silence && silence < fin )) ||
    fail "goal handoff silence paragraphs are in the wrong order: watch=$watch silence=$silence completion=$fin"
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
  delegate_goal_section_contains '`atct_goal_handoff_complete` lands; that report is the entry point.'
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
    '| the goal is finished |'; do
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

  grep -Fq -- "committed, each raises a detection on the delegator's watch. On 2026-08-27 goal" <<<"$section" ||
    fail 'unsent report section must name the stall detection'
  grep -Fq -- '172 stalled with three tasks still `todo` and eight files uncommitted, and goal' <<<"$section" ||
    fail 'unsent report section must name goal 172'
  grep -Fq -- 'detections had already fired; nobody had been told to read them.' <<<"$section" ||
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
  recovery_section_contains '- goal: `atct_goal_handoff_complete` → `atct_goal_handoff_request` (the commander must issue the handoff again)'
  recovery_section_contains 'the subcommander cannot reissue it, and the goal waits on the'
}

test_recovery_section_has_task_path_and_non_repair_note() {
  recovery_section_contains 'atct_handoff_complete` (with `task_id` and `complete_report`)'
  recovery_section_contains 'Rejection is automatic, so the goal step above that asks the commander to reissue the handoff is needed only for the last trigger.'
}

test_handoff_completion_reports_are_explicit() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  assert_file_contains 'with the `task_id` provided in this request and a `complete_report`' "$atct_skill"
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
  assert_file_contains '`atct_handoff_report_amend` with the specific `handoff_id`' "$atct_skill"
  assert_file_contains 'It is not part of normal executor completion.' "$atct_skill"
}

test_handoff_completion_keeps_one_normal_path() {
  local normal_section
  local completion_step
  normal_section="$(sed -n '/^## Delegate a task$/,/^## Delegate a goal$/p' "$REPO_ROOT/skills/atct/SKILL.md")"
  completion_step="$(sed -n '/When the work is complete, record completion by calling `atct_handoff_complete`/,/^$/p' <<<"$normal_section")"
  ! grep -Fq -- 'atct_handoff_report_amend' <<<"$normal_section" || fail 'normal task completion must not name the repair tool'
  ! grep -Fq -- 'with the specific `handoff_id`' <<<"$normal_section" || fail 'normal task completion must not require handoff_id'
  ! grep -Fq -- 'with only the `task_id` provided in this request.' <<<"$completion_step" || fail 'task completion must require complete_report'
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
# longer one: `atct_handoff_complete` would otherwise hit
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

  for tool in atct_session_identify atct_handoff_receive atct_role \
    atct_task_update atct_handoff_complete; do
    grep -Fq -- "\`$tool\`" <<<"$allowlist" ||
      fail "the allowlist line does not allow <$tool>"
  done

  for tool in atct_goal_handoff_complete atct_goal_handoff_receive \
    atct_goal_handoff_request atct_goal_claim atct_goal_release \
    atct_goal_complete atct_goal_update_content atct_project_claim \
    atct_project_release atct_task_claim atct_handoff_request \
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
    atct_project_release atct_task_claim atct_handoff_request \
    atct_task_create atct_decision_ask; do
    grep -Fq -- "\`$tool\`" <<<"$forbidden" ||
      fail "the forbidden block does not forbid <$tool> by name"
  done

  for tool in atct_session_identify atct_handoff_receive atct_role \
    atct_task_update atct_handoff_complete; do
    ! grep -Fq -- "\`$tool\`" <<<"$forbidden" ||
      fail "the forbidden block must not forbid the allowed <$tool>"
  done
}

test_delegation_reports_completion_before_closing_the_task() {
  local section
  local report_line
  local close_line

  delegate_task_section_contains 'Report completion before closing the task'

  section="$(delegate_task_section)"
  report_line="$(grep -nF -- 'record completion by calling `atct_handoff_complete`' \
    <<<"$section" | head -1 | cut -d: -f1 || true)"
  close_line="$(grep -nF -- 'Only then close the task' \
    <<<"$section" | head -1 | cut -d: -f1 || true)"

  [[ -n "$report_line" ]] || fail 'delegate task section never says to call `atct_handoff_complete`'
  [[ -n "$close_line" ]] || fail 'delegate task section never says to close the task'
  (( report_line < close_line )) ||
    fail "completion must be reported before the task is closed, or the report is overwritten: report at line $report_line, close at line $close_line"
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
  completion_step="$(sed -n '/When the work is complete, record completion by calling/,/^$/p' <<<"$goal_section")"
  ! grep -Fq -- 'atct_goal_handoff_report_amend' <<<"$goal_section" || fail 'normal goal completion must not name the repair tool'
  ! grep -Fq -- 'handoff_id' <<<"$completion_step" || fail 'goal completion must not require handoff_id'
  ! grep -Fq -- 'with only the `goal_id` provided in this request.' <<<"$completion_step" || fail 'goal completion must require complete_report'
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
  assert_file_contains '`atct_goal_handoff_complete` with the `goal_id` provided in this request' "$atct_skill"
  assert_file_contains 'and a `complete_report`' "$atct_skill"
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
  monitor_line="$(grep -n '^## .*Claude Code.*Monitor' "$start_skill" | head -1 | cut -d: -f1)"
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

test_task_close_names_the_commits_argument() {
  local atct_skill="$REPO_ROOT/skills/atct/SKILL.md"
  local tools_go="$REPO_ROOT/internal/mcpshim/tools.go"
  local close_section
  local task_update_input
  local task_update_call

  close_section="$(sed -n '/^## Close a task the moment it is finished$/,/^## Keep going$/p' "$atct_skill")"
  grep -Fq -- 'commits=' <<<"$close_section" ||
    fail 'closing a task must name the commits argument'

  task_update_input="$(sed -n '/^type TaskUpdateIn struct {$/,/^}$/p' "$tools_go")"
  grep -Fq -- 'json:"commits' <<<"$task_update_input" ||
    fail 'atct_task_update input must keep a commits field'

  task_update_call="$(sed -n '/callWithUnappliedDecisions(ctx, c, "task.update", map\[string\]any{/,/^\t\t})$/p' "$tools_go")"
  grep -Fq -- '"commits"' <<<"$task_update_call" ||
    fail 'atct_task_update must pass commits to task.update'
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
test_task_close_names_the_commits_argument
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
test_delegation_reports_completion_before_closing_the_task
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
test_skill_numbering_is_contiguous
test_ordered_sections_name_the_out_of_order_consequence
test_ordered_sections_are_numbered
test_numbering_check_catches_a_broken_list
test_out_of_order_check_catches_a_missing_consequence
printf 'PASS wrapper tests\n'
