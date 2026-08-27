#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)}"
RELEASE_SCRIPT="${RELEASE_SCRIPT:-$REPO_ROOT/script/release.sh}"
DIST_CHECK_SCRIPT="${DIST_CHECK_SCRIPT:-$REPO_ROOT/script/dist-check.sh}"
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

make_dist_fixture() {
  local fixture="$1"

  mkdir -p "$fixture/_astro"
  touch "$fixture/.gitkeep"
  cat >"$fixture/index.html" <<'EOF'
<!doctype html>
<link rel="stylesheet" href="/_astro/app.css">
<script type="module" src="/_astro/app.js"></script>
EOF
  cat >"$fixture/_astro/app.js" <<'EOF'
import "./shared.js";
EOF
  cat >"$fixture/_astro/shared.js" <<'EOF'
export const ready = true;
EOF
  cat >"$fixture/_astro/app.css" <<'EOF'
body { background: url(./background.svg), url(/_astro/background-absolute.svg); }
EOF
  touch "$fixture/_astro/background.svg" "$fixture/_astro/background-absolute.svg"
}

test_dist_check_accepts_reachable_fixture() {
  local fixture="$TEMP_ROOT/dist-clean"
  local stdout="$TEMP_ROOT/dist-clean.stdout"
  local stderr="$TEMP_ROOT/dist-clean.stderr"
  local status=0

  make_dist_fixture "$fixture"
  bash "$DIST_CHECK_SCRIPT" "$fixture" >"$stdout" 2>"$stderr" || status=$?

  assert_eq 0 "$status" 'dist-check must accept a clean fixture'
}

test_dist_check_rejects_unreachable_asset() {
  local fixture="$TEMP_ROOT/dist-unreachable"
  local stdout="$TEMP_ROOT/dist-unreachable.stdout"
  local stderr="$TEMP_ROOT/dist-unreachable.stderr"
  local status=0

  make_dist_fixture "$fixture"
  touch "$fixture/_astro/StateMessage.OldGen00.js"
  bash "$DIST_CHECK_SCRIPT" "$fixture" >"$stdout" 2>"$stderr" || status=$?

  assert_eq 1 "$status" 'dist-check must reject an unreachable asset'
  assert_file_contains '_astro/StateMessage.OldGen00.js' "$stderr"
}

test_dist_check_rejects_missing_gitkeep() {
  local fixture="$TEMP_ROOT/dist-missing-gitkeep"
  local stdout="$TEMP_ROOT/dist-missing-gitkeep.stdout"
  local stderr="$TEMP_ROOT/dist-missing-gitkeep.stderr"
  local status=0

  make_dist_fixture "$fixture"
  rm -- "$fixture/.gitkeep"
  bash "$DIST_CHECK_SCRIPT" "$fixture" >"$stdout" 2>"$stderr" || status=$?

  assert_eq 1 "$status" 'dist-check must reject a missing .gitkeep'
  assert_file_contains 'go:embed' "$stderr"
}

test_dist_check_accepts_multiple_index_chunks() {
  local fixture="$TEMP_ROOT/dist-multiple-index"
  local stdout="$TEMP_ROOT/dist-multiple-index.stdout"
  local stderr="$TEMP_ROOT/dist-multiple-index.stderr"
  local status=0

  make_dist_fixture "$fixture"
  cat >>"$fixture/index.html" <<'EOF'
<script type="module" src="/_astro/index.CgxM0nL0.js"></script>
<script type="module" src="/_astro/index.DxeUZV0I.js"></script>
EOF
  touch "$fixture/_astro/index.CgxM0nL0.js" "$fixture/_astro/index.DxeUZV0I.js"
  bash "$DIST_CHECK_SCRIPT" "$fixture" >"$stdout" 2>"$stderr" || status=$?

  assert_eq 0 "$status" 'dist-check must accept two referenced index chunks'
}

test_dist_check_accepts_gitkeep_only_fixture() {
  local fixture="$TEMP_ROOT/dist-gitkeep-only"
  local stdout="$TEMP_ROOT/dist-gitkeep-only.stdout"
  local stderr="$TEMP_ROOT/dist-gitkeep-only.stderr"
  local status=0

  mkdir -p "$fixture"
  touch "$fixture/.gitkeep"
  bash "$DIST_CHECK_SCRIPT" "$fixture" >"$stdout" 2>"$stderr" || status=$?

  assert_eq 0 "$status" 'dist-check must accept a .gitkeep-only fixture'
}

test_dist_check_rejects_assets_without_html_entrypoint() {
  local fixture="$TEMP_ROOT/dist-no-html"
  local stdout="$TEMP_ROOT/dist-no-html.stdout"
  local stderr="$TEMP_ROOT/dist-no-html.stderr"
  local status=0

  mkdir -p "$fixture/_astro"
  touch "$fixture/.gitkeep" "$fixture/_astro/orphan.js"
  bash "$DIST_CHECK_SCRIPT" "$fixture" >"$stdout" 2>"$stderr" || status=$?

  assert_eq 1 "$status" 'dist-check must reject assets without an HTML entrypoint'
  assert_file_contains 'HTML entrypoint' "$stderr"
  assert_file_contains 'dist-no-html' "$stderr"
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

test_project_claim_reacquisition_is_documented() {
  local release_header="$TEMP_ROOT/release-header"
  local release_done="$TEMP_ROOT/release-done"

  sed -n '1,20p' "$RELEASE_SCRIPT" >"$release_header"
  awk '/^echo "==> done\./ { capture = 1 } capture { print }' "$RELEASE_SCRIPT" >"$release_done"

  assert_file_contains 'After the replacement, each space must reacquire its project claim' "$release_header"
  assert_file_contains 'After the replacement, each space must reacquire its project claim' "$release_done"
  assert_file_contains 'call atct_project_release first' "$release_done"
  assert_file_contains 'then atct_project_claim' "$release_done"
}

test_plugin_manifests_are_in_sync
test_release_bumps_both_plugin_manifests
test_release_rejects_mismatched_plugin_versions
test_review_is_required
test_reviewed_positions_pass_gate
test_invalid_version_still_fails
test_project_claim_reacquisition_is_documented
test_dist_check_accepts_reachable_fixture
test_dist_check_rejects_unreachable_asset
test_dist_check_rejects_missing_gitkeep
test_dist_check_accepts_multiple_index_chunks
test_dist_check_accepts_gitkeep_only_fixture
test_dist_check_rejects_assets_without_html_entrypoint
printf 'PASS: release review gate\n'
