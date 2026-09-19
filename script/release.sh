#!/usr/bin/env bash
# Release atct. Takes the new version, does everything except the watch swap.
#
# The watch is a Claude Code Monitor, so stopping and re-arming it needs TaskStop
# and Monitor from inside the session. Those two steps stay with the caller:
#
#   1. TaskStop the watch task
#   2. script/release.sh <version>
#   3. atct daemon stop && atct daemon start   (with the new binary)
#   4. Monitor the new watch
#   5. After the replacement, each space calls atct_project_claim. Releasing
#      first is no longer needed: the claim of a session whose monitor stopped
#      renewing its lease is stale on its own, and claiming takes it over.
#
# Everything between 2 and 3 used to be ten separate commands, which left ten
# places to stop and write a summary instead of continuing.
set -euo pipefail

version=""
reviewed=0

for arg in "$@"; do
  case "$arg" in
    --reviewed)
      reviewed=1
      ;;
    *)
      if [[ -n "$version" ]]; then
        echo "usage: script/release.sh <version> [--reviewed]" >&2
        exit 1
      fi
      version="$arg"
      ;;
  esac
done

if [[ -z "$version" ]]; then
  echo "usage: script/release.sh <version> [--reviewed]" >&2
  exit 1
fi

if (( reviewed == 0 )); then
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
  exit 1
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "version must look like 1.2.3, got: $version" >&2
  exit 1
fi

# RELEASE_GATE_TEST_MARKER
current="$(python3 -c 'import json;print(json.load(open(".claude-plugin" + "/plugin.json"))["version"])')"
echo "==> $current -> $version"

if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
  echo "the working tree has uncommitted changes; commit them first" >&2
  git status --short >&2
  exit 1
fi

# Each check is quiet while it passes and prints everything when it does not.
# Discarding the output unconditionally cost two debugging sessions on
# 2026-09-19: the release stopped at "==> tests" with no record of which test
# had failed, and every one of them passed when run again by hand.
run_check() {
  local label="$1"
  shift
  local output
  if ! output="$("$@" 2>&1)"; then
    printf '%s\n' "$output" >&2
    echo "release check failed: $label" >&2
    return 1
  fi
}

echo "==> tests"
go build ./...
run_check "go test" go test -count=1 -timeout 600s ./...
run_check "session_start_test.bash" bash tests/session_start_test.bash
run_check "wrapper_test.bash" bash tests/wrapper_test.bash
run_check "schema-check.sh" bash script/schema-check.sh
run_check "web tests" sh -c 'cd web && pnpm test'
run_check "web typecheck" sh -c 'cd web && pnpm typecheck' 

# web/embed.go bakes web/dist into the binary with go:embed all:dist, so the
# release carries whatever is in dist at this moment. pnpm build only adds, it
# never removes, so stale chunks pile up and the HTML can end up pointing at an
# old generation: on 2026-08-28 v0.58.0 shipped 174 files with 20 StateMessage
# generations and the browser kept using the pre-WebSocket one.
#
# Clearing dist before the build is what fixes it, and clearing is the whole fix:
# once dist starts empty, a stale generation cannot survive the build, so there is
# nothing left for a separate check to catch. Keep .gitkeep, because go:embed
# rejects an empty directory (see the comment in web/embed.go); the -not below is
# what keeps it. Keep the clear before the build, not after -- reversed, it would
# delete the output and ship a binary with no UI.
echo "==> web"
find web/dist -mindepth 1 -not -name '.gitkeep' -delete
( cd web && pnpm build >/dev/null )

echo "==> bump"
python3 - "$version" <<'PY'
import json, pathlib, sys
version = sys.argv[1]
manifest_paths = [
    pathlib.Path(".claude-plugin") / "plugin.json",
    pathlib.Path(".codex-plugin") / "plugin.json",
]
manifest_data = [(path, json.loads(path.read_text())) for path in manifest_paths]
previous_versions = {data["version"] for _, data in manifest_data}
if len(previous_versions) != 1:
    raise SystemExit(
        f"plugin manifests have different versions: {sorted(previous_versions)}"
    )
previous = next(iter(previous_versions))

# Check before writing anything. This used to bump the manifests first, so a
# failure here left them on the new version while the hooks kept the old one,
# and the next run stopped on a dirty tree instead of on the real problem.
codex_hooks_path = pathlib.Path("hooks") / "codex-hooks.json"
codex_hooks = codex_hooks_path.read_text()
marker = "required=" + previous
# One marker per hook command. The count used to be hardcoded at 2; 468509d
# added a third hook without touching it, and every release since then stopped
# here.
expected = codex_hooks.count('"type": "command"')
found = codex_hooks.count(marker)
if found != expected:
    raise SystemExit(
        f"Codex hooks carry {found} `{marker}` markers, expected one per hook command ({expected})"
    )

for path, data in manifest_data:
    data["version"] = version
    path.write_text(json.dumps(data, indent=2) + "\n")
codex_hooks_path.write_text(codex_hooks.replace(marker, "required=" + version))

PY
go build ./...
bash tests/wrapper_test.bash >/dev/null

echo "==> commit and tag"
git add ".claude-plugin"/plugin.json ".codex-plugin"/plugin.json hooks/codex-hooks.json
git -c commit.gpgsign=false commit -q -m "chore: bump to $version"
git -c commit.gpgsign=false tag -a "v$version" -m "v$version"

echo "==> push"
git push origin main
git push origin "v$version"

echo "==> goreleaser"
GITHUB_TOKEN="$(gh auth token)" goreleaser release --clean

echo "==> publish"
gh release edit "v$version" --draft=false

echo "==> plugin"
claude plugin update atct@atct

echo "==> done. now: atct daemon stop && atct daemon start, then re-arm the watch"
echo "==> After the replacement, each space calls atct_project_claim. No release first: a claim whose lease stopped being renewed is stale and is taken over by the claim itself."
