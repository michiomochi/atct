# 「PC が重い」への対処

実測は `doc/investigations/2026-08-28-machine-load.md`。この文書は**何を採り、何を採らないか**と、
次に「重い」と言われたときに**何を測るか**を決める。

## 決めたこと

| 候補 | 判断 | 理由 |
| --- | --- | --- |
| ① Spotlight から `~/go/pkg/mod` を外す | **採る**（人間が踏む） | 220,977 件は実在する無駄。人間が明示的に求めた。ただし体感への寄与は小さい |
| ② MCP サーバを space ごとに絞る | **採らない** | 3 つとも実使用がある。CPU 0.0%、RSS 6.07 GB は 128 GB / swap 0 のマシンで計測にかからない |
| ③ テストの同時実行に上限を置く | **採らない** | 実測で上限は go を 2.1 倍・web を 4.1 倍遅くし、load も Defender の CPU も下げなかった |
| ④ Defender は触らない | **そのまま** | 人間が「しょうがない」と明言。ただし主因なので記録する |
| ⑤ load average を指標に使うのをやめる | **採る**（新規） | load 90 の瞬間に CPU 75% idle。この計器で読んだ因果推定は成立しない |

### ① の手順

**`.metadata_never_index` は効かない。**実測で新規ファイルの索引も止まらなかった。
commander が 5 箇所に置いたマーカーは対策になっていないので、**置いてあることを理由に
「対策済み」と読まないこと。**

効くのは **`.noindex` 接尾辞**。Go はモジュールキャッシュの位置を `GOMODCACHE` で変えられるので、
sudo も GUI も要らない。

**全 space を止めてから**踏む（`go build` 中に走らせると壊れる）。

    mv ~/go/pkg/mod ~/go/pkg/mod.noindex
    go env -w GOMODCACHE="$HOME/go/pkg/mod.noindex"
    go env GOMODCACHE

`mv` は同一ボリュームなので即時。再ダウンロードは発生しない。戻すとき:

    go env -u GOMODCACHE && mv ~/go/pkg/mod.noindex ~/go/pkg/mod

踏んだ 5〜10 分後に効果を測る:

    mdfind -onlyin ~/go/pkg/mod.noindex 'kMDItemContentType == "*"' | wc -l   # 0 を期待
    mdfind -onlyin ~/go 'kMDItemContentType == "*"' | wc -l                   # 220977 から減るはず

GUI でやりたい場合はシステム設定 > Spotlight > 検索プライバシー に `~/go/pkg/mod` を追加しても
同じ効果になる。ただし macOS のアップデートで消えることがある。

### ③ を採らない理由（上限を外すと落ちる検査は作らない）

上限を置かないので、上限を守らせる検査も置かない。**代わりに、上限を置きたくなったときに
引き直すべき測定を残す**（下の「重いと言われたら」）。

`go test -p` も vitest の `maxWorkers` も 1 回の起動しか縛れず、worktree を跨いだ上限は
誰も持てない。仮に持てたとしても、実測では上限が wall を伸ばすだけで load も Defender も
下げなかったので、作る価値がない。

## 重いと言われたら（測る順番）

**`uptime` から始めない。**このマシンの load average は飽和を表していない。

1. **本当に CPU が足りないのかを確かめる**

       top -l 2 -n 0 | grep -E 'Processes|CPU usage|PhysMem'

   `idle` が 20% を切っていなければ CPU 飽和ではない。`running` の本数も見る。

2. **メモリとスワップ**

       sysctl vm.swapusage
       vm_stat | grep -Ei 'compress|swapout'

   `swapouts` が増えていなければメモリ不足ではない。

3. **誰が食っているか**

       ps -Ao %cpu=,rss=,args= -r | head -10

   ATCT のプロセス（`claude` / `node` / `go`）が上位に来ていなければ、
   **ATCT 側の対策では直らない。**実測では常に Defender / Notion / Chrome が上位だった。

4. **ディスク**

       iostat -c 3

   `tps` と `MB/s` を見る。Defender は同期スキャンなのでここに出る。

5. **それでも原因が割れないときだけ検査を止めて差分を取る。**
   止める前と後の 1・2・3 を並べる。**空いている時間帯どうしを比べても意味がない。**

## やらないこと

- **「space を減らせ」を対策にしない。**ATCT の設計は space を増やして並列に進めることを
  前提にしている。実測でも space の数は load の原因ではなかった
  （space を 4 つ閉じても load は下がらず、検査が終わった瞬間に 244 → 22 になった）。
- **`.metadata_never_index` を新たに置かない。**効かない。
- **`mdutil -i off <path>` を試さない。**ボリューム単位でしか効かず `invalid operation` になる。
