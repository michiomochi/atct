# worker の sandbox は delegator の sandbox ではない

2026-08-27。ゴール 180「executor が構造的に止まる 2 件（報告経路が無い / httptest が
実行できない）」。**同じ壁を 3 回踏んでいる**（2026-08-22 の `daemon.Serve exited: bind:
operation not permitted`、ゴール 91、そして今日の検算）。

## 何が起きたか

### F1: worker は httptest を使うテストを実行できない

```
worker:     panic: httptest: failed to listen on a port:
            listen tcp6 [::1]:0: bind: operation not permitted

delegator:  同じ worktree・同じコマンド
            $ go test ./internal/httpapi -run '^Test...$' -count=1
            --- FAIL: ... read SSE: context deadline exceeded   <- bind は成功している
```

**同じコマンドが、実行する者によって通ったり通らなかったりする。**依頼書が
`go test ./...` と書くと、worker では必ず panic する。影響は `internal/httpapi` と
`cmd/atct`。

### F2: worker はブロックされたことを報告できなかった

ゴール 91 の executor の実出力:

> 報告コマンド自体も aqua のメタデータ更新権限で拒否され、subcommander へ未送信です

**13 分間黙って idle になった。**

**訂正（2026-08-27 の実測）。**この executor の見立ては誤りである。同じ sandbox で測ると、
aqua のシムは**警告を出すだけで実行を止めていない。**

```
$ herdr --version
WRN update the last used datetime ... error="... timestamp.txt: operation not permitted"
herdr 0.8.2                                      <- exit 0。実行できている
```

`go` も `gofmt` も同じ WRN を出しながら成功している。

**元の断定「指示は正しく、経路が無かった」の根拠は、この executor の自己申告 1 件だけだった。**
終了コードは記録されていない。**「拒否された」は executor の解釈である。**

**「シムが起動できるか」と「サーバの socket へ繋がるか」は別の問いである。**`--version` は
サーバへ繋がなくても答えが出る。実際の報告コマンドは Unix socket でサーバへ繋ぐ。分けて測った。

```
$ herdr agent get <name>                 exit 1
$ "$HERDR_BIN_PATH" agent get <name>     exit 1
Error: Os { code: 1, kind: PermissionDenied, message: "Operation not permitted" }
```

**socket 接続そのものが拒まれる。**したがって結論は次のように整理される。

```
「経路が無い」    結論としては正しい
原因             aqua の timestamp 書き込みではなく、socket connect の拒否である
$HERDR_BIN_PATH  報告経路を直していない。aqua の警告が消えるだけである
```

**8 単位へ配られた回避策は効いていなかった。**executor が実際に報告できていたのは
MCP 経由だからで、多重化ツール経由の報告は一度も通っていない。

問題は 2 つに分かれる。

```
経路が無い      -> 経路を作る（181 の管轄）
警告を誤読した  -> 警告と失敗を区別させる。依頼書か SKILL.md の問題
```

どちらも実在する。この訂正は 2026-08-27 に commander へ 1 行で送り、ゴール 181 へ渡した。
**どちらに寄せるかは 181 の管轄であり、このゴールでは決めない。**

## なぜ違うのか

実測（2026-08-27、`~/.codex/config.toml`）:

```
sandbox_mode = "workspace-write"
[sandbox_workspace_write] の節は無い    -> network_access は既定のまま
```

**この sandbox が包むのは shell コマンドだけである。**worker が起こす shell は
worktree（と一時ディレクトリ）の外へ書けず、ネットワークへも出られない。

このため、次の 2 つが同じ原因の別の症状になる。

```
worktree の外へ書く   aqua のシムは起動のたびに最終使用日時を worktree の外へ書く
                      $ ls -la $(which herdr)
                      lrwxr-xr-x ... /aquaproj-aqua/bin/herdr -> ../aqua-proxy

ポートを bind する    httptest は listener を開く。network_access が閉じていると
                      bind: operation not permitted になる
```

**ATCT 自身のラッパーも同じ場所へ書いている。**`bin/_resolve` は起動のたびに
`$HOME/.atct/bin` に `mkdir -p` し、古い版を `find -exec rm` し、使った版を `touch` する。
worktree の外である。**実測では通った**（`./bin/atct` は usage を出した）。書き込みが
`|| true` と `2>/dev/null` で best-effort になっているためで、拒否されても止まらない。

