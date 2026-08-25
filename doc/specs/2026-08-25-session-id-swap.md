# セッション ID が途中で入れ替わる（2026-08-25 の調査）

**タスク `7b194d4e` の成果物。**稼働版 0.54.0 で commander と subcommander の両方が
これで止まったため調べた。**判断ではなく観測の記録である。**未確定の点は末尾に残してある。

---

対象タスク: `7b194d4e-a5c9-402d-9206-b613227ec48e`

## 1. ID を作る経路の一覧

本番コードで `agentSessionID := uuid.NewString()` は次の 2 か所だけだった。

- `internal/daemon/server.go:59`: `mcp.NewStreamableHTTPHandler` に渡した `getServer` 関数の中で呼ぶ。HTTP の新規 MCP セッションごとに呼ばれうる。直後の `run.register` 呼び出しは同ファイル `:61`。
- `cmd/atct-mcp/main.go:55`: stdio shim の `main` で、プロセス起動ごとに 1 回呼ぶ。直後の `run.register` 呼び出しは同ファイル `:57`。

`run.register` の受け側は `internal/daemon/handler.go:260-268` で、ここでは ID を生成せず `RegisterAgentSession` に渡すだけである。別経路として `internal/daemon/handler.go:200-203` の `ensureAgentSessionProject` が、未登録 ID を見つけた場合に `RegisterAgentSession(..., 0)` を直接呼ぶ。ただしこれは UUID 生成でも `run.register` RPC 呼び出しでもない。

`internal/store/store.go:53-78` は登録時に pid と開始時刻を保存する。検索で見つかった他の `uuid.NewString()` は goal/project/decision/task/handoff 等の ID であり、agent session ID の生成ではない。

## 2. この pane が実際に通る経路

`.mcp.json:4-5` は `type: "http"`、URL は `http://127.0.0.1:8787/mcp` なので、この pane は `cmd/atct-mcp` の stdio 経路ではなく `internal/daemon/server.go:56-72` の HTTP 経路を使う。

`go.mod:25` の MCP SDK は `github.com/modelcontextprotocol/go-sdk v1.7.0`。SDK の `StreamableHTTPOptions` は nil options で stateful（zero value の `Stateless == false`）になり、後述の実装では既存 `Mcp-Session-Id` 付き POST は既存セッションを使い、ID なし POST だけが新規セッションを作る。このとき `server.go:59` の callback が実行され、同じ callback 内の `server.go:61` で登録される。

したがって、この pane で登録を増やす直接の条件は「MCP の新しい stateful セッションを開始する POST（典型的には初回 initialize、または再接続時に `Mcp-Session-Id` が無い POST）」である。同一 MCP セッションの通常の POST、GET、DELETE はこの callback を通らない。

候補ごとの判定:

- MCP 再接続: 可能。SDK の実装が、セッション ID なしの POST を新規セッションとして callback に渡すことを明記・実装している。
- 圧縮: 圧縮そのものが HTTP リクエストや新規セッションを発生させるコード上の根拠はない。今回の時刻に圧縮が再接続を誘発したかは分からない。
- フック: 今回確認した Claude Code の ATCT plugin hook は `startup|clear|compact` で `daemon start` を実行するだけで、直接 MCP POST や `run.register` は送らない。compact 周辺のクライアント再初期化に関与した可能性は残るが、今回の引き金だったかは分からない。
- CLI の別経路: この pane の `.mcp.json` からは該当しない。別プロセスが `cmd/atct-mcp` を起動すれば別行を作れるが、今回の pane がそれを起動した証拠はない。

## 3. StreamableHTTPHandler の関数がいつ呼ばれるか

`go/pkg/mod/github.com/modelcontextprotocol/go-sdk@v1.7.0/mcp/streamable.go` の実物:

```go
// The getServer function is used to create or look up servers for new
// sessions. (streamable.go:229-230)
```

stateful POST の実装は次の順である（`streamable.go:617-648`）。

