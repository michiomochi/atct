# MCP を daemon の HTTP で配る

2026-08-24。人間の決定:「B で進めて」。B は notion 型、すなわち `.mcp.json` が
`"type": "http", "url": ...` だけを書き、プロセスを起動しない形である。

## なぜこの形にするか

実測（2026-08-24・0.47.0）。手書きの `[mcp_servers.atct]` を外して新しい Codex
セッションを立てると、**フックとスキルは動くのに MCP のツールが 0 個**になった。

```
hook    ○  ATCT: project atct / ... / commander 0c827a1e…
skill   ○  atct:start が見える
MCP     ✗  atct 名のツールが 0 個
```

原因は `.mcp.json` の 1 行である。

```
atct              "command": "${CLAUDE_PLUGIN_ROOT}/bin/atct-mcp"
```

`mcpServers` キー自体は Codex が対応している。公式の `codex-app-tools` と `notion` が
同じ宣言を持つ。**違うのは中身であった。**

```
codex-app-tools  "command": "./scripts/launch_...", "cwd": ".",
                 "env_vars": ["CODEX_CLI_PATH", "HOME", "PATH", ...]
notion           "type": "http", "url": "https://mcp.notion.com/mcp"
```

**superpowers は参考にならない。**MCP サーバーを持たないためである（`.mcp.json` が無く、
`mcpServers` の宣言もない）。「入れただけで動いた」のはスキルとフックで、それは atct でも
既に動いている。

### http 型が優れている点

```
              codex-app-tools 型          notion 型
PATH          不要                        不要
env_vars      要る（HOME, PATH …）         不要
パス変数       ハーネスごとに違う             不要
ファイル数     Claude 用 + Codex 用          1 つ
```

`command` そのものが無くなるので、**パス変数の問題が根元から消える。**これで 3 つの
課題が同時に片付く。

```
task 537  Codex で MCP が入らない（${CLAUDE_PLUGIN_ROOT} が展開されない）
task 539  直下の .mcp.json が /bin/atct-mcp という存在しないパスを登録する
task 531  手書きの [mcp_servers.atct] と atct-mcp-newest を消せない
```

`task 539` は `task 534`（プラグインを直下へ移した）が生んだ退行である。プラグイン外では
`${CLAUDE_PLUGIN_ROOT}` が空になり、`command` が `/bin/atct-mcp` になる。**このリポジトリを
Claude Code で開いた者に、起動しない MCP サーバーが登録される。**http 型ならこの行自体が無い。

## 最大の懸念は実測で消えた

`type: http` は **daemon が既に起きていないと繋がらない。**いまは `atct-mcp` が daemon を
起こす作りなので、順序が逆になる。**これが唯一の未知であった。**

### 実測 1: Claude は自動で再接続しない

届かない `http://127.0.0.1:18787/mcp` を宣言して起動した。

```
ConnectionRefused
retry 1/3 (1000ms)
retry 2/3 (2000ms)
retry 3/3 (4000ms)
```

**最終試行のあとにサーバーを起こし、15 秒以上待ったが HTTP リクエストは 0 件。**
自動再試行はしない。**手動で `/mcp` から Reconnect したときだけ** `POST /mcp` が飛んだ。

**しかし同時に、約 7 秒の窓があることも分かった。**「絶対に繋がらない」ではない。

### 実測 2: フックは初回接続には間に合わないが、retry には間に合う

使い捨てのプロジェクトで、フックに時刻を出させて測った。

```
MCP 初期化       16:30:27.818
hook_start       16:30:27.832    +14 ms
hook_end         16:30:27.834    フック自体は 2.7 ms
初回 Refused     16:30:27.847    +29 ms
retry 1          16:30:28.350    +532 ms
retry 2          16:30:29.854    +2,036 ms
```

**「フックが初回接続より先」は成り立たない。**MCP の初期化が 14 ms 早い。
**しかしフックは最初の retry の 515 ms 前に終わっている。**

### 実測 3: daemon は 0.083 秒で起きる

冷えた `HOME` から 3 回。毎回 `rm -rf` してから測り、`daemon ready` の行を確認した。

```
1 回目  atct daemon ready: pid 81747, http 127.0.0.1:18891    0.080 秒
2 回目  atct daemon ready: pid 81755, http 127.0.0.1:18892    0.078 秒
3 回目  atct daemon ready: pid 81763, http 127.0.0.1:18893    0.083 秒
```

**最悪値 0.083 秒。**

### したがって成立する

```
MCP 初期化                     T
フック開始                     T + 14 ms
daemon 起動完了                T + 97 ms      （14 + 83）
retry 1                        T + 532 ms     ← ここで繋がる
```

**435 ms の余裕がある。**retry 2（+2.0 秒）と retry 3（約 +7 秒）はさらに余裕がある。

## 決定 1: フックが daemon を起こす