**ただし `mkdir -p "$CACHE_DIR"` だけは guarded ではない。**キャッシュディレクトリが
まだ無い環境の worker では、ここで `fail` して ATCT の CLI が起動しない。
いまの機械では既に存在しているので表面化していないだけである。

一方、MCP のツール呼び出しは shell を経由しない。`.mcp.json` は http 型で、
プロセスを起こさない。

```
{"mcpServers": {"atct": {"type": "http", "url": "http://127.0.0.1:8787/mcp"}}}
```

**これは実測で確かめられた。**同じ worker の shell から同じ URL へ curl すると繋がらないのに、
MCP のツール呼び出しは通る。

```
$ curl -sS -m 5 http://127.0.0.1:8787/mcp
curl: (7) Failed to connect to 127.0.0.1 port 8787: Couldn't connect to server

atct_session_identify / atct_handoff_receive / atct_role   すべて成功
```

`doc/specs/2026-08-24-mcp-over-http.md` で選んだこの形が、結果として
**sandbox の下でも通る経路**になっている。

## 決めたこと

### 決定 1: 依頼書は worker が実行できる検証コマンドを名指しする

`go test ./...` のような包括的なコマンドを依頼書に書かない。**worker が走らせてよい
パッケージを列挙する。**

理由は「worker の sandbox は delegator の sandbox と同じではない」ことに尽きる。
delegator が手元で通したことは、worker で通る証拠にならない。

worker 側の規約も対にする。**依頼書が名指ししていない検証を足さない。実行できなかった
検証を黙って飛ばさない。**実行できなかった検証は完了報告に「実行できなかった」と書く。
黙って飛ばされると、緑の報告が「検証していない」を意味してしまう
（`doc/specs/2026-08-22-green-tests-prove-less-than-they-look.md` と同じ穴）。

実測の一覧は `doc/investigations/2026-08-27-executor-sandbox.md` に残す。このリポジトリでは
worker が走らせられるのは次の 3 つだけである（残りは no test files）。

```
走らせてよい      cmd/atct-mcp   internal/domain   internal/store
走らせられない    cmd/atct       internal/daemon   internal/daemonctl
                  internal/e2e   internal/httpapi  internal/mcpshim
```

**`internal/daemon` が入っていることは、この spec を書いた subcommander 自身が外した。**
最初の依頼書で `go test ./internal/daemon` を名指しし、worker は socket の bind に
拒まれた。**delegator が「自分で通るから worker でも通る」と推測した結果である。**
これが決定 1 が要る理由そのものだった。

**同時に、規約が効いたことも測れている。**その worker は失敗を黙って飛ばさず、
「`internal/daemon` は bind に拒まれて完走できなかった。`internal/store` は成功」と
報告に書いた。緑ではないことが delegator に届いた。

### 決定 2: 通らない検証は delegator が肩代わりする。sandbox は緩めない

`decision 454`。代案は `~/.codex/config.toml` に
`[sandbox_workspace_write] network_access = true` を足すこと。**採らない。**

- `sandbox_mode` は**全プロジェクト共通の設定**である。開ければ atct と無関係な
  Codex セッションの shell も全部ネットワークへ出られる。**1 ゴールの検証都合で、
  機械全体の隔離境界を下げる取引になる。**
- worker は最も監督の薄い層である。依頼書だけを読んで動く相手の隔離を落とす代償が
  一番大きい。
- 肩代わりは既に 14 ゴールを通している。**動いていないのではなく、手順になっていなかった。**
  delegator はレビューのためにその worktree を既に読んでいるので、追加の往復も無い。

**肩代わりは委譲の例外ではなく委譲の一部である。**そう書く。

戻し方は 1 行なので、必要が測られたら変えられる。いま変えない理由は「必要が無い」では
なく「代償の範囲が機械全体だから」である。

### 決定 3: 未報告の handoff に「作業の跡があるか」を添える

`detection.handoff_unreported` は「受領はあったが 30 分報告が無い」で鳴る。
**原因が 2 つあり、いま外形で区別できない。**

```
(a) worker は働いたが、報告する経路が無くて黙った
(b) worker が着手していない
```

