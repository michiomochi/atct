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
| `ATCT_SCOPE_GOAL` の名前 | **atct 側で持つ。**`orchestration` には書かない |

## 通知の受け口

```
ATCT_SCOPE_GOAL=<フルのゴール ID>

  値がある  → pending をその単位に絞る（抑止しない）
  値が無い  → 全部報告する（いまと同じ）
  値が不正  → 絞らずに全部出す
  いずれも systemMessage で 1 行出す
```

- **抑止ではなく絞り込み**
- **役割名は渡さない**（名前が既に持っている）
- 環境変数は**名前を付けるコマンドと同じ場所に書く**

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

## 残っているもの（すべて人間の承認待ち）

| 残り | 担当 |
|---|---|
| `ATCT_SCOPE_GOAL` の実装 | atct |
| `single-subcommander.sh` の apply | dotfiles |
| `chezmoi apply`（役割定義 `b66acc5`） | dotfiles |