`hooks/session-start` に `daemon start` を足す。**いまのフックは daemon を起こしていない**
——`atct context -brief` は DB を直接開くためである。

**daemon は長命なので、これが効くのは再起動後や daemon が死んだ後の最初のセッションだけ**
である。2 回目以降は既に起きている。

### 失敗したときに何が起きるか

`daemon start` が 435 ms を超えた場合、そのセッションは MCP を失う。**手動で `/mcp` から
Reconnect すれば回復する**（実測 1）。**黙って壊れるのではなく、回復手段がある。**

フックが走らない環境（プラグインを入れていない場合）では daemon が起きない。
**そこは元から atct が動かない環境なので、退行ではない。**

## 決定 2: セッション識別は `Mcp-Session-Id` に載せる

```go
cmd/atct-mcp/main.go:55   agentSessionID := uuid.NewString()   // プロセスごとに 1 つ
```

**claim も役割もすべてこれに乗っている。**「1 つの MCP プロセス = 1 つのエージェント
セッション」が現在の模型である。

共有エンドポイントでは全員が同じ daemon に来るので、**接続ごとに ID を配る必要がある。**
MCP の streamable HTTP は `initialize` の応答でサーバーが `Mcp-Session-Id` を割り当てる
仕組みを持つ。**いまの模型にそのまま対応づく**——プロセス 1 つに 1 つ発行していたものを、
接続 1 つに 1 つ発行するだけである。

**`agent_sessions` を UUID のまま残す判断（`goal 111`）と整合する。**

## 決定 3: stdio を壊さない

`cmd/atct-mcp` は Claude で稼働中である。**HTTP を足すのであって、stdio を置き換えない。**
両方が同じ `internal/mcpshim` の登録を使う。

**検査で固める。**stdio が壊れていないことを、HTTP を足したあとに示す。

## 決定 4: 完了条件は実測とする

**宣言を書いただけで完了にしない。**commander は `4134ba3` で
`"mcpServers": "./.mcp.json"` を書き、**実物で確かめずに完了とした。**それが動いていない
ことが今日判明した。同じことを繰り返さない。

```
(1) 手書きの [mcp_servers.atct] が無い状態で、新しい Codex セッションに 17 個見える
(2) 同じ状態で新しい Claude セッションでも 17 個見える
(3) 両方で atct_role が正しい役割を返す
(4) 両方が違うセッションとして扱われる（片方の claim がもう片方を拒む）
```

## 合わせて効くもの

`task 538`（固定の文言をフックから MCP の `instructions` へ移す）と組み合わせると、
**フックの役割が `daemon start` だけになる。**

```
plugin/hooks/session-start（現在）
   1 行   $ATCT_BIN context -brief     動的
  12 行   固定の文言                    ← MCP の instructions が本来の置き場
```

`cmd/atct-mcp/main.go` は `mcp.NewServer` に `Instructions` を一度も設定していない
（grep で 0 件）。**この仕組みが効くことは確認済み**——commander 自身のコンテキストに
`# MCP Server Instructions` として他のサーバーの文言が入っている。

## URL が 8787 に固定であることについて

**`.mcp.json` は静的な JSON で、`~/.atct/daemon.json` を読めない。**したがって URL は
固定になる。**しかし daemon は 8787 に固定されていない。**

```
cmd/atct/main.go:431
for port := defaultListenPort; port <= lastListenPort; port++    // 8787 → 8796
```

8787 が塞がっていれば 8788 へ移り、**プラグインは 8787 を叩いて何も見つけられない。
しかも黙って失敗する**（retry 3 回で諦め、ツールが 0 個になるだけ）。2026-08-24 に
孤児プロセスが 8788 を掴んでいた実例がある。

**当面は、フックが実際のアドレスを見て食い違いを警告する。**

```
ATCT warning: daemon is listening at 127.0.0.1:8788;
MCP endpoint is fixed at http://127.0.0.1:8787/mcp.
```

**恒久策は決めていない。**決定 `decision 245` で人間に問うている。動的な解決を入れるなら、
起動役を挟むか、別の設定機構が要る。

## 未検証

- **Codex が後発起動を自動で拾うか。**偽サーバー停止中に `rmcp ... http/request failed` が
  6 回出たあと `READY` で完了することは測った。**後から起こした場合は測っていない。**
  Claude と同じなら問題ないが、**確かめるまでは同じと仮定しない**
- **daemon が死んだとき、接続済みのセッションが再接続するか。**設計判断に効かないので
  今回は測っていない。運用上は知りたい
- **`127.0.0.1:8787` が塞がっているときの振る舞い。**別のプロセスが掴んでいる場合、
  `daemon start` は失敗する。今日そのような孤児プロセスを実際に見ている
- **複数プロジェクトを跨ぐとき。**daemon は 1 台で、`cwd` はツール呼び出しごとに渡る。
  HTTP でも同じ形が保てるはずだが、確かめていない
