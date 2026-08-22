# 3 層に分ける（orchestrator / commander / executor）

2026-08-22。人間の提案:

> 1 つの claude が atct からの通知をさばき、設計もし、commander への依頼も担当すると
> なるとボトルネックとなってしまいスピードがでない。なので atct のフロントに立つ
> claude（orchestrator）を作成し、orchestrator はゴールの対応毎に別で herdr で space を
> 作成し claude（commander）をたてる、commander は codex の executor をたてる。

commander の判定: **形は正しい。**ただし**先に決めないと壊れる点が 2 つある**（末尾）。

この文書は依頼先で 2 つに分かれる。**A は dotfiles（この環境の設定）、B は atct
（公開物）。**分ける基準は 2026-08-20 に確定した「公開物か、この環境の設定か」。

## なぜ（2026-08-22 の実測）

| 測ったもの | 値 |
|---|---|
| この space で出した依頼書 | 36 通 |
| active なゴール | 26 件 |
| それを通したコンテキスト | 1 つ |
| executor 1 件あたりの所要 | 5 分前後 |
| commander が「投げます」と書いて投げずに終えた回数 | 3 回 |
| このセッションがコンテキスト上限に達した回数 | 1 回以上（要約から再開） |

**executor は詰まっていない。待ち行列は commander の側にあった。**

## 全体像

```
                    ┌──────────────────────────────┐
                    │  atct daemon                  │
                    │   SSE / Stop hook / pending   │
                    └───────────┬──────────────────┘
                                │ 通知はここ 1 か所に集まる
                                ▼
                    ┌──────────────────────────────┐
                    │  orchestrator (claude)        │
                    │  ・通知をさばく                │
                    │  ・ゴールを commander へ割る   │
                    │  ・space を作る                │
                    │  ・設計も実装もしない          │
                    └───┬──────────┬──────────┬────┘
                        │          │          │  ゴール 1 件 = space 1 つ
              ┌─────────▼──┐ ┌─────▼──────┐ ┌─▼──────────┐
              │ commander  │ │ commander  │ │ commander  │
              │ (goal A)   │ │ (goal B)   │ │ (goal C)   │
              │ 設計・レビュー│ │            │ │            │
              └──┬───┬─────┘ └──┬─────────┘ └──┬─────────┘
                 │   │           │              │
            ┌────▼┐ ┌▼────┐  ┌───▼──┐       ┌───▼──┐
            │ exec│ │ exec│  │ exec │       │ exec │   codex
            └─────┘ └─────┘  └──────┘       └──────┘
```

**ゴールが space の単位になるのが要点。**「話題が変わったら pane を作り直す」という既存の
規則の、自然な単位がゴールである。

## 層の境界

| 層 | やること | やらないこと |
|---|---|---|
| orchestrator | 通知をさばく / ゴールを割る / space を作る / 人間への窓口 | 設計・実装・レビュー・ファイルの編集 |
| commander | そのゴールの設計・依頼書・実装レビュー・完了報告 / worktree を作る | 他のゴールを見る / リリース |
| executor | 実装とテスト | 設計判断 / 再委譲 / commit |

**orchestrator は設計しない。**設計を始めると今日と同じ場所で詰まる。

## 提案が既に解いている問題

**画面の高さの制約が消える。**pane を増やす形だと実測で上限 3 台だった（高さ 91 行の
タブを 2 分割で 1 台 45〜46 行、3 台で約 30 行、4 台で約 22 行）。**ゴールごとに space を
作れば各 space が画面を丸ごと使えるので、この上限に当たらない。**

---

# A. dotfiles への依頼分（この環境の設定）

## A-1. 役割定義を 3 つに増やす

`orchestration` スキルの「役割の割り当て」ブロックはいま 2 行（commander と executor）。
これを 3 層にする。**このブロックだけを書き換える形は維持する**（以下の記述に
ハーネス名・モデル名を書かない、という既存の方針）。

名前の規約も 3 層に広げる。いまは `<space>-commander` / `<space>-executor`。
**orchestrator は space をまたぐので、`<space>-` を前置できない。**別の規約が必要。
実測（2026-08-15）: 汎用名 `commander` を名乗っていた space に別 space の完了報告が
届いた。**汎用名 `orchestrator` は同じ事故を起こす。**

## A-2. orchestrator が space を作る手順

`herdr workspace create` の戻り値（`.result.workspace` / `.result.tab` /
`.result.root_pane`）から ID を読み、そこに commander を立てる。**ゴール ID を space 名に
入れるかを決める必要がある**（名前は `[a-z][a-z0-9_-]{0,31}` で 32 文字まで。UUID は
入らないので先頭 8 桁になる）。

## A-3. 通知の受け口を orchestrator に寄せる

いまは commander の pane で `atct watch` を Monitor に流し、Stop hook も commander で
効いている。**両方を orchestrator へ移す。**commander 側に残すと、今日と同じで
commander が通知に反応して設計を始める。

## A-4. commander に「他のゴールを見ない」を書く

いま commander は全ゴールを見る前提で書かれている。**1 ゴールに閉じる指示が必要。**
ただし後述の B-2（ゴール横断の知識）と衝突するので、**どこまで閉じるかは B-2 と
セットで決める。**

## A-5. 書かないもの

