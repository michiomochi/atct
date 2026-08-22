#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_SCRIPT="${RELEASE_SCRIPT:-$REPO_ROOT/script/release.sh}"
TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/atct-release-test.XXXXXX")"
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

make_gate_only_release() {
  local target="$1"

  if ! awk '
    /# RELEASE_GATE_TEST_MARKER/ {
      print
      print "exit 0"
      found = 1
      next
    }
    { print }
    END { if (!found) exit 1 }
  ' "$RELEASE_SCRIPT" >"$target"; then
    fail 'release gate test marker is missing'
  fi
  chmod +x "$target"
}

expected_review() {
  cat <<'EOF'
Cross-goal review, before this release goes out:

  1. Did a change rely on a count that another goal has since moved?
     (text-xs was 34 in a brief and 36 in the tree)
  2. Did a published name change meaning, leaving another caller lying?
     (UnstartedTaskCount went from claimable to total; pending, the nudge list,
      and the wakeup condition each read it, and only one was updated)
  3. Did a change break an existing way of measuring or verifying?
     (SSE made playwright's waitUntil: networkidle wait forever)
  4. Did a change introduce a new violation of a cross-cutting rule?
     (Kumo has 15; fixing one component can break a different rule)

Re-run with --reviewed once you have been through these.
EOF
}

test_review_is_required() {
  local gate_only="$TEMP_ROOT/release-gate-only"
  local stdout="$TEMP_ROOT/review.stdout"
  local stderr="$TEMP_ROOT/review.stderr"
  local status=0

  make_gate_only_release "$gate_only"
  "$gate_only" 0.40.0 >"$stdout" 2>"$stderr" || status=$?

  assert_eq 1 "$status" 'release without --reviewed must stop at the gate'
  assert_eq "$(expected_review)" "$(<"$stdout")" 'review prompt'
  assert_empty_file "$stderr"
}

test_reviewed_positions_pass_gate() {
  local gate_only="$TEMP_ROOT/release-gate-only-positions"
  local stdout="$TEMP_ROOT/reviewed.stdout"
  local stderr="$TEMP_ROOT/reviewed.stderr"
  local status
  local -a args

  make_gate_only_release "$gate_only"
  for args in "0.40.0 --reviewed" "--reviewed 0.40.0"; do
    status=0
    # shellcheck disable=SC2086
    "$gate_only" $args >"$stdout" 2>"$stderr" || status=$?
    assert_eq 0 "$status" "reviewed argument order: $args"
    assert_empty_file "$stdout"
    assert_empty_file "$stderr"
  done
}

test_invalid_version_still_fails() {
  local gate_only="$TEMP_ROOT/release-gate-only-invalid-version"
  local stdout="$TEMP_ROOT/invalid-version.stdout"
  local stderr="$TEMP_ROOT/invalid-version.stderr"
  local status=0

  make_gate_only_release "$gate_only"
  "$gate_only" nope --reviewed >"$stdout" 2>"$stderr" || status=$?

  assert_eq 1 "$status" 'invalid version must fail after the review gate'
  assert_empty_file "$stdout"
  assert_file_contains 'version must look like 1.2.3, got: nope' "$stderr"
}

test_review_is_required
test_reviewed_positions_pass_gate
test_invalid_version_still_fails
printf 'PASS: release review gate\n'
