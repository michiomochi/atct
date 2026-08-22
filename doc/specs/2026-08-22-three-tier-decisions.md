# 3 層化: 決まったことだけ

2026-08-22。**理由は書かない。**理由と実測は
`doc/specs/2026-08-22-three-tier-orchestration.md`（566 行）にある。

## 層

| 層 | やること | やらないこと |
|---|---|---|
| commander | 通知をさばく / 割り当てる / space を作る / 着地した差分のレビュー / リリース / マージ衝突の解決 / 後片付け | そのゴールの設計 / 実装 / executor の成果物の手直し |
| subcommander | 設計・依頼書・実装レビュー・完了報告 / worktree を作る / 人間へ決定を出す | 他の単位を見る / リリース / space を作る |
| executor | 実装とテスト | 設計判断 / 再委譲 / commit / `.git` を書く |

**中間層は条件付き。**常に置くのではなく、commander が通知やレビューを落とし始めたとき。

**commander が書くものはある。**禁じているのは「そのゴールの設計と実装」であって、
**書くこと自体ではない。**commander が書くのは次の 3 つ。

| commander が書くもの | 例 |
|---|---|
| 自分の成果物 | spec、依頼書、引き継ぎ、ナレッジ文書 |
| 環境の設定 | `~/.codex/config.toml`、MCP のラッパ、フックの配線 |
| リリースに必要な機械的な整合 | 版の書き換え（`script/release.sh` が行う） |

**executor の成果物を手直しするのは境界を越えている。**2026-08-22 に commander が
`GoalDetail.tsx` のインデントを直した。1 行の整形だが、**executor に戻すべきだった。**

## 名前

```
atct-commander                 プロジェクトの space
atct-c22a6d79-subcommander     作業単位ごとの space（ゴール ID の先頭 8 桁）
atct-c22a6d79-executor
```

- 規約は `<space>-<役割>` のまま。**変わるのは space の定義だけ**（プロジェクト単位 → 作業単位）
- **エージェント名は 32 文字まで。プロジェクト名は 10 文字まで**
- **1 つの作業単位に subcommander は 1 台**
- **workspace の label には長さ・文字種の制約は無い**（32 文字はエージェント名だけ）

## 分担

| 論点 | 決定 |
|---|---|
| ファイルの衝突 | 作業単位ごとに worktree。**atct 側の変更は不要** |
| ゴール横断の知識 | **atct に `knowledge` を作らない。**事実はテスト、判断は `doc/specs/` |
| 読むべき spec の伝達 | ゴールの記述に書く（`atct context` が運ぶ） |
| 決定の宛先 | subcommander が直接人間に聞く |
| マージ衝突の解決 | commander |
| リリース | commander が本体ツリーで |
| 担当の記録 | **atct には持たない。**`herdr agent list` の名前と claim で足りる |
| 古い worktree の片付け | commander。未コミットの変更は人間に出す |
| 最終成果物のレビュー | commander。**リリースを関門にする**（観点 4 つ） |
| `pnpm install` | subcommander が worktree を作った直後に 1 回 |
| 通知の受け口 | **wakeup に統一。Stop hook を廃止。間隔 3 分**（下記） |
| atct 固有の名前の置き場 | **atct 側で持つ。**`orchestration` には書かない（下記） |

### 何をどの文書に書くか

`orchestration` スキルは全 space が読む（atct / stock-data / HQ / stock-ai / dotfiles）。

**ただし前提が 1 つ違っていた（人間の指摘、2026-08-22）。**この環境の dotfiles は
`orchestration` スキルを含めて **atct 前提**である。したがって「atct を使わない space が
読む」という commander の懸念は成り立たない。**atct 固有の名前を書いてよい。**

**それでも分ける価値はある。**`orchestration` は役割と委譲の作法を扱うスキルであって、
atct の実装の詳細（環境変数名、スキル名）はその主題ではない。**スキルにその主題に属さない
規則を書かない**という既存の方針（`ai-config` の書き方方針）に従う。