```go
sessionID := req.Header.Get(sessionIDHeader)
if sessionID != "" {
    // lookup existing session and serve it
    ...
    return
}

// No session ID: create a new session.
server := h.getServer(req)
```

また `streamable.go:324-364` は各 HTTP リクエストを stateful/stateless の処理へ振り分けるだけで、constructor (`:232-249`) は callback を呼ばない。よって callback は「接続ごと」でも「リクエストごと」でもなく、stateful handler における「セッション ID の無い新規 POST ごと」である。

## 4. 21 ミリ秒差の 2 行の正体

稼働 DB は次でコピーし、コピーを読んだ。

```sh
sqlite3 ~/.atct/atct.db '.backup /tmp/atct-7b194d4e.db'
```

該当時刻のコピーからの実出力:

```text
id                                    project_id                            registered_at                         pid   started_at
29e20dac-b35c-46e6-91e9-e91c4bf8a005                                        2026-08-25T03:16:33.982555Z  2987  Tue Aug 25 12:12:41 2026
191db26e-bd9e-469b-871d-354b4fca53fb  1ff70f35-9060-45e9-8169-5952bd1032ef  2026-08-25T03:16:34.005595Z  2987  Tue Aug 25 12:12:41 2026

ffcf65d7-fb95-4f70-a1ae-951424bad970                                        2026-08-25T03:46:34.37743Z   2987  Tue Aug 25 12:12:41 2026
b4d5bc37-bbde-450b-b18e-1e54d415050c  1ff70f35-9060-45e9-8169-5952bd1032ef  2026-08-25T03:46:34.398962Z  2987  Tue Aug 25 12:12:41 2026
```

1 組目は約 23.040 ms、2 組目は約 21.532 ms 差で、各行は別 UUID である。両組とも pid と started_at が完全に同じで、最初の行は project 未関連、次の行は同じ project に関連している。

従って DB が示す範囲では、これは別プロセスの 2 行ではなく、同じ daemon PID から短時間に登録された 2 つの agent session 行である。追加照合では、当日 14 行中 project 付きはちょうど 4 行で、各々がこの形の組の 2 行目だった。project 無し 10 行のうち 6 行は単独で、残り 4 行が組の 1 行目である。project 付き 4 ID だけが goal/task handoff の参照先になっており、組の 1 行目は handoff 参照が 0 件だった。

このため、提示された pane 起動時刻と handoff の参照関係を合わせれば、「各組は 1 つの Claude Code pane の論理 session が作った 2 行」という説明が最有力である。ただし DB だけで物理的な同一 pane まで証明はできない。HTTP access log の client 接続情報、または pane/client ID が不足している。project_id の設定自体は登録元を示すものではなく、後続の project-aware 操作で関連付けられた結果である。

## 5. daemon が入れ替わりを知る手がかり

部分的な手がかりはあるが、旧 ID と新 ID の入れ替わりを確定する手がかりはない。

- `pid` と `started_at`: `store.go:58-63` は `processStartedAt(pid)` が成功したときだけ保存する。`server.go:63` は daemon 内で `os.Getpid()` を渡すため、HTTP 経路の複数 session が同じ daemon の pid/start 時刻になる。`claim_liveness.go:106-121` はその pid に `kill(pid, 0)` を行い、開始時刻が一致すれば running と判定する。これは daemon が生きていることは分かるが、同じ daemon 内のどの pane/session かは分からない。
- `project_id`: session の project scope は分かるが、今回の 2 行は同じ project であり、旧 ID と新 ID の対応関係は分からない。
- `registered_at`: 順序と近接時刻は分かる。2 行が別登録であることの証拠にはなるが、原因や pane の対応は分からない。
- UUID: ランダム生成値で、旧 ID から新 ID を導出する関係はない。

