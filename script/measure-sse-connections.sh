#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
使い方: script/measure-sse-connections.sh [--tabs N] [--url URL] [--port CDP_PORT]
                                          [--wait SEC] [--hold SEC]
                                          [--events PROJECT_ID] [--fetch-probe]
既定:   --tabs 8 --url http://127.0.0.1:8787/ --port 9333 --wait 4 --hold 0

--events PROJECT_ID  全枚数を開いたあと POST /api/goals で 1 回だけ鳴らし、
                     各タブの document.body.textContent に目印が入ったかを出す。
                     **innerText は headless の背面タブで途中までしか返らないので使わない**
--fetch-probe        各タブ内から fetch('/api/inbox') を打ち、所要時間を出す。
                     SSE が接続を使い切っていると、ここが返らなくなる

再現の条件: HEAD が 5763f09 以降であること。それ以前は -listen が塞がっていると
ポートをずらすので、隔離したつもりで稼働 daemon に当たりうる。
EOF
}

die() {
  echo "$*" >&2
  exit 1
}

tabs=8
base_url='http://127.0.0.1:8787/'
cdp_port=9333
wait_seconds=4
hold_seconds=0
events_project=''
fetch_probe=0

while (($# > 0)); do
  case "$1" in
    --tabs)
      (($# >= 2)) || { usage >&2; exit 2; }
      tabs="$2"
      shift 2
      ;;
    --url)
      (($# >= 2)) || { usage >&2; exit 2; }
      base_url="$2"
      shift 2
      ;;
    --port)
      (($# >= 2)) || { usage >&2; exit 2; }
      cdp_port="$2"
      shift 2
      ;;
    --wait)
      (($# >= 2)) || { usage >&2; exit 2; }
      wait_seconds="$2"
      shift 2
      ;;
    --hold)
      (($# >= 2)) || { usage >&2; exit 2; }
      hold_seconds="$2"
      shift 2
      ;;
    --events)
      (($# >= 2)) || { usage >&2; exit 2; }
      events_project="$2"
      shift 2
      ;;
    --fetch-probe)
      fetch_probe=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "不明な引数: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ "$tabs" =~ ^[1-9][0-9]*$ ]] || die "--tabs は 1 以上の整数で指定してください: $tabs"
[[ "$cdp_port" =~ ^[1-9][0-9]*$ ]] || die "--port は 1 以上の整数で指定してください: $cdp_port"
[[ -n "$base_url" ]] || die "--url は空にできません"
[[ "$wait_seconds" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "--wait は 0 以上の秒数で指定してください: $wait_seconds"
[[ "$hold_seconds" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "--hold は 0 以上の秒数で指定してください: $hold_seconds"

chrome='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
[[ -x "$chrome" ]] || die "Chrome が見つかりません: $chrome"

cdp_base="http://127.0.0.1:${cdp_port}"

# lsof で数える対象は --url から導く。固定にすると --url を変えたときに
# 別のポートを数え続け、測り違いが黙って通る。
target_port="$(python3 -c '
import sys
from urllib.parse import urlsplit
parts = urlsplit(sys.argv[1])
print(parts.port or (443 if parts.scheme == "https" else 80))
' "$base_url")"
[[ "$target_port" =~ ^[1-9][0-9]*$ ]] || die "--url からポートを導けませんでした: $base_url"
profile_dir="$(mktemp -d /private/tmp/atct-measure-sse.XXXXXX)"
chrome_pid=''

profile_pids() {
  pgrep -f "$profile_dir" 2>/dev/null || true
}

connection_ports() {
  local pid

  while IFS= read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    {
      lsof -nP -iTCP:"$target_port" -a -p "$pid" 2>/dev/null || true
    } | awk -v pt=":$target_port" '/ESTABLISHED/ && index($9, "->127.0.0.1" pt) > 0 {print $9}' \
      | sed -E 's/^127\.0\.0\.1:([0-9]+)->.*/\1/'
  done < <(profile_pids)
}

daemon_connection_rows() {
  local expected_port

  while IFS= read -r expected_port; do
    [[ -n "$expected_port" ]] || continue
    lsof -nP -iTCP:"$target_port" 2>/dev/null \
      | awk -v pp=":$expected_port" '$1 ~ /atct/ && index($0, pp)>0 {print "daemon fd", $4, $9}'
  done <<< "$1"
}

count_ports() {
  if [[ -z "$1" ]]; then
    echo 0
  else
    printf '%s\n' "$1" | awk 'NF { count += 1 } END { print count + 0 }'
  fi
}

format_ports() {
  if [[ -n "$1" ]]; then
    printf '%s\n' "$1" | tr '\n' ' '
  fi
}

loaded_page_count() {
  local current_pages

  current_pages="$(curl -s -m 5 "$cdp_base/json/list")"
  python3 -c '
import json
import sys
from urllib.parse import urlsplit

base = urlsplit(sys.argv[1])
loaded = 0
for page in json.load(sys.stdin):
    if page.get("type") != "page":
        continue
    current = urlsplit(page.get("url", ""))
    if (current.scheme, current.netloc) != (base.scheme, base.netloc):
        continue
    if page.get("title", ""):
        loaded += 1
print(loaded)
' "$base_url" <<< "$current_pages"
}

cleanup() {
  local pid
  local pids

  pids="$(profile_pids)"
  if [[ -n "$pids" ]]; then
    while IFS= read -r pid; do
      [[ "$pid" =~ ^[0-9]+$ ]] || continue
      kill "$pid" 2>/dev/null || true
    done <<< "$pids"
  elif [[ -n "$chrome_pid" ]]; then
    kill "$chrome_pid" 2>/dev/null || true
  fi

  for _ in 1 2 3 4 5; do
    pids="$(profile_pids)"
    [[ -z "$pids" ]] && break
    sleep 0.2
  done
  if [[ -n "$pids" ]]; then
    while IFS= read -r pid; do
      [[ "$pid" =~ ^[0-9]+$ ]] || continue
      kill -KILL "$pid" 2>/dev/null || true
    done <<< "$pids"
  fi

  rm -rf "$profile_dir"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

"$chrome" \
  --user-data-dir="$profile_dir" \
  --no-first-run \
  --no-default-browser-check \
  --disable-extensions \
  --headless=new \
  --remote-debugging-port="$cdp_port" \
  about:blank \
  >"$profile_dir/chrome.log" 2>&1 &
chrome_pid=$!

cdp_version=''
for ((attempt = 1; attempt <= 40; attempt++)); do
  if cdp_version="$(curl -s -m 1 "$cdp_base/json/version")" && [[ -n "$cdp_version" ]]; then
    break
  fi
  cdp_version=''
  sleep 0.25
done
[[ -n "$cdp_version" ]] || die "CDP が起動しませんでした: $cdp_base"
printf 'CDP: %s\n' "$(printf '%s' "$cdp_version" | tr -d '\n')"

for ((tab = 1; tab <= tabs; tab++)); do
  if [[ "$base_url" == *\?* ]]; then
    tab_url="${base_url}&tab=${tab}"
  else
    tab_url="${base_url}?tab=${tab}"
  fi

  curl -s -m 5 -X PUT "$cdp_base/json/new?$tab_url" >/dev/null
  sleep "$wait_seconds"

  ports="$(connection_ports | sort -nu)"
  connection_count="$(count_ports "$ports")"
  port_list="$(format_ports "$ports")"
  printf 'tab=%s 接続数=%s ports=%s\n' "$tab" "$connection_count" "$port_list"
done

if awk -v seconds="$hold_seconds" 'BEGIN { exit !(seconds > 0) }'; then
  hold_step="$(awk -v seconds="$hold_seconds" 'BEGIN { print seconds / 4 }')"
  printf '=== hold=%ss（4 分割） ===\n' "$hold_seconds"
  for ((sample = 1; sample <= 4; sample++)); do
    sleep "$hold_step"
    ports="$(connection_ports | sort -nu)"
    connection_count="$(count_ports "$ports")"
    loaded_count="$(loaded_page_count)"
    hold_elapsed="$(awk -v seconds="$hold_seconds" -v sample="$sample" 'BEGIN {
      elapsed = seconds * sample / 4
      if (elapsed == int(elapsed)) printf "%d", elapsed
      else printf "%.3f", elapsed
    }')"
    printf 't=%ss 接続数=%s 読み込めた=%s / %s\n' \
      "$hold_elapsed" "$connection_count" "$loaded_count" "$tabs"
  done
fi

ports="$(connection_ports | sort -nu)"
printf '%s\n' '=== daemon 側から見た接続の突き合わせ ==='
daemon_rows="$(daemon_connection_rows "$ports")"
if [[ -n "$daemon_rows" ]]; then
  printf '%s\n' "$daemon_rows"
fi
daemon_ports="$(printf '%s\n' "$daemon_rows" \
  | sed -nE 's/.*->127\.0\.0\.1:([0-9]+).*/\1/p' \
  | sort -nu)"
daemon_connection_count="$(count_ports "$daemon_ports")"
daemon_port_list="$(format_ports "$daemon_ports")"
printf 'daemon 接続数=%s ports=%s\n' "$daemon_connection_count" "$daemon_port_list"
if [[ "$daemon_ports" == "$ports" ]]; then
  echo 'daemon 照合=一致'
else
  echo 'daemon 照合=不一致'
fi

printf '%s\n' '=== CDP から見た各タブの状態 (type=page) ==='
page_list="$(curl -s -m 5 "$cdp_base/json/list")"
python3 -c '
import json
import sys
from urllib.parse import urlsplit

base = urlsplit(sys.argv[1])
page_count = 0
target_page_count = 0
empty_title_count = 0
for page in json.load(sys.stdin):
    if page.get("type") != "page":
        continue
    page_count += 1
    title = page.get("title", "")
    url = page.get("url", "")
    print("page={} title={} url={}".format(
        page_count,
        json.dumps(title, ensure_ascii=False),
        json.dumps(url, ensure_ascii=False),
    ))
    current = urlsplit(url)
    if (current.scheme, current.netloc) == (base.scheme, base.netloc):
        target_page_count += 1
        if title == "":
            empty_title_count += 1
print("pages={} target_pages={} empty_titles={}".format(
    page_count, target_page_count, empty_title_count,
))
' "$base_url" <<< "$page_list"

cdp_eval() {
  python3 "$(dirname "$0")/cdp-eval.py" "$cdp_port" "127.0.0.1:$target_port" "$1"
}

if ((fetch_probe)); then
  printf '%s\n' "=== 各タブ内から fetch('/api/inbox')。ページ側で 8 秒で打ち切る ==="
  cdp_eval "(async()=>{const t0=Date.now();const r=await Promise.race([fetch('/api/inbox').then(()=>'ok'),new Promise(x=>setTimeout(()=>x('TIMEOUT'),8000))]);return r+' '+(Date.now()-t0)+'ms';})()"
fi

if [[ -n "$events_project" ]]; then
  marker="MEASURE-$$-$(git rev-parse --short HEAD 2>/dev/null || echo nogit)"
  printf '=== イベントを 1 回鳴らす（marker=%s）===\n' "$marker"
  curl -s -X POST "http://127.0.0.1:${target_port}/api/goals" \
    -H 'Content-Type: application/json' \
    -d "{\"project_id\":\"${events_project}\",\"content\":\"${marker}\\n\\n計測が鳴らした目印\",\"creator\":\"human\"}" \
    -o /dev/null -w 'POST /api/goals http=%{http_code}\n'
  sleep 12
  printf '%s\n' '=== 各タブの DOM に marker が届いたか（textContent） ==='
  cdp_eval "document.body.textContent.includes('${marker}')"
fi