**worker が動けない状態では、worker 自身に信号を出させられない。**「報告できない」相手に
「報告できないと報告しろ」は解にならない。したがって信号は worker の行動ではなく、
**worker が働いた副作用**から取る。副作用はゴールの worktree の変更である。

判定は検知が鳴る直前にだけ行う。

```
worktree が存在しない / git が失敗した                        -> 未判定
git status --porcelain の挙げたファイルの mtime が
  受領時刻より新しい、または wt/goal-<goal8> に
  受領時刻以降のコミットがある                                -> changed
どちらも無い                                                  -> unchanged
```

worktree の場所（`.worktrees/<goal8>`）とブランチ名（`wt/goal-<goal8>`）は
`script/worktree-setup.sh` が作る **ATCT 自身の規約**なので、これを読むことは
外部ツールへの依存にならない（`doc/specs/2026-08-23-delegating-without-a-multiplexer.md`
の制約を破らない）。

文言を 3 分岐にする。**未判定は今日と同じ文言に落とす**ので、worktree を使っていない
利用者の見え方は変わらない。

**この信号は非対称である。**同じ worktree は subcommander も使うので、`changed` は
「誰かが働いた」までしか言わない。**強い証拠は `unchanged` のほう**で、これは
「誰も何もしていない」を意味する。今日いちばん高くついているのは、着手していない
worker を「働いているかもしれない」と扱って待つ時間なので、強いほうが効く側に出ている。

## 測り方の教訓: 資源に触らない引数で可否を判断しない

F2 の原因の見立ては 3 回変わった。**変えたのは全部その日のうちの測定である。**

```
1 回目  ゴール 91 の executor の自己申告「aqua の権限で拒否され未送信」
        -> commander が検算せずにゴール本文へ「経路が無い」と断定し、
           $HERDR_BIN_PATH を回避策として 8 単位へ配った

2 回目  herdr --version が exit 0
        -> 「経路が無いのではなく警告の誤読では」と訂正が配られた

3 回目  herdr agent get が両経路とも exit 1 / PermissionDenied
        -> --version は socket を使わない。agent get は使う。そこが境界だった
```

**結論は 1 回目に戻り、原因だけが入れ替わった。**

```
結論   executor は多重化ツール経由で報告できない   正しい
原因   timestamp 書き込みの拒否                   誤り
       socket connect の拒否                      正しい
```

**同じ実行ファイルが、資源に触らない引数では成功し、触る引数では権限で落ちる。**
`--version` や `--help` で可否を判断すると、通ったように見える。**worker がある道具を
使えるかは、worker が実際に走らせるコマンドで確かめる。**これは `skills/atct/SKILL.md` の
委譲の節に入れた。

**もう 1 つ。**1 回目の断定の根拠は executor の自己申告 1 件で、終了コードは残っていなかった。
**自己申告は解釈であって測定ではない。**

## 決めていないこと

**報告経路をどこに寄せるかはゴール 181 の管轄である。**このゴールでは決めない。

180 が扱うのは「報告コマンドが物理的に実行できない」という事実と、その事実の下で
検証と検知をどう組むかである。181 が扱うのは「worker に何を呼ばせるか」である。
2026-08-27 に commander へ 1 行で送り、181 へ転送された。

ただし、**実測は片方に寄る形を指している。**sandbox の下で確実に通る経路は
shell を経由しない MCP 呼び出しだけであり、多重化ツールのコマンドは定義上 shell である。
181 は自分で測り直したうえで結論を出す。

## 却下した案

**worker に「ブロックされた」と報告させる** — 報告できない相手に報告させる形なので
解にならない。`atct_handoff_yield` も同じ理由で (a) を救わない。

**セッションの生存で区別する** — 生きていて黙っている状態が (a) と (b) の両方に現れる。
分かれない。

**sandbox を緩める** — 決定 2 で捨てた。範囲が機械全体である。

**依頼書の定型を検査する新しい bash テストを作る** — 作らない。
`tests/wrapper_test.bash` が既にスキルの文言を検査しており、`script/release.sh` から
呼ばれている。**新しいファイルを足すと、release から呼ばれない検査が 1 本増える。**
既存ファイルに関数を足す。
