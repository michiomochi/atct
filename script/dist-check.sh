#!/usr/bin/env bash
set -euo pipefail

if (( $# != 1 )); then
  echo 'usage: script/dist-check.sh <dist-dir>' >&2
  exit 2
fi

dist_dir="$1"
if [[ ! -d "$dist_dir" ]]; then
  echo "$dist_dir is not a directory" >&2
  exit 1
fi
if [[ ! -f "$dist_dir/.gitkeep" ]]; then
  echo "$dist_dir/.gitkeep is missing; go:embed requires it" >&2
  exit 1
fi

python3 - "$dist_dir" <<'PY'
import pathlib
import re
import sys

dist = pathlib.Path(sys.argv[1]).resolve()
files = {
    path.relative_to(dist).as_posix()
    for path in dist.rglob("*")
    if path.is_file()
}
html = sorted(path for path in files if path.endswith(".html"))
assets = {
    path
    for path in files
    if not path.endswith(".html")
    and pathlib.PurePosixPath(path).name != ".gitkeep"
}

ABSOLUTE = re.compile(r"/_astro/([A-Za-z0-9._@-]+)")
RELATIVE_CHUNK = re.compile(
    r"[\"'(]\./([A-Za-z0-9._@-]+\.(?:js|css))[\"')]")
CSS_RELATIVE_URL = re.compile(
    r"url\(\s*[\"']?\./([A-Za-z0-9._@-]+)[\"']?\s*\)"
)


def references(path):
    text = (dist / pathlib.PurePosixPath(path)).read_text(
        encoding="utf-8", errors="replace"
    )
    found = {f"_astro/{name}" for name in ABSOLUTE.findall(text)}
    if path.endswith((".js", ".mjs", ".css")):
        parent = pathlib.PurePosixPath(path).parent
        found.update((parent / name).as_posix() for name in RELATIVE_CHUNK.findall(text))
    if path.endswith(".css"):
        parent = pathlib.PurePosixPath(path).parent
        found.update(
            (parent / name).as_posix() for name in CSS_RELATIVE_URL.findall(text)
        )
    return found


visited = set()
seen = set()
queue = list(reversed(html))
while queue:
    current = queue.pop()
    if current in visited:
        continue
    visited.add(current)
    if current not in files:
        continue
    for reference in references(current):
        seen.add(reference)
        if reference in files and reference not in visited:
            queue.append(reference)

reachable = sorted(assets & seen)
unreachable = sorted(assets - seen)
dangling = sorted(seen - files)

for line in (
    f"html roots      : {len(html)}",
    f"assets on disk  : {len(assets)}",
    f"reachable       : {len(reachable)}",
    f"UNREACHABLE     : {len(unreachable)}",
    f"DANGLING refs   : {len(dangling)}",
):
    print(line, file=sys.stderr)
if not html and assets:
    print(f"{dist} holds assets but no HTML entrypoint", file=sys.stderr)
for path in unreachable:
    print(f"  - {path}", file=sys.stderr)
for path in dangling:
    print(f"  ! {path}", file=sys.stderr)

sys.exit(1 if unreachable or dangling else 0)
PY
