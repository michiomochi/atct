# 「PC が重い」の実測

測定日: 2026-08-28 JST（12:21–13:34）
マシン: Apple Silicon / macOS 26.6.2 / 16 論理コア / RAM 128 GB
worktree: `.worktrees/199`（`wt/goal-199`）
同時に動いていたもの: Claude セッション 14〜15 本、Chrome、Notion、VS Code、Microsoft Defender、
Jamf。**測定中に他の worktree が `go build ./...` と `go test ./... -count=1` を走らせていた時間帯がある**（下の各節に ambient load を併記した）。

## 結論を先に

1. **`uptime` の load average はこのマシンでは飽和の指標にならない。** load 90 の瞬間に CPU は 75% idle、
   実行可能プロセスは 1136 中 7 本だった。ゴールを立てた「load 244」も同じ計器で読んだ値である。
2. **`pnpm test` は 613 秒かからない。実測 7.05 秒。** commander の 613 秒は「重い」状態で測った値で、
   87 倍遅くなっていた。テストは重さの原因ではなく、重さの被害者である。
3. **テストの同時実行を絞ると遅くなるだけで、load も Defender の CPU も下がらない。**
4. **CPU を継続的に食っているのは Microsoft Defender（112〜557%、瞬間最大 662%）。** 次いで
   Notion Helper（134〜191%）と Chrome（102%）。ATCT 側のプロセスは上位に入らない。
5. **`.metadata_never_index` はこのマシンで効いていない。** 新規ファイルの索引も止まらなかった。
   効くのは `.noindex` 接尾辞。

## 1. load average は飽和を表していない

    $ uptime
    13:27  up 3 days, 1:16, load averages: 123.27 79.58 47.91

    $ ps -Ao stat= | cut -c1 | sort | uniq -c
    1133 S      <- sleeping
       3 R      <- running
       1 U      <- uninterruptible

    $ top -l 2 -n 0
    Processes: 1136 total, 7 running, 1 stuck, 1128 sleeping, 8454 threads
    Load Avg: 90.17, 74.85, 46.95
    CPU usage: 16.77% user, 7.86% sys, 75.36% idle
    PhysMem: 93G used (4641M wired, 5904K compressor), 35G unused.

    $ sysctl vm.swapusage
    vm.swapusage: total = 0.00M  used = 0.00M  free = 0.00M

**load 90〜123 で CPU は 75% idle、swap は 0、実行可能は 7 本。**
load average と CPU 需要が対応していない。Defender の endpoint security 拡張
（`epsext` / `netext`）がファイル操作を同期的に止めるため、待たされたスレッドが
load に計上され続けているのが最も素直な説明である。

**したがって「load 244」を根拠にした因果推定はすべて引き直す必要がある。**

### プロセス数は load と連動していない（自然実験）

同じ日の 3 時間後、**Claude セッションも MCP サーバも 1 本も閉じていない**状態で測り直した。

    12:58 頃   load 244   （commander 測定、検査が走っていた）
    13:27      load 123   procs 1136  threads 8454  CPU 75.4% idle
    16:08      load   4.59 procs 1137  threads 8764  CPU 77.8% idle

**プロセス数は 1136 → 1137、スレッド数はむしろ 8454 → 8764 に増えているのに、
load は 123 から 4.59 に落ちた。**space の数・MCP サーバの数・プロセス数のいずれも
load と連動していない。commander の「space を 4 つ閉じても下がらず、検査が終わったら
244 → 22 になった」と同じ結論を、逆向き（何も閉じずに下がる）でも確認できた。

## 2. テストの実測（対策前後の比較）

同じ worktree・同じ検査内容（`go test ./... -count=1` と `pnpm test` を**同時に**起動）で 3 回測った。
`go build ./... && go vet ./...` で事前にビルドキャッシュを温めてある。

| 回 | 設定 | 開始時の ambient load1 | `go test` wall | `pnpm test` wall | vitest Duration |
| --- | --- | --- | --- | --- | --- |
| A | 既定 | 209.86 | 18.54s | 20.53s | 18.31s（environment 122.81s） |
| B | **上限あり** `go test -p 2 -parallel 2` / `vitest --maxWorkers=2` | 6.11 | **38.91s** | **29.13s** | 27.43s（environment 21.54s） |
| A2 | 既定 | 10.65 | 18.17s | **7.05s** | 6.56s（environment 18.12s） |

**上限を置いた B は、A2 より空いている状態で走ったのに go が 2.1 倍・web が 4.1 倍遅い。**
上限は wall を伸ばすだけで、load も Defender の CPU も下げなかった（次節）。

commander の測定値との差:

| 項目 | commander（重い状態） | 今回（通常状態） | 比 |
| --- | --- | --- | --- |
| `pnpm test` wall | 613s | 7.05s | 87x |
| vitest environment | 3181s | 18.12s | 176x |

**テストが遅かったのではなく、マシンが 87 倍遅かった。**

## 3. サンプリング（5 秒間隔、%CPU 合計）

run B（上限あり、13:32:17–13:33:03）:

    ts        load1  load5  defender  gotool  node   spotlight  procs
    13:32:22  6.29   31.19  47.0      0.0     3.2    0.0        1138
    13:32:27  6.11   30.74  389.7     12.1    42.5   0.0        1144
    13:32:37  6.01   29.91  557.4     78.0    15.8   3.3        1152
    13:32:47  8.24   29.60  222.4     13.7    160.0  1.6        1151
    13:33:03  8.30   28.57  422.9     4.1     0.4    0.7        1149

