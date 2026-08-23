#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)}"
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

manifest_version() {
  python3 - "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    print(json.load(stream)["version"])
PY
}

make_bump_only() {
  local target="$1"

  if ! awk '
    /^python3 - / && /<<.*PY/ {
      in_bump = 1
      next
    }
    in_bump && /^PY$/ {
      found = 1
      exit
    }
    in_bump { print }
    END { if (!found) exit 1 }
  ' "$RELEASE_SCRIPT" >"$target"; then
    fail 'release bump block is missing'
  fi
  chmod +x "$target"
}

make_bump_fixture() {
  local fixture="$1"

  mkdir -p "$fixture/.claude-plugin" "$fixture/.codex-plugin" "$fixture/bin"
  cp "$REPO_ROOT/.claude-plugin"/plugin.json "$fixture/.claude-plugin"/plugin.json
  cp "$REPO_ROOT/.codex-plugin"/plugin.json "$fixture/.codex-plugin"/plugin.json
  cp "$REPO_ROOT/bin/_resolve" "$fixture/bin/_resolve"
}

test_plugin_manifests_are_in_sync() {
  local claude_manifest="$REPO_ROOT/.claude-plugin"/plugin.json
  local codex_manifest="$REPO_ROOT/.codex-plugin"/plugin.json

  [[ -f "$claude_manifest" ]] || fail "missing Claude plugin manifest: $claude_manifest"
  [[ -f "$codex_manifest" ]] || fail "missing Codex plugin manifest: $codex_manifest"
  assert_eq "$(manifest_version "$claude_manifest")" "$(manifest_version "$codex_manifest")" \
    'Claude and Codex plugin versions must match'
}

test_release_bumps_both_plugin_manifests() {
  local fixture="$TEMP_ROOT/bump-fixture"
  local bump_script="$TEMP_ROOT/release-bump-only"
  local version=99.88.77

  make_bump_fixture "$fixture"
  make_bump_only "$bump_script"
  (cd "$fixture" && python3 "$bump_script" "$version")

  assert_eq "$version" "$(manifest_version "$fixture/.claude-plugin"/plugin.json)" \
    'Claude plugin version after release bump'
  assert_eq "$version" "$(manifest_version "$fixture/.codex-plugin"/plugin.json)" \
    'Codex plugin version after release bump'
  assert_file_contains "$version" "$fixture/bin/_resolve"
}

test_release_rejects_mismatched_plugin_versions() {
  local fixture="$TEMP_ROOT/mismatched-bump-fixture"
  local bump_script="$TEMP_ROOT/release-bump-only-mismatch"
  local stderr="$TEMP_ROOT/mismatched-bump.stderr"
  local status=0

  make_bump_fixture "$fixture"
  python3 - "$fixture/.codex-plugin"/plugin.json <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text())
data["version"] = "0.0.0"
path.write_text(json.dumps(data, indent=2) + "\n")
PY
  make_bump_only "$bump_script"
  (cd "$fixture" && python3 "$bump_script" 99.88.78) 2>"$stderr" || status=$?

  assert_eq 1 "$status" 'release bump must reject mismatched plugin versions'
  assert_file_contains 'plugin manifests have different versions' "$stderr"
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

test_plugin_manifests_are_in_sync
test_release_bumps_both_plugin_manifests
test_release_rejects_mismatched_plugin_versions
test_review_is_required
test_reviewed_positions_pass_gate
test_invalid_version_still_fails
printf 'PASS: release review gate\n'
