#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/atct-cache-prune-test.XXXXXX")"
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

assert_file_exists() {
  local file="$1"
  [[ -e "$file" ]] || fail "expected file to exist: $file"
}

assert_file_absent() {
  local file="$1"
  [[ ! -e "$file" ]] || fail "expected file to be absent: $file"
}

wrapper_version() {
  sed -n 's/^VERSION="\([0-9][0-9.]*\)"/\1/p' "$REPO_ROOT/bin/_resolve" | head -1
}

write_cached_binary() {
  local home="$1"
  local name="$2"
  local output="$3"
  local cache_dir="$home/.atct/bin"

  mkdir -p "$cache_dir"
  cat >"$cache_dir/$name" <<SCRIPT
#!/usr/bin/env bash
printf '%s\\n' '$output'
SCRIPT
  chmod +x "$cache_dir/$name"
}

set_old_mtime() {
  local file
  for file in "$@"; do
    touch -t 200001010000 "$file"
  done
}

mtime() {
  if [[ "$(uname -s)" == Darwin ]]; then
    stat -f '%m' "$1"
  else
    stat -c '%Y' "$1"
  fi
}

test_prunes_stale_cache_and_touches_cache_hit() {
  local home="$TEMP_ROOT/stale-cache-home"
  local version
  local current
  local stale
  local stale_unversioned
  local recent
  local current_mcp
  local output

  version="$(wrapper_version)"
  current="$home/.atct/bin/atct-$version"
  stale="$home/.atct/bin/atct-0.53.0"
  stale_unversioned="$home/.atct/bin/atct-mcp-newest"
  recent="$home/.atct/bin/atct-0.53.1"
  current_mcp="$home/.atct/bin/atct-mcp-$version"

  write_cached_binary "$home" "atct-$version" 'cached atct'
  printf 'stale\n' >"$stale"
  printf 'stale unversioned\n' >"$stale_unversioned"
  printf 'recent\n' >"$recent"
  printf 'current mcp\n' >"$current_mcp"
  set_old_mtime "$current" "$stale" "$stale_unversioned" "$current_mcp"

  output="$(HOME="$home" "$REPO_ROOT/bin/atct" project list)"

  assert_eq 'cached atct' "$output" 'cached execution'
  assert_file_absent "$stale"
  assert_file_absent "$stale_unversioned"
  assert_file_exists "$recent"
  assert_file_exists "$current"
  assert_file_exists "$current_mcp"
  [[ "$(mtime "$current")" -gt 946684800 ]] || fail 'cache hit did not update mtime'
}

test_cleanup_failure_does_not_block_cached_execution() {
  local home="$TEMP_ROOT/cleanup-failure-home"
  local fake_bin="$TEMP_ROOT/cleanup-failure-bin"
  local rm_log="$TEMP_ROOT/cleanup-failure-rm.log"
  local stale="$home/.atct/bin/atct-0.53.0"
  local version
  local output

  version="$(wrapper_version)"
  write_cached_binary "$home" "atct-$version" 'cached after cleanup failure'
  mkdir -p "$fake_bin"
  printf 'stale\n' >"$stale"
  set_old_mtime "$stale"
  cat >"$fake_bin/rm" <<'SCRIPT'
#!/usr/bin/env bash
printf 'rm invoked\n' >>"$RM_LOG"
exit 1
SCRIPT
  chmod +x "$fake_bin/rm"

  output="$(HOME="$home" PATH="$fake_bin:$PATH" RM_LOG="$rm_log" "$REPO_ROOT/bin/atct" project list)" \
    || fail 'cleanup failure prevented cached execution'

  assert_eq 'cached after cleanup failure' "$output" 'cached execution after cleanup failure'
  assert_file_exists "$stale"
  assert_file_exists "$rm_log"
}

test_release_script_runs_cache_prune_test() {
  grep -Fq 'bash tests/cache_prune_test.bash' "$REPO_ROOT/script/release.sh" \
    || fail 'release script does not run cache prune tests'
}

test_prunes_stale_cache_and_touches_cache_hit
test_cleanup_failure_does_not_block_cached_execution
test_release_script_runs_cache_prune_test
printf 'PASS cache prune tests\n'
