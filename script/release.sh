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

version="${1:?usage: script/release.sh <version>   e.g. 0.26.0}"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "version must look like 1.2.3, got: $version" >&2
  exit 1
fi

current="$(python3 -c 'import json;print(json.load(open("plugin/.claude-plugin/plugin.json"))["version"])')"
echo "==> $current -> $version"

if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
  echo "the working tree has uncommitted changes; commit them first" >&2
  git status --short >&2
  exit 1
fi

echo "==> tests"
go build ./...
go test -count=1 -timeout 600s ./... >/dev/null
bash tests/wrapper_test.bash >/dev/null
( cd web && pnpm test >/dev/null && pnpm typecheck >/dev/null )

echo "==> bump"
python3 - "$version" <<'PY'
import json, pathlib, sys
version = sys.argv[1]
path = pathlib.Path("plugin/.claude-plugin/plugin.json")
data = json.loads(path.read_text())
previous = data["version"]
data["version"] = version
path.write_text(json.dumps(data, indent=2) + "\n")

resolve = pathlib.Path("plugin/bin/_resolve")
text = resolve.read_text()
if previous not in text:
    raise SystemExit(f"{resolve} does not mention {previous}")
resolve.write_text(text.replace(previous, version))
PY
go build ./...
bash tests/wrapper_test.bash >/dev/null

echo "==> commit and tag"
git add plugin/.claude-plugin/plugin.json plugin/bin/_resolve
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