run A2（既定、13:33:18–13:33:44）:

    13:33:23  11.49  27.96  33.1      0.0     0.5    0.3        1154
    13:33:28  10.65  27.51  300.2     51.6    759.8  1.2        1197
    13:33:38  11.76  27.20  319.7     132.5   296.9  4.0        1195
    13:33:44  11.38  26.86  83.6      75.2    0.6    3.2        1155

**どちらも Defender が 300〜557% に張り付く。上限を置いても下がらない。**
読むファイルの数が同じだからで、上限は同じ量の I/O を時間方向に伸ばすだけである。
**load1 は検査の実行中もほとんど動かない（10.65 → 11.76）。テストは load を作っていない。**

無負荷時の ambient（12:32–12:34、検査なし）でも Defender は 8〜565% を往復していた。

## 4. CPU を食っているものの内訳

    $ ps -Ao %cpu=,args= -r | head -6
    206.0 wdavdaemon_unprivileged        (Microsoft Defender)
    134.2 Notion Helper (Renderer)
     23.7 WindowServer
     11.7 wdavdaemon privileged
     10.9 Notion
     10.1 Google Chrome Helper

    クラス別合計: defender=112.9  chrome=101.9  notion=41.5  claude=23.3  node=3.6

**ATCT 側（claude 23.3% + node 3.6%）は Defender の 1/4 以下。**
人間は Defender を「しょうがない」と明言しているので触らないが、
**「重い」の主因が Defender であることは記録に残す。**

## 5. Spotlight

### `.metadata_never_index` は効いていない

同一マシン・同一 60 秒窓で、同じ内容のファイル 30 個ずつを 2 つのディレクトリに書いた。

    plain/    （マーカーなし）
    blocked/  （.metadata_never_index を置いた）

    plain items:      30      plain fulltext:   1
    blocked items:    30      blocked fulltext: 1

**マーカーの有無で差が出ない。既存索引を消さないどころか、新規索引も止めていない。**
commander が `.worktrees` / `~/Library/Caches/go-build` / `~/go/pkg/mod` / `~/Library/pnpm` /
`web/node_modules` に置いたマーカーは、**置いただけでは対策になっていない。**

### `.noindex` 接尾辞は効く

同じディレクトリ配下に `sub.noindex/` を作り、同じく 30 ファイルを書いた。

    noindex items:    0       noindex fulltext: 0
    plain items:      30      （対照、同時刻）

**ディレクトリ名が `.noindex` で終わると索引されない。sudo も GUI も要らない。**

### 何が索引されているか

`mdfind -onlyin <dir> 'kMDItemFSName == "*"'` は索引済みの場所でも 0 を返す。
`kMDItemContentType == "*"` を使う。

    ~/go/pkg/mod                220,977    <- 13 GB / 497,261 ファイル
    ~/Library/pnpm                  290
    ~/.npm                            0
    ~/.cache                          0
    ~/Library/Caches                  0
    web/node_modules                  1    <- 52,673 ファイルあるが実質索引外
    ~/.local/share/mise               0
    .worktrees                        0
    ~/Library/Caches/go-build         0
    <main checkout>                 327    （うち doc/ が 101）

**索引を消す価値があるのは `~/go/pkg/mod` の 1 箇所だけである。**
`mdutil -i off <path>` はボリューム単位でしか効かず、パス指定は `invalid operation` になる。

## 6. MCP サーバ（space 数に正比例するもの）

15 セッション時点:

    playwright-mcp   n=31  RSS=2.85 GB  CPU=0.0%
    context7-mcp     n=31  RSS=2.53 GB  CPU=0.0%
    codex mcp-server n=15  RSS=0.69 GB  CPU=0.0%
    合計             n=77  RSS=6.07 GB  CPU=0.0%

`npx -y <pkg>` は `npm exec` ラッパと実体の 2 プロセスを常駐させるので、
playwright と context7 はセッションあたり 2 本ずつになる。

**実使用（`~/.claude/projects/` の全 145 transcript・351 MB、`"name":"mcp__<server>__` の tool_use を計数）:**

| サーバ | 呼び出し回数 | 呼んだセッション数 |
| --- | --- | --- |
| playwright | 97 | 5 |
| context7 | 9 | 3 |
| codex | 7 | 1 |
| atct | 1,924 | 多数 |

（atct リポジトリの 88 transcript に限ると playwright 97 / context7 0 / codex 0。
playwright を呼んだ 5 セッションはすべて atct のものだった。）

**3 つとも使われている。**そして RSS 6.07 GB は 128 GB・35 GB 未使用・swap 0 のマシンでは
計測にかからない。CPU は 0.0%。npx の起動コストも cold 6.77s / warm 0.79s で、
セッション起動 1 回きりである。

## 7. 測定の注意

- **重い測定を自分で走らせると、自分が測ろうとしている load を自分で作る。**
  上表の ambient load はすべて併記した。run A は他 worktree が検査を走らせていた最中である。
- `ps` や `find` でホームを舐めるだけで Defender が 500% を超える。
  無負荷 ambient の 565% はこの測定自身が誘発した可能性が高い。
- zsh では `"$var:path/to/file"` が `$var:t` のヒストリ修飾子と解釈され `bad substitution` になる。
  `"${var}:path/to/file"` と書く。
- awk の正規表現リテラル `/.../` の中に `/` を書くとそこで正規表現が閉じる。
  `$0 ~ "pat"` の文字列形式を使う。
