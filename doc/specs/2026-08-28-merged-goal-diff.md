# マージ済みのゴールでも差分を出す

ゴール 193。ゴール 187 で入れた `GET /api/goals/{id}/diff` は `git diff <base>...wt/goal-<N>` を出す。
**これはマージした瞬間にゼロになる。**それでも `available: true` を返すので、画面上は
**「差分が無いゴール」と区別できない。**実測（2026-08-28）:

    $ curl -s http://127.0.0.1:8787/api/goals/181/diff
    available: true / base: main -> branch: wt/goal-181 / 0 files changed, +0 -0

ゴール 181 は `2ed2c45` でマージ済みである。承認判断のときに成果が読めない。

## 決めたことと理由

### 1. マージ済みかは祖先関係で判定する。差分の件数では判定しない

    git merge-base --is-ancestor refs/heads/wt/goal-<N> <baseRevision>

ブランチ先端が base の祖先なら「マージ済み」である。**差分 0 件を条件にしない。**
0 件はマージ前（1 つもコミットしていないブランチ）でも起きるし、マージ後にブランチへ
コミットを足せば 0 件にならない。**事実は祖先関係のほうである。**

この判定は**マージコミットの文言に一切依存しない**（完了条件 6）。

### 2. 導入マージコミットは「第 2 親がブランチ先端に一致するマージ」である

    git rev-list --ancestry-path --merges --topo-order --reverse <branch>..<baseRevision>

を候補として、**`M^2` がブランチ先端と一致する最初の 1 つ**を採る。**一致するものが無ければ
「マージ済み」と扱わない**（決定 4）。

**「ancestry-path の先頭を採る」だけでは足りない。**実測（2026-08-28・このリポジトリの
`wt/goal-*` 全 26 本）で、先頭を採る規則は**まだ 1 つもコミットしていないブランチを
「マージ済み」と誤判定した。**

    wt/goal-183   先頭を採る規則 -> 20b2777「Merge goal 184」の 6 ファイル   <- 誤り
    wt/goal-188   先頭を採る規則 -> 20b2777「Merge goal 184」の 6 ファイル   <- 誤り
    wt/goal-191/192/193/194  先頭を採る規則 -> マージ済み扱い（該当マージ無し）  <- 誤り

作りたてのブランチは先端が **main の古いコミットそのもの**なので、`merge-base --is-ancestor`
は真になり、その後 main に積まれた**無関係なマージ**が候補の先頭に来る。
**6 本のゴールの画面に他ゴールの差分が出る。**

`M^2 == ブランチ先端` にすると、同じ 26 本で**誤判定が 0 になる。**

    for b in $(git for-each-ref --format='%(refname:short)' refs/heads | grep '^wt/goal-'); do
      tip=$(git rev-parse "refs/heads/$b")
      for m in $(git rev-list --ancestry-path --merges --topo-order --reverse "refs/heads/$b..main"); do
        [ "$(git rev-parse "$m^2" 2>/dev/null)" = "$tip" ] && echo "$b -> $m" && break
      done
    done

    wt/goal-181 -> 2ed2c45  Merge goal 181: ...          （4 ファイル）
    wt/goal-187 -> 6b3f32e  Merge goal 187: ...          （14 ファイル）
    ... マージ済み 17 本すべてが自分のマージに解決した
    wt/goal-183 / 188 / 191 / 192 / 193 / 194 -> 該当無し（＝マージ済みではない）

**候補が複数返るのは通常である**（`wt/goal-181` では 5 件）。`M^2 == 先端` はそのうち
**そのブランチ先端をそのまま base へ入れたマージ**を 1 つだけ選ぶ（完了条件 5）。

**限界を 1 つ書いておく。**同じブランチを 2 回マージしたゴール（`wt/goal-91` は
「Merge goal 91」と「Merge goal 91 follow-up」の 2 回）では、**後のマージだけが出る**（1 ファイル）。
先のマージの成果は出ない。**承認は 1 回のマージの直後に行うので、通常の流れでは起きない。**
2 回分を足し合わせるには差分の合成が要り、`?path=` の生 diff も合成しなければならない。
**得られるものに対して高い。採らない。**