atct の存在を前提にした指示を dotfiles に書かない（逆も同じ）。`orchestration` スキルは
atct 以外の space でも使う。**「atct のゴール 1 件 = space 1 つ」は atct 固有なので、
汎用の役割定義には書かない。**

---

# B. atct への変更依頼分（公開物）

## B-1. ファイル単位の直列化 → worktree で解決する（atct 側の変更は不要）

当初 commander は「`tasks.files` の重なりを claim のときに検査する」案を書いた。
**人間の指摘「それぞれの commander が worktree 作ればいいんじゃないの？」で不要になった。**

失われる書き込みという問題そのものが消えるので、検査を足すより根本的である。

### 実測（2026-08-22）

| 測ったもの | 結果 |
|---|---|
| worktree から `atct context` | **本体と同一プロジェクトに解決。プロジェクトは増えない**（1 件のまま） |
| 正規化の実装 | `NormalizeRoot` → `normalizeWorktreePath` が `--git-common-dir` と `--git-dir` を比較し、違えば `filepath.Dir(commonDir)` を返す |
| worktree の作成 | 366 ms / 2.3 MB |
| pnpm の共有ストア | `~/Library/pnpm/store/v10` に 6.7 GB。**install はハードリンクで済む** |
| `web/node_modules` | 498 MB・gitignore 済み。**新しい worktree には無い** |

**atct はこの使い方を想定して作られていた。**当初 commander は「worktree ごとに別
プロジェクトとして登録されてしまうのではないか」と疑ったが、実測で否定された
（`atct project add` の自動登録を今日入れたので、もし正規化が無ければ worktree ごとに
新しいプロジェクトができていた）。

### 決めること（3 つだけ）

1. **`pnpm install` を誰がいつ走らせるか。**ストアがあるのでハードリンクだが、worktree
   作成時に 1 回は要る。commander が space を立てた直後が素直
2. **マージの衝突を誰が解くか。**worktree は「静かに消える」を「見える衝突」に変える
   だけで、解決者は要る。**これはゴールをまたぐ作業なので B-2 の穴に落ちる。**
   orchestrator が持つのが妥当
3. **リリースは本体ツリーでしか通らない。**`script/release.sh` は clean tree を要求し、
   `git push origin main` と goreleaser を叩く。worktree のブランチからは出せないので、
   **リリースは orchestrator が本体で行う**

### 既存の注意

- **executor は `.git` を書けない**（実測済み）。worktree の作成は commander の仕事
- **共有ツリーでのリリースは落ちる**（実測済み）。worktree に分ければこの問題は減るが、
  リリース時に本体が clean である必要は残る
- 古い worktree が残る。2026-08-22 時点で `atct-wt1` と `atct-wt2` が残っており、
  **`atct-wt2` には未コミットの変更がある**（`internal/store/migrations.go`）。
  **後片付けの担当を決めないと溜まる**

## B-2. ゴール横断の知識の置き場

今日いちばん価値があった指摘は、**どれもゴールをまたいで初めて見えたもの**である。

- `text-xs` の件数が 34 → 36 に増えていた。**別のゴールが行を足したから**
- playwright の `networkidle` が永久に待つ。**SSE を入れた別のゴールの副作用**
- kumo の監査は全コンポーネントにまたがる

**1 ゴールに閉じた commander はこれを見つけられない。**候補は 3 つ。

1. orchestrator が持つ（通知をさばく立場なので横断は見える。ただしコンテキストが太る）
2. リポジトリの `doc/specs/` に書く（今日やっていること。人間も読める。反映が遅い）
3. atct に「ゴールをまたぐ注意」を置く場所を作る（新機能）

commander の推奨は **2 を主、1 を補**。3 は atct の役割を広げすぎる。

## B-3. 決定の宛先

いまは 1 つの commander が全部の決定を出し、人間の受信箱に 1 本の流れで届く。
**commander が N 人になると N 本になる。**決定には `goal_id` が入っているので
ダッシュボードは分けて表示できるが、**「誰が人間に聞くか」を決める必要がある。**

候補: commander が直接聞く（速い。窓口が増える）/ orchestrator を通す（窓口 1 つ。
遅い、かつ orchestrator が内容を理解する必要が出て太る）。

commander の推奨は **commander が直接聞く。**決定は `goal_id` を持っているので
出どころは分かる。orchestrator を通すと、通すために内容を読む必要が出て、
orchestrator を薄く保つ目的と衝突する。

## B-4. orchestrator が必要とする問い合わせ

orchestrator は「どのゴールに人手が付いていて、どれが空いているか」を知る必要がある。
いまの `atct_goal_list` と `atct context` はゴールとタスクを返すが、
**「このゴールに commander が付いているか」は atct が知らない。**

これは 015c9b1a（依頼と受領の記録）と同じ形の話である。**ゴールに担当を記録する場所が
要るのか、それとも claim の集約で足りるのかを決める。**

## B-5. 先に決めること（この 2 つが決まるまで着手しない）

**B-1 は worktree で解決したので、残るのは 1 つ。**

1. **B-2 のゴール横断の知識。**これが無いと、今日拾えた 3 件の指摘が今後拾えない。
   速くなる代わりに質が落ちる形になる。**加えて B-1 のマージ衝突の解決者も、この穴に
   落ちる。**同じ持ち主（orchestrator か `doc/specs/`）を決める話になる

**これは「速くする」提案の代償そのものなので、提案の一部として決めるべきである。**
