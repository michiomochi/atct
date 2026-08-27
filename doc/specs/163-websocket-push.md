# ゴール 163: ブラウザ側の push を WebSocket にする

SSE は HTTP レスポンスなので、ブラウザが 1 オリジンに課す HTTP/1.1 の 6 接続枠を
1 本ずつ占める。タブを 6 枚開くと枠が尽き、画面が黙って古くなり、7 枚目以降は開かない。
WebSocket はこの枠の外にある。ブラウザの購読だけを WebSocket に移す。

## 1. 前提の実測（2026-08-27）

**方針の根拠は「Chromium は WebSocket を別枠で管理している
（`kMaxWebSocketsPerHost = 255`）」という理解だが、誰も測っていなかった。**先に測った。

計測用に捨てるエンドポイント 2 本（`/api/__measure/page` と `/api/__measure/ws`）を
daemon に足し、1 タブ = WebSocket 1 本の形で 8 枚開いた。**判定は daemon 側の `lsof` で行った**
——Chrome 側は閉じたソケットを残すため、Chrome で数えると測り違う。

```
$ script/measure-sse-connections.sh --tabs 8 \
    --url http://127.0.0.1:18163/api/__measure/page --fetch-probe --wait 2 --hold 8

tab=1 接続数=2   tab=5 接続数=7
tab=2 接続数=4   tab=6 接続数=8
tab=3 接続数=5   tab=7 接続数=9
tab=4 接続数=6   tab=8 接続数=10

=== hold=8s（4 分割） ===
t=2s 接続数=10 読み込めた=8 / 8
t=8s 接続数=10 読み込めた=8 / 8

=== daemon 側から見た接続の突き合わせ ===
daemon 接続数=10 ports=59349 59350 59356 59357 59392 59396 59425 59431 59433 59435
daemon 照合=一致

pages=8 target_pages=8 empty_titles=0

=== 各タブ内から fetch('/api/inbox') ===
tab=1 ok 3ms   tab=3 ok 3ms   tab=5 ok 4ms   tab=7 ok 5ms
tab=2 ok 2ms   tab=4 ok 4ms   tab=6 ok 4ms   tab=8 ok 5ms
```

ゴール 158 が記録した SSE の RED と並べる。

|            | 長寿命接続 | 開いた | タブ内 `fetch('/api/inbox')` |
|------------|-----------|--------|------------------------------|
| SSE 8 枚   | **6 本で頭打ち** | 6/8 | **0/8。TIMEOUT 8394ms** |
| WS 8 枚    | **10 本**  | 8/8 | **8/8。2-5ms**           |

**6 本の枠は WebSocket に及んでいない。**しかも 10 本を保持したまま HTTP の `fetch` が
2-5ms で返っている——**HTTP のプールが食い潰されていない**ことが、接続数とは独立に確認できた。
`daemon 照合=一致` は daemon 側が本当に握っていることを示す（片側だけ残るなら不一致になる）。

**計測器はこの実測のためだけのもので、product に残さない。**

## 2. 依存の選択: `github.com/coder/websocket` v1.8.15

直接依存を 4 つから 5 つに増やす判断である。

| 候補 | 判定 |
|------|------|
| 標準ライブラリだけで書く | **却下。**フレーミング・マスキング・ping/pong・close の扱いを自前で持つことになる |
| `github.com/gorilla/websocket` | **却下。**archived。新規には選ばない |
| `github.com/coder/websocket` | **採用** |

採用の理由:

```
$ go list -deps -f '{{.Module}}' github.com/coder/websocket | grep -v '^<nil>$' | sort -u
github.com/coder/websocket
```

**推移的依存が 0。**標準ライブラリ以外に何も引かない。`net/http` に馴染む API
（`websocket.Accept` / `Conn.Read` / `Conn.Write`、context 駆動）で、
フレーミングを自分で持たずに済む。

ゴール 114（プラグインを入れるだけで動く）の方向に反しない。**Go の依存はバイナリに
入るため、利用者の手間は 1 つも増えない**——`script/cdp-eval.py` が要求する Python の
`websockets` のような、宣言されていない実行時依存とは性質が違う。

## 3. 設計

### サーバ: `/api/ws` を足す。`/api/events` は消さない

**購読側は 2 つあり、独立している。**

```
cmd/atct/watch.go    /api/events を bufio.Scanner で読む   エージェントへの通知
web/src/lib/api.ts   ブラウザ                              -> WebSocket へ移す
```

`atct watch` には症状が出ていないので直す対象が無い。**`/api/events` を消すと
エージェントへの通知が止まる。**ブラウザが使わなくなるだけである。

push 経路は 2 系統になるが、**イベントの発生源は 1 つ**（`internal/store/notify.go` の
`publishEvent`）なので、分岐は出口だけに収まる。

**`project_id` / `goal_id` のフィルタ判定は共有ヘルパ（`parseEventFilter` /
`eventPasses`）へ切り出し、SSE と WebSocket の両方から呼ぶ。**同じ判定を 2 箇所に持つと、
片方だけ直る壊れ方が必ず来る。

フレームは JSON テキスト 1 通 = 1 イベント。

```json
{"name": "decision.created", "data": { ... }}
```

SSE の `event:` 行と `data:` 行が運んでいた 2 つの値を 1 フレームに畳む。

`parseEventFilter` は `websocket.Accept` より **前** に呼ぶ。不正な `project_id` を
通常の HTTP 400 で返すためである。Accept の後だと close コードでしか返せず、
`/api/events` と挙動が食い違う。

`websocket.Accept` のオプションは既定（`nil`）にする。**既定は Origin と Host の一致を
要求する。**他サイトのページが localhost の daemon へ繋ぐのを防ぐ境界なので外さない。
計測用エンドポイントだけは `InsecureSkipVerify: true` にした——計測と無関係な
失敗の面を 1 つ減らすためであり、product には持ち込まない。

サーバから ping は打たない。daemon が 30 秒ごとに `keepalive` イベントを publish しており、
それがそのままフレームとして流れる。生存確認を二重に持たない。

### ブラウザ: 境界を 1 文字も変えない

```ts
export function subscribeToDecisionEvents(onEvent: (name: DecisionEventName) => void): () => void
```

**署名も、呼び手 3 つ（`Dashboard.tsx` / `GoalDetail.tsx` / `TaskDetailPage.tsx`）も
変えない。**WebSocket はイベント名をフレームに載せられるので、`onEvent(name)` を残せる。

維持するもの:

- `refreshDebounce = 100`（連続イベントを 1 回の再取得にまとめる）
- 90 秒無音での張り直し（半死の接続に対する保険。daemon は 30 秒ごとに `keepalive` を送る）
- `keepalive` では `onEvent` を呼ばない。`lastEventAt` だけ更新する
- `isDecisionEventName` による判定。知らない名前で再取得しない

`close` イベントで 5 秒後に張り直す。**指数バックオフは入れない**——挙動が変わったときに
今回の変更のせいなのかバックオフのせいなのかが分からなくなる。`error` では張り直さない
（ブラウザは `error` の直後に必ず `close` を出すので、両方でやると二重接続になる）。

## 4. 範囲外

- `atct watch` の再接続ログ（`connection unavailable; reconnecting in 5s`）。
  **人間が「測らなくてよい」と判断した（2026-08-26）。**実害は観測されていない
- `script/cdp-eval.py` が要求する Python の `websockets` を宣言する件。
  このゴールでは扱わない
- `eventBus.ts`。ゴール 158 のブランチにある 3 本は main に入っていないので、
  **この worktree には存在しない。**消すものが無い