**候補 ①（`--grep "^Merge goal <N>:"`）はブランチがある限り採らない。**commander が文言を
変えたら落ちる。**候補 ③（`goals.merge_commit` を持つ）も採らない。**②で git から引ける。

### 3. 差分は `git diff <M>^1 <M>`

`M^1` は base 側の親、`M` はマージ後の base である。差はそのマージが **base に持ち込んだもの**で、
**コンフリクト解決の内容も含む。**「実際に main へ入った成果」がこれである。

`M^1...M^2` にはしない。**解決の内容が落ちる。**

### 4. ブランチが無いときだけ、文言で引く。ここが `merged_unresolved` の居場所

**`M^2 == 先端` が一致しないときは「マージ済み」と扱わない。**作りたてのブランチと
fast-forward で入ったブランチは**先端の形が同じで区別できない**（どちらも main の履歴上の
コミットを指す）。区別できないものを「マージ済み」と名乗ってはならない。
このときは従来どおり `<base>...<branch>` を出す（多くは 0 ファイル）。

**ブランチ自体が無い場合は ② が使えない。**承認後の片付けでブランチは消えるので、
ここを落とすと**片付け済みの古いゴールは永久に差分が見られない。**ここだけ文言で引く。

    git log -E --grep='^Merge goal <N>([: ]|$)' --format='%H %P' <baseRevision>

の**最初の行（＝最も新しい一致）**を採る。②が「先端をそのまま入れた最後のマージ」を採るのと
揃える。

    親が 2 つ以上   そのマージの差分を出す                     available: true / source: "merge_commit"
    親が 1 つ       squash か fast-forward。差分を出す先が無い  available: false / reason: "merged_unresolved"
    一致無し        そもそもブランチが無い                     available: false / reason: "no_branch"

**これで完了条件 4 の 2 つの理由が区別できる。**`merged_unresolved` は
「マージされた証跡はあるが、差分を取れるマージコミットが無い」という**実際に起きうる状態**であり、
`no_branch` は「証跡が何も無い」である。

**この経路だけが文言に依存する。**依存していることを検査で固定する（完了条件 6）:
`^Merge goal <N>` に一致しないマージ（例 `"landed goal <N>"`）でブランチを消した場合、
`no_branch` になることを検査する。**文言を変えたらこの検査が落ちる。**

### 5. 応答に `source` と `merge_commit` を足す

    "source": "branch"        `<base>...wt/goal-<N>` を出した（マージ前）
    "source": "merge_commit"  マージコミットの差分を出した
    "merge_commit": "<40 桁の SHA>"   source が branch のときは ""

**これで「差分が無いゴール」と「マージ済みのゴール」が画面で区別できる**（完了条件 2）。
画面は `source: "merge_commit"` のとき短縮 SHA を添えて「マージ済み」と出し、
`merged_unresolved` のときは節を消さずに一行で理由を出す。

### 6. マージ前の経路は 1 バイトも変えない

`merge-base --is-ancestor` が偽なら、既存の `<baseRevision>...<branch>` をそのまま通す。
既定ブランチの解決（`refs/remotes/origin/HEAD` -> main / master）と 5 秒の期限も
ゴール 187 のまま残す（完了条件 3）。**否定側はここである。**

## 検査

`internal/httpapi/goal_diff_test.go` に足す。

1. マージ済み（`--no-ff`）で、そのゴールのファイルが出る／マージ後に base へ入った
   別のファイルは出ない -> 完了条件 1
2. マージのメッセージを `"Merge goal <N>"` 以外にしても 1 が通る（②は文言を見ない）-> 完了条件 6
3. **コミットが 1 つも無いブランチ**（作った直後）で、`source: "branch"` / `files_changed: 0` に
   なること。**マージ済み扱いにしないこと** -> 誤判定の否定側
4. マージの後に base へ別ブランチのマージを積んでも、採るのは**自分のマージ** -> 完了条件 5
5. ブランチを消し、`"Merge goal <N>: ..."` のマージが base にある -> `source: "merge_commit"`。
   同じ状況で `--squash` の単一親コミットなら `merged_unresolved`。
   `"landed goal <N>"` のような別文言なら `no_branch` -> 完了条件 4・6
6. マージ前のブランチは `source: "branch"` で従来どおりの差分 -> 完了条件 3
7. マージ済みのゴールで `?path=` がマージコミットの hunk を返す
