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

echo "==> tests"
go build ./...
go test -count=1 -timeout 600s ./... >/dev/null
bash tests/cache_prune_test.bash >/dev/null
bash tests/wrapper_test.bash >/dev/null
( cd web && pnpm test >/dev/null && pnpm typecheck >/dev/null )

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
for path, data in manifest_data:
    data["version"] = version
    path.write_text(json.dumps(data, indent=2) + "\n")

resolve = pathlib.Path("bin/_resolve")
text = resolve.read_text()
if previous not in text:
    raise SystemExit(f"{resolve} does not mention {previous}")
resolve.write_text(text.replace(previous, version))
PY
go build ./...
bash tests/wrapper_test.bash >/dev/null

echo "==> commit and tag"
git add ".claude-plugin"/plugin.json ".codex-plugin"/plugin.json bin/_resolve
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