コピーの `agent_sessions` スキーマは `id, project_id, registered_at, pid, started_at` だけで、前身 ID、client/pane ID、登録元、replacement/alias の列はない。したがって daemon は「同じ daemon から近接して登録された」という異常候補は検出できても、旧 ID の handoff/claim を新 IDへ安全に移すべきだと自動確定することはできない。どう移すかは設計判断であり、ここでは決めない。

## 6. 分からなかったこと

- 2026-08-25 の各登録時刻に、実際にどの HTTP リクエスト（ヘッダー、client、pane）が来たかは DB に記録されていない。
- 21.532 ms の組（および 23.040 ms の組）が同一 pane の再接続か、複数 client の同時接続かは確定できない。
- 圧縮やフックが MCP 再接続を誘発したかは、この調査対象のソースと DB からは分からない。
- したがって、今回の ID 入れ替わりの「引き金」を圧縮・再接続・フック・別 CLI のいずれか 1 つに断定することはできない。

## 7. 追調査: 1 つの pane が 2 セッションを作ったと言えるか

コピーへの再集計結果は `total=14, without_project=10, with_project=4`。project 付き ID は `4e54dd75`, `191db26e`, `b4d5bc37`, `b449fb07` の 4 つで、すべて handoff の requested_by/received_by に現れる。組の 1 行目 `de1d99bb`, `29e20dac`, `ffcf65d7`, `c2927c84` は handoff 参照が 0 件だった。

旧 subcommander ID `191db26e` は goal/task handoff を使い、後続の `b4d5bc37` も同じ project で goal/task handoff を使っている。これは「同じ論理 pane の旧 ID → 新 ID」と強く整合するが、pane/client ID そのものではない。厳密な断定に必要なのは HTTP access log の接続元・MCP client ID・pane ID のいずれかである。

## 8. 追調査: Codex と Claude Code の設定差

`~/.codex/config.toml` のグローバル `[mcp_servers]` には `atct` の直接設定はない。しかし Codex の ATCT plugin manifest は `mcpServers: "./.mcp.json"` を指し、その `.mcp.json` は HTTP `http://127.0.0.1:8787/mcp` である。Claude Code も有効な `atct@atct` v0.54.0 plugin と同じ HTTP `.mcp.json` を持つ。

したがって、設定ファイルの transport 差では説明できない（実際には ATCT は両者とも HTTP）。Codex の単独行と Claude の組の差は、MCP client 実装・初回 POST/再接続のライフサイクル差なら説明可能だが、設定ファイルからは確定できず、クライアント側の HTTP trace が必要である。

## 9. 追調査: 30 分の定数・TTL・タイムアウト

- SDK v1.7.0: `StreamableHTTPOptions.SessionTimeout` は nil options では 0（idle session を閉じない）。client の `reconnectMaxDelay` は 30 秒で、30 分ではない。
- daemon: `internal/daemon/wakeup.go:18-20` に `30 * time.Minute` が 3 つあるが、handoff 未受領・未報告・claim 未委譲の detection 用。`runMaintenance` は 30 秒 ticker でイベントを評価・発行するだけで、session ID の生成・置換はしない。agent session retention は 30 日。
- Claude Code: active config に 30 分値は見つからなかった。`token_refresh_buffer_ms=1500000` は 25 分、`heartbeat_interval_ms=20000` は 20 秒。ATCT hook の `compact` matcher に固定 30 分はない。Codex config/hook にも 30 分値はない。

subcommander の登録間隔は `03:16:34.005595 → 03:46:34.398962` の `30:00.393367`、commander は約 50:00.696561 である。30 分定数は存在するが用途も基準時刻も一致せず、今回の ID swap の直接原因とは確認できなかった。
旧 goal handoff の `received_at` は `03:19:49.168453Z` であり、これを 30 分 detection の基準にすると `03:49:49.168453Z` になる。実際の subcommander swap `03:46:34.398962Z` とは一致しない。

## 7. 回復手順（commander が 3 回使ったもの）

**これは手順であって修理ではない。**直るまでは、止まったらこれで戻す。

