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
  local atct_archive="$fixture_dir/atct_0.4.0_darwin_arm64.tar.gz"

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
  assert_file_contains '"version": "0.4.0"' "$REPO_ROOT/plugin/.claude-plugin/plugin.json"
  assert_file_contains 'VERSION="0.4.0"' "$REPO_ROOT/plugin/bin/_resolve"
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
  local first_out
  local before
  local after

  mkdir -p "$home"
  : >"$curl_log"
  make_archives "$fixtures" good
  write_fake_tools "$fake_bin"

  first_out="$(HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct" project list)"
  assert_eq 'fake atct <project> <list>' "$first_out" 'first wrapper execution'
  assert_file_contains 'https://github.com/michiomochi/atct/releases/download/v0.4.0/atct_0.4.0_darwin_arm64.tar.gz' "$curl_log"
  assert_file_contains 'https://github.com/michiomochi/atct/releases/download/v0.4.0/checksums.txt' "$curl_log"
  [[ -x "$home/.atct/bin/atct-0.4.0" ]] || fail 'versioned atct cache is missing'
  for candidate in "$home/.atct/bin"/.download.*; do
    [[ ! -e "$candidate" ]] || fail "download directory remained after success: $candidate"
  done

  HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct-mcp" </dev/null >"$mcp_stdout" 2>"$mcp_stderr"
  assert_empty_file "$mcp_stdout"
  assert_file_contains 'fake mcp' "$mcp_stderr"
  [[ -x "$home/.atct/bin/atct-mcp-0.4.0" ]] || fail 'versioned atct-mcp cache is missing'

  mkdir -p "$home/.atct/bin/.download.stale"
  printf 'stale\n' >"$home/.atct/bin/.download.stale/file"
  printf 'database\n' >"$home/.atct/atct.db"
  first_out="$(HOME="$home" PATH="$fake_bin:$PATH" FIXTURES_DIR="$fixtures" CURL_LOG="$curl_log" CURL_FAIL=1 FAKE_OS=Darwin FAKE_ARCH=arm64 "$REPO_ROOT/plugin/bin/atct" project list)"
  assert_eq 'fake atct <project> <list>' "$first_out" 'cached execution after stale cleanup'
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
  [[ ! -e "$home/.atct/bin/atct-0.4.0" ]] || fail 'checksum mismatch left an executable cache'
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

test_static_contract
test_download_cache_and_mcp_stdout
test_checksum_failure
test_missing_checksum_tool_fails
test_unsupported_platform_fails
printf 'PASS wrapper tests\n'