| 文書 | 書くこと |
|---|---|
| `orchestration` スキル | **汎用形だけ。**「pane に作業単位を示す環境変数を渡すなら、名前を付けるコマンドと同じ場所に書く」 |
| atct の spec と手順 | **`atct:start` を呼ぶかどうかなど、atct 固有の手順** |

**判定の軸は「全 space が読むか、1 つのプロジェクトだけが読むか」。**
置き場のもう 1 つの軸（「公開物か、この環境の設定か」。2026-08-20 に確定）とは別物で、
**両方を通す。**

- `herdr` を見るフックは atct に置かない ← 公開物かどうかの軸
- `subcommander` という語は `orchestration` に書くが「atct のゴール 1 件 = space 1 つ」は
  書かない ← 全 space が読むかどうかの軸
- 「subcommander は `atct:start` を呼ばない」は atct 側 ← 同じ軸

## 通知の受け口

**人間の判断（2026-08-22）: wakeup に統一し、Stop hook を廃止する。間隔は 3 分。**

```
Monitor を張るのは commander だけ（atct:start を呼ぶ）
subcommander は atct:start を呼ばない → 通知が届かない
Stop hook は廃止する → 両方の層で発火する問題が消える
```

**`ATCT_SCOPE_GOAL` は不要になった。**環境変数も Stop hook の変更も要らない。
Monitor はプラグインが張るのではなく**セッションが自分で張る**（`plugin/hooks/*.json` に
`watch` は 0 件）ので、張らなければ届かない。

**先に足すものが 3 件ある。**Stop hook が言っていて検知イベントに無いもの。

1. **答えられた決定を poll していない**（人間の回答がエージェントに届かないまま止まる）
2. 既定で閉じた決定を poll していない
3. 死んだセッションの claim が残っている

**順序を守る。**足す → 実測する → Stop hook を消す。逆にすると、その間だけ
「人間の回答が届かない」穴が空く。**1 番は回答が失われる形なので最も落とせない。**

### 間隔 3 分の代償

実行ログ 708 件の間隔は p50 が 0.6 分、p75 が 3.7 分、p90 が 13.8 分。
**3 分は p75 の少し下なので、正常な作業の合間にも鳴る。**加えて wakeup はプロジェクト
単位なので、条件を満たすプロジェクト数の倍だけ増える（今日 1 周期で 4 プロジェクトが
同時に鳴った）。10 分 → 3 分で通知は約 3.3 倍。

**Stop hook を捨てる代償として受け入れる判断である。**

## 横断レビューの観点（リリースの関門）

`script/release.sh` は `--reviewed` が無いと非ゼロで終わる。

1. 数を根拠にした変更が、別の単位で増減していないか
2. 公開されている名前の意味が変わり、別の呼び出し元が嘘になっていないか
3. 既存の計測・検証の手順を壊していないか
4. 横断規則に新しい違反を持ち込んでいないか

**1 と 4 は検査に落とせる。2 と 3 は人手で見る。**

## 強制されていないもの

`single-subcommander.sh` は次を素通しする。**意図的。**

```
/usr/bin/herdr agent start NAME              絶対パス呼び出し
herdr agent start --kind codex NAME          名前より前にフラグ
```

**このフックは「1 単位 1 subcommander」を強制しない。**規約どおりに書いたときの
間違いを止めるだけ。

## 残っているもの

| 残り | 担当 | 状態 |
|---|---|---|
| 足りない 3 つの検知を足す | atct | **承認済み。着手できる** |
| `wakeup` の間隔を 3 分にする | atct | **承認済み。着手できる** |
| Stop hook を消す | atct | 上の 2 つを実測した後 |
| `single-subcommander.sh` の apply | dotfiles | 人間の承認待ち |
| `chezmoi apply`（役割定義 `b66acc5`） | dotfiles | 人間の承認待ち |

**`ATCT_SCOPE_GOAL` の実装は不要になった。**Stop hook を廃止するので、絞り込む対象が
無くなる。