役割が `executor` に戻っていることに気づいたら:

```
atct_project_release(<project_id>)   保持者でなくても通る（タスク 06435e48）
atct_project_claim(<project_id>)     → role が commander に戻る
```

**`release` を先に呼ぶ必要がある。**古い ID は pid が daemon のものなので永久に
生存扱いになり、`atct_project_claim` だけでは `project already claimed` で拒まれる。

subcommander に渡したゴールが同じ形で切れたときは、commander が:

```
atct_goal_handoff_complete(<goal_id>)          開いている受領を閉じる
atct_goal_handoff_request(<goal_id>, <新 ID>)  新しい handoff_id で出し直す
```

**~~subcommander は自力で戻れない。再発行には project claim が要るためである。~~**
**2026-08-25 に 0.56.0 で偽になった。**`atct_session_identify` を同じ鍵で呼べば
`reattached: true` で元の行に戻り、**subcommander が自分で復帰できる。**
w4B と w4C の両方が実測している（役割が `executor` に落ちた状態から、
identify 1 回で `subcommander` に戻り、handoff の再発行が通った）。
**commander への依頼は不要になった。**

**この節の 3 行の手順は、鍵で戻れないときだけ使う**——鍵を名乗る道具が
ツール一覧に無い場合（版が上がった直後のセッション）と、鍵を登録する前に
取った claim が残っている場合である。
**したがって subcommander には「役割が executor に戻ったら作業を止めて commander に
再発行を求めろ」と指示しておくこと。**気づかずに進むと、委譲だけが静かに失敗する。

### 観測された頻度（2026-08-25）

    commander      03:12 → 04:02（50 分） → 04:37（35 分） → 04:47（10 分）
    subcommander   03:16 → 03:46（30 分） → 04:47（61 分）

**周期ではない。**最後の 1 回は 3 つのセッションがほぼ同時に切れた。

## 8. これは生存判定の問題ではない（人間の指摘・2026-08-25）

**当初 commander は「生存の指標を `last_seen_at` に変える」を推奨し、決定 `f353c80d` として
出した。人間の指摘で取り下げた。**

    claim が記録したいこと   どのエージェントが持っているか
    実際に記録しているもの   どの HTTP セッションが持っているか

`agent_sessions.id` は `internal/daemon/server.go:59` で **daemon が transport のセッション
ごとに振る UUID** である。**エージェントは自分の識別子を選ばないし、知らない。**

したがって今日 5 回起きたのは生存判定の誤りではなく、**エージェントの同一性の喪失**である。
**生存の指標をどれだけ精密にしても、再接続で作られた行は別人のままで、claim は古い行に
取り残される。**

**先に決めるべきは同一性で、生存はその後である。**立て直した問いは決定 `8487cae1`。

### pid の話も同じ層に収まる

`os.Getpid()` は 2 か所にあり、**同じ 1 行が置かれた場所によって別のものを指す。**

    cmd/atct-mcp/main.go:57       stdio。1 セッション = 1 プロセス → そのエージェント。正しい
    internal/daemon/server.go:62  HTTP。daemon の中で走る        → daemon。意味が変わった

**型は同じ、意味だけが変わる。**コンパイルもテストも通る。CONTRIBUTING が sqlc を必須にした
理由（列を消したら `Scan` が古い位置を読み、実行時に壊れた）と同じ形である。

## 9. `pid 0` の 9 件は無関係だった（2026-08-24 の実測）

`446d87f0` の本文は候補③（pid を使わない）を検討するとき「いまの `pid 0` が 9 件あるのも
同じ形かもしれない。先に調べろ」と書いていた。**調べた結果、無関係である。**

```
99999999   2026-08-23   テスト用の作り物
11111111   2026-08-22   同上
92a9359a   2026-08-19   古いもの
0324358f   2026-08-19   同上
```

`run.register` を呼ぶのは 2 箇所だけで、**どちらも pid を渡している。**

```
cmd/atct-mcp/main.go:57        stdio 版。自分の pid
internal/daemon/server.go:61   HTTP 版。daemon の pid   ← これが欠陥
```

**`pid 0` は「渡されなかった」ではなく「作り物と古い残骸」である。**
候補③を検討するとき、既存の `pid 0` を根拠にしないこと。

（この記録は決定 `fea67f7a` に置かれていたが、**既定の無い決定がタスクを永久に
`done` にできなくする**ため、ここへ移して取り下げた。同じ形が 14 件ある——`8d53e68e` を参照。）

## 10. ペアの正体は未解明のまま（identify は原因でない）

**7 節の「21 ミリ秒差で 2 行できる組」は、`atct_session_identify` が入ったあとも出ている。**
w4C の subcommander の実測（2026-08-25、0.56.0）:

```
09:28:26.886108  2b5f8a7d  (鍵なし)
09:28:26.895201  c025bb27  (鍵なし)   ← +9ms。両方とも鍵なし
```

**identify が 1 度も関与していないのにペアができている。**したがって
「identify が孤児を作っている」という仮説は**否定される。**

`identify` は行を作らない。**transport の行の id で upsert し、その行に鍵を書く。**
同じ実測:

```
09:18:00.652494  bf866697  (鍵なし)                    ← pane 作成時
09:18:00.652494  bf866697  atct-419e0dff-executor      ← identify 後。同じ id、同じ registered_at
```

**例外は `reattached=true` の場合だけである。**鍵が既存の別行にあったとき、
canonical はそちらになり、transport の行は鍵なしで残る。**これは意図した設計**で
（手順を破って identify より前に atct を呼んでいた場合、その行が handoff から
参照されている可能性があるため消せない）、参照が無ければ 30 日の retention が掃除する。
**2026-08-25 時点で `reattached=true` は 1 度も観測されていない。**
（この記述は同日中に古くなった。11 節を参照。）

**何が 2 回登録しているかは依然として不明である。**`7b194d4e` の完了条件 (2) は未達。
**推測で埋めない。**

（commander は当初この節と逆の結論——identify が孤児を作る——を書いた。
コードの `RegisterAgentSession` を「行を作る」と読み違えたためで、
上の実測が否定した。）

## 11. `reattached=true` を実測した（dotfiles-commander・2026-08-25）

10 節の「1 度も観測されていない」は同日中に古くなった。**MCP 再接続を挟んだ実測で
`reattached=true` が返り、claim を取り直さずに役割が戻った。**

    atct_role(expected_role=commander)          matches:false  role:'executor'   project_id:''
    atct_session_identify('dotfiles-commander') reattached:true agent_session_id:0dac5e66
    atct_role(expected_role=commander)          matches:true   role:'commander'  project_id:4d20dc48

`agent_session_id` は 09:09 に鍵を登録したときと同一（`0dac5e66`）。**新しい行を作らず、
同じ行に戻っている。**

**効果の大きさは同日の対照で出た。**同じ space で、鍵を登録する前は
project claim を 3 回取り直し、goal handoff を 4 本発行している
（`handoff-dotfiles-0fe78eaf-01` から `-04`）。うち 2 回は MCP 再接続、1 回は daemon
入れ替えが原因である。**鍵を登録した後の再接続では 0 回。**
MCP 再接続 1 回あたり、持ち主側の 1 往復（`release` → `claim`）と
受け手側の 1 往復（`receive` のやり直し）が消える。

### 鍵を登録する前に取った claim は戻らない

**この非対称が手順の位置を決める。**戻ったのは鍵を登録した**あとに**取り直した claim
だけである。1 回失ってから鍵を登録しても、失った分は戻らない。

したがって `identify` は **`atct:start` の先頭**で呼ばれなければならない。
「役割がおかしくなったら identify を呼ぶ」という回復手順（7 節）だけでは、
**最初の 1 回の損失は防げない。**回復手順は残すが、それは 2 回目以降のためのものである。

