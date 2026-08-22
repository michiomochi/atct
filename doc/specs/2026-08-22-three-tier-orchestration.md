# 3 層に分ける（commander / subcommander / executor）

2026-08-22。人間の提案:

> 1 つの claude が atct からの通知をさばき、設計もし、commander への依頼も担当すると
> なるとボトルネックとなってしまいスピードがでない。なので atct のフロントに立つ
> claude（commander）を作成し、commander はゴールの対応毎に別で herdr で space を
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
                 ┌──────────────────────────────────┐
                 │  atct daemon                      │
                 │   SSE / Stop hook / pending       │
                 └──────────────┬───────────────────┘
                                │ 通知はここ 1 か所に集まる
                                ▼
                 ┌──────────────────────────────────┐
                 │  commander (claude)               │
                 │  ・通知をさばく                    │
                 │  ・ゴールを subcommander へ割る     │
                 │  ・space を作る                    │
                 │  ・着地した差分をレビューする        │
                 │  ・リリースする                    │
                 │  ・設計も実装もしない               │
                 └───┬──────────┬──────────┬────────┘
                     │          │          │  ゴール 1 件 = space 1 つ
        ┌────────────▼─┐ ┌──────▼───────┐ ┌▼─────────────┐
        │ subcommander │ │ subcommander │ │ subcommander │
        │  (goal A)    │ │  (goal B)    │ │  (goal C)    │
        │ 設計・依頼書   │ │              │ │              │
        │ 実装レビュー   │ │              │ │              │
        │ worktree     │ │              │ │              │
        └───┬───┬──────┘ └──┬───────────┘ └──┬───────────┘
            │   │           │                │
        ┌───▼┐ ┌▼───┐   ┌───▼──┐         ┌───▼──┐
        │exec│ │exec│   │ exec │         │ exec │        codex
        └────┘ └────┘   └──────┘         └──────┘
```

**ゴールが space の単位になるのが要点。**「話題が変わったら pane を作り直す」という既存の
規則の、自然な単位がゴールである。

## 層の境界

| 層 | やること | やらないこと |
|---|---|---|
| **commander** | 通知をさばく / ゴールを割る / space を作る / **着地した差分のレビュー** / リリース / マージ衝突の解決 / 古い worktree の片付け | そのゴールの設計 / 実装 / executor の成果物の手直し |
| **subcommander** | そのゴールの設計・依頼書・実装レビュー・完了報告 / worktree を作る / 人間へ決定を出す | 他のゴールを見る / リリース / space を作る |
| **executor** | 実装とテスト | 設計判断 / 再委譲 / commit / `.git` を書く |

**commander は設計しない。**設計を始めると今日と同じ場所で詰まる。今日この space が
詰まったのは、通知をさばく者が設計もしていたからである。

**subcommander は space を作らない。**executor を立てるのは pane の分割であって space の
作成ではない。2026-08-14 に executor が自分の下に 3 台を立てて同じ 8 ファイルを二重に
編集した事故があり、**「下を作れる者」を 1 段に限る**のがその対策である。

## 提案が既に解いている問題

**画面の高さの制約が消える。**pane を増やす形だと実測で上限 3 台だった（高さ 91 行の
タブを 2 分割で 1 台 45〜46 行、3 台で約 30 行、4 台で約 22 行）。**ゴールごとに space を
作れば各 space が画面を丸ごと使えるので、この上限に当たらない。**

---

# A. dotfiles への依頼分（この環境の設定）

## A-1. 役割定義を 3 つに増やす（名前は人間が決めた）

`orchestration` スキルの「役割の割り当て」ブロックはいま 2 行（commander と executor）。
これを 3 層にする。**このブロックだけを書き換える形は維持する**（以下の記述に
ハーネス名・モデル名を書かない、という既存の方針）。

人間の判断（2026-08-22）: **`commander` / `subcommander` / `executor`。**
提案時の呼び名「commander」は使わず、**いちばん上を `commander` と呼ぶ。**

**利点: いまの `atct-commander` が名前を変えずに済む。**今日この space がやっている
ことが、そのまま新しい `commander` の役割である。新しい語が増えるのは中間層だけ。

```
atct-commander                    ← プロジェクトの space（今の space がそのまま）
atct-c22a6d79-subcommander        ← ゴールごとの space（ゴール ID の先頭 8 桁）
atct-c22a6d79-executor
atct-c22a6d79-executor-2
```

規約 `<space>-<役割>` は変えない。**変わるのは「space とは何か」の定義だけ** ──
プロジェクトごとから、ゴールごとへ。

当初 commander は「いちばん上は space をまたぐので `<space>-` を前置できない」と書いた。
**これは誤りだった。**いちばん上も自分の space を持っている。またぐのは**作る対象**で
あって、居場所ではない。

**汎用名の事故は起きない。**すべての名前がプロジェクト名で始まるので、2026-08-15 の誤配
（汎用名 `commander` を名乗る space に別 space の完了報告が届いた）の条件を満たさない。

### 長さの予算（実測）— ここに制約が出る

herdr の名前は `[a-z][a-z0-9_-]{0,31}` で **32 文字まで**。

```
 14  atct-commander
 20  stock-data-commander
 26  atct-c22a6d79-subcommander
 32  stock-data-c22a6d79-subcommander      ← ぴったり上限
 30  stock-data-c22a6d79-executor-2
 34  stock-data-c22a6d79-subcommander-2    ← 溢れる
```

**制約 1: 1 ゴールに subcommander は 1 台。これは長さの都合ではなく、守るべき規則である**
（人間の判断: 「これは絶対にそうすべき」）。

理由は 3 つあり、長さはそのうち最も弱い。

1. **2 台は同じ worktree と同じタスク集合を共有する。**2026-08-14 に executor が自分の
   下に 3 台を立て、**同じ依頼を並行実装して同じ 8 ファイルを二重に編集した**事故と
   同型になる。worktree はゴール単位なので、同じゴールの 2 台は同じツリーを踏む
2. **claim が 2 台を区別できない。**claim は「誰かが持っている」までしか答えない
   （B-4 の決定でこれを担当の信号として使うと決めた）。2 台居ると、どちらが持って
   いるのか分からなくなり、信号として機能しなくなる
3. 名前が溢れる（`stock-data-c22a6d79-subcommander-2` は 34 文字）

### どう守るか

**指示だけでは守られない**（2026-08-22 の実測: 指示で止まったことは一度も無い）。
通り道に置く。

- **space を作れるのは commander だけ**（層の境界で決めた）。作成箇所が 1 つなので、
  そこで確かめれば足りる
- **作る前に `herdr agent list` を見て、`*-<goal8>-subcommander` が居たら作らない。**
  名前にゴール ID が入っているので、これは名前の一致で判定できる
- herdr は**同名の生存エージェントを許さない**ので、同じ名前での二重起動は herdr 側で
  弾かれる。**ただし `-2` を付けた形は弾かれない**（`atct` のような短い名前では 28 文字
  で収まる）。だから上の確認が必要である

### 実装された形（dotfiles 側、2026-08-22）

`~/.claude/hooks/single-subcommander.sh`（85 行、`PreToolUse` / Bash matcher）。

- **判定は名前だけ。**`-<数字>` を落とした stem が一致する生存エージェントが居れば `exit 2`
- **判断できないときはすべて `exit 0`**（`HERDR_ENV` 未設定 / `herdr` 不在 /
  `agent list` 失敗・JSON 不正 / 入力 JSON 不正）
- 検証 9 件を executor と dotfiles-commander が独立に実施し、両方で一致（止まる 3 /
  通る 6）。`claim-before-delegate` の回帰も確認済み

**「判断できないときは通す」は意図的である。**`claim-before-delegate` が exit 2 で
条件に当たる委譲を全部止めた事故と同型にしないため。

### 塞がれていない穴（2 つ。強制されていると誤解しないこと）

```
/usr/bin/herdr agent start NAME              絶対パス呼び出し → 素通り
herdr agent start --kind codex NAME          名前より前にフラグ → 素通り
```

**塞がなかったのは意図である。**スキルが定める形は「素の `herdr`・名前が先」であり、
**実際に通られたときが直す合図**になる。先回りして広げると誤検知の面が増える。

**したがって、このフックは「1 ゴール 1 subcommander」を強制しない。**規約どおりに
書いたときに間違いを止めるだけである。**過大に見積もらないこと。**

**制約 2: プロジェクト名は 10 文字まで。**11 文字だと 1 台目から溢れる
（`p + 22 ≤ 32`）。`stock-data` がちょうど 10 文字で、**余裕はゼロ。**これより長い
プロジェクト名を使うなら、ゴール ID の桁数を 8 から減らすか、役割の語を短縮する。

**この制約はエージェント名だけにかかる。**workspace は `--label <TEXT>` を持つだけで、
正規表現がかからない。**実測（2026-08-22）**: `HQ` という大文字を含む label が現に生きて
いる（`herdr workspace list`）。エージェント名の規約なら弾かれる文字列である。
workspace は `w41` のような不透明 ID でアドレスされ、label は表示専用なので、
**ゴール ID 8 桁を label に入れても問題は起きない。**当初これを未検証として残したが、
**実物 1 つで否定できた。**

### 役割ブロックは入った（2026-08-22）

`orchestration` スキルに 3 層の役割定義が入った（commit `b66acc5`、push 済み）。
130 → 150 行で、削った箇所はない。

**atct 固有の対応づけは書かれていない。**「ゴール」ではなく「作業単位」という語を使い、
`<project>-<unit>-<役割>` の形だけを定義している。「プロジェクト名は 10 文字まで」も
入っている。

**中間層は条件付きである。**「常に置く」ではなく「commander が設計と依頼に追われて
通知やレビューを落とし始めたとき」。ゴールが 1 件の space で 3 層にすると無駄が出る。

**`chezmoi apply` は人間の承認待ち。**実体は反映前である。

## A-2. commander が space を作る手順

space 名はゴール ID の先頭 8 桁を使う（`atct-c22a6d79`）。

**作る前に `herdr agent list` を見て、`*-<goal8>-subcommander` が既に居たら作らない**
（A-1 の制約 1）。`herdr workspace create` の戻り値（`.result.workspace` / `.result.tab` /
`.result.root_pane`）から ID を読み、そこに commander を立てる。**ゴール ID を space 名に
入れるかを決める必要がある**（名前は `[a-z][a-z0-9_-]{0,31}` で 32 文字まで。UUID は
入らないので先頭 8 桁になる）。

## A-3. 通知の受け口を commander に寄せる（検討の記録）

いまは commander の pane で `atct watch` を Monitor に流し、Stop hook も commander で
効いている。**両方を上の層へ寄せる。**subcommander 側に残すと、今日と同じで
subcommander が通知に反応して設計を始める。

### `atct watch` の Monitor は寄せられる

Monitor はセッションが張るものなので、subcommander が張らなければ済む。**手順の話。**

### Stop hook は寄せられない（dotfiles-commander の調査、2026-08-22）

**dotfiles 側で塞ぐ手段が無い。**

- `plugin/hooks/stop` の早期 exit は 4 つだけ（`stop_hook_active` / バイナリが無い /
  `atct pending` が失敗 / 出力が空）。**オプトアウトの環境変数が無い**
- `ATCT_BIN` は読むが、`resolve_atct_bin()` の結果に**無条件で上書きされる**ので、
  事前に渡しても効かない
- `disableAllHooks` は全か無かで、他のフックも一緒に死ぬ。採れない

**機構は herdr 側にある。**`herdr pane split --env KEY=VALUE` と
`herdr workspace create --env KEY=VALUE` の両方が使える（`--help` で確認済み）。
**したがって atct 側で env を見る判定を足せば解決する。**

### 撤回された案: `ATCT_SCOPE_GOAL` で絞る

**この案は採用されなかった。**2026-08-22 に人間が「wakeup に統一し Stop hook を廃止する」
と決めたため、**絞り込む対象そのものが無くなった。**実装しないこと。

以下は検討の記録である。**判断の理由に価値があるので残すが、作業の指示ではない。**

**候補として「1 行出す」「役割を渡す」「受け入れる」の 3 つを挙げたが、採用したのは
どれでもない。**4 つ目の形に決まった。

```
ATCT_SCOPE_GOAL=<フルのゴール ID>

  値がある  → pending をその単位に絞って報告する（抑止しない）
  値が無い  → 全部報告する（= いまと同じ）
  値が不正  → 絞らずに全部出す（安全側）
  いずれの場合も、絞ったか否かを systemMessage で 1 行出す
```

**この形を選んだ理由を 3 つ残す。**後から「なぜ役割名にしなかったのか」を再検討させない
ため。

1. **無効化ではなく絞り込みである。**失敗の症状が「何も起きない」ではなく「見える範囲が
   おかしい」になる。**目に見えて自己修正できる。**無効化の失敗は見えない
2. **役割名を渡さない。**役割はエージェント名が既に持っている
   （`atct-c22a6d79-subcommander`）。env にも持たせると**同じ事実の出所が 2 つになり、
   誰も突き合わせない。**名前は 8 桁の接頭辞、フックが要るのはフル ID なので、
   **この形なら二重管理にならない**（8 桁は表示用、フル ID は問い合わせ用）
3. **不正なゴール ID では絞らない。**存在しないゴールに絞ると「何も出ない」となり、
   **結局「静かに消える」と同じ**になる

**「渡し忘れたときに安全側に倒れる」は採用理由ではない。**`ATCT_STOP_HOOK` 方式でも
未設定なら通常どおり働くので、**両案の差になっていない。**commander は当初これを理由に
挙げたが、誤りだった。

実際の差は**適用範囲を間違えたときの被害**である。`off` は profile への export や
子プロセスへの継承で**全 space に及ぶ。**ゴール ID なら、間違った値は「事実の誤り」で
あって「全体の無効化」にはならない。

### 可視化はできる（実測、2026-08-22）

公式ドキュメントの Stop の節:

```
Decision control:              continue（false で停止を止める）
Supported JSON output fields:  continue, systemMessage, terminalSequence
```

**`systemMessage` は Stop で honored で、transcript に出る。**`continue` を触らなければ
**ブロックせずに 1 行出せる。**

当初「告知のためにブロックするのは本末転倒なので可視化できない」と考えたが、
**block は不要だった。**

### atct 固有の名前は atct 側で持つ

`orchestration` スキルには**変数名を書かない。**あのスキルは全 space が読むので、
atct 固有の名前は atct 側（この spec と atct のスキル）に置く。

dotfiles 側には汎用形だけが入った（commit `b66acc5`）。

> pane に作業単位を示す環境変数を渡すなら、名前を付けるコマンドと同じ場所に書く。
> `herdr pane split --env KEY=VALUE` が使える。離して書くと名前と env がずれ、
> どちらが正しいか誰も突き合わせない不整合になる。

**この「同じ場所に書く」は守る。**離れた場所で設定できる形にすると必ずずれる。

## A-3b. 確定した形: wakeup に統一し Stop hook を廃止する

**人間の判断（2026-08-22）: wakeup に統一する。Stop hook を廃止する。間隔は 3 分。**

```
Monitor を張るのは commander だけ（atct:start を呼ぶ）
subcommander は atct:start を呼ばない → 通知が届かない
Stop hook は廃止する → 両方の層で発火する問題が消える
```

**環境変数も Stop hook の改造も要らない。**Monitor はプラグインが張るのではなく
**セッションが自分で張る**（`plugin/hooks/*.json` に `watch` は 0 件。張るのは
`atct:start` スキルの最初の手順）ので、**張らなければ届かない。**

### なぜ統一できるか

Stop hook が言う 10 種類のうち **7 種類はすでに検知イベントとして SSE を流れている。**

```
未着手がある                → wakeup
タスクが未宣言              → detection.undeclared_goal
完了報告が無い              → detection.completion_report_missing
コミットが紐づいていない      → detection.commits_missing
全タスクが dropped          → detection.all_tasks_dropped
claim 無しで doing          → detection.unclaimed_doing
claim したまま渡していない    → detection.claim_undelegated
```

そして **Monitor の通知は人間が何も言わなくてもエージェントのターンを起こす。**
この session 中に `atct wakeup:` の通知で実際に作業が進んだ。**自走する。**

### 先に足すものが 3 件ある。順序を守る

```
答えられた決定が未適用      ← 人間の回答が届かないまま止まる。最も落とせない
既定で閉じた決定が未適用
死んだセッションの claim が残っている
```

**足す → 実測する → Stop hook を消す。**逆にすると、その間だけ人間の回答が届かない
穴が空く。

### 間隔 3 分の代償

実行ログ 708 件の間隔は p50 が 0.6 分、p75 が 3.7 分、p90 が 13.8 分。
**3 分は p75 の少し下なので、正常な作業の合間にも鳴る。**加えて wakeup はプロジェクト
単位なので、条件を満たすプロジェクト数の倍だけ増える（2026-08-22 に 1 周期で 4 プロジェクト
が同時に鳴った）。10 分 → 3 分で約 3.3 倍。

**Stop hook を捨てる代償として受け入れる判断である。**

## A-4. subcommander に「他のゴールを見ない」を書く

いまの commander は全ゴールを見る前提で書かれている。subcommander には**1 ゴールに閉じる指示が必要。**
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

1. **`pnpm install` は走らせない。**2026-08-20 の spec
   `doc/specs/2026-08-20-worktree-per-goal.md` で既に決まっており、
   **`script/worktree-setup.sh` として実装済みである。**

   commander は今日これを知らずに「worktree を作った直後に 1 回走らせる」と書いたが、
   **誤りだった。**`node_modules` は主チェックアウトへの symlink で借りるので、
   **worktree で `pnpm install` を走らせると主チェックアウトごと変わる。**
   あの spec は「worktree で `pnpm install` を禁止する」と明記している。

   実測（2026-08-20）: 借りる形で準備は **90.5 秒 → 0.8 秒**、ディスクは
   **431MB → 700KB**。`go test ./...` は全通過する。
   `web/dist` は `cp -R main/web/dist/. <wt>/web/dist/` と書く（`/.` を落とすと
   入れ子になり `web_test.go` が落ちる）
2. **マージの衝突を誰が解くか。**worktree は「静かに消える」を「見える衝突」に変える
   だけで、解決者は要る。**これはゴールをまたぐ作業なので B-2 の穴に落ちる。**
   commander が持つのが妥当
3. **リリースは本体ツリーでしか通らない。**`script/release.sh` は clean tree を要求し、
   `git push origin main` と goreleaser を叩く。worktree のブランチからは出せないので、
   **リリースは commander が本体で行う**

### 既存の注意

- **executor が動いている間はリリースしない。**ツリーが clean に見えた瞬間でも、直後に
  書かれる。**実測（2026-08-22）**: `git status --porcelain` が空であることを確かめて
  `script/release.sh 0.39.0` を出したが、その直後に executor が
  `web/src/lib/ui.test.ts` を書き始め、**スクリプトが走らせた web のテストが
  実装途中の赤いテストに当たって中止した**（`ui.test.ts:458`）。タグも版の書き換えも
  残らず安全に止まったが、これは `release.sh` のテスト関門に助けられただけである。
  **worktree に分ければこの競合は消える**（本体ツリーで作業する者がいなくなる）
- **executor は `.git` を書けない**（実測済み）。worktree の作成は commander の仕事
- **共有ツリーでのリリースは落ちる**（実測済み）。worktree に分ければこの問題は減るが、
  リリース時に本体が clean である必要は残る
- 古い worktree が残る。**後片付けの担当を決めないと溜まる。**

  **実測（2026-08-22）**: `atct-wt1` と `atct-wt2` が残っており、`atct-wt2` には
  未コミットの変更があった（`internal/store/migrations.go`）。commander が片付けた。

  **片付けるときは、未コミットの変更が残骸かどうかを 1 行ずつ確かめる。**wt2 の 17 行は
  すべて本体に存在した（commit `1980f14` で取り込まれた後の残骸だった）。
  **確かめる前は「捨てられかけた実在のバグの修正」と誤読し、ゴールまで立てた。**

  ```sh
  git -C <worktree> diff <file> | grep '^+' | grep -v '^+++' | sed 's/^+//'     | while IFS= read -r l; do grep -qF "$l" <file> || echo "無い: $l"; done
  ```

  **worktree を消してもブランチは残る。**`wt/executor-1` と `wt/executor-2` が残った。
  ブランチを消すかは人間の判断（worktree は再生成できるが、ブランチ名は記録である）

## B-2. ゴール横断の知識 → atct に `knowledge` は作らない（決定済み）

今日いちばん価値があった指摘は、**どれもゴールをまたいで初めて見えたもの**である。

- `text-xs` の件数が 34 → 36 に増えていた。**別のゴールが行を足したから**
- playwright の `networkidle` が永久に待つ。**SSE を入れた別のゴールの副作用**
- `UnstartedTaskCount` の意味が「claim できる数」から「合計」に変わり、
  **`pending.go` の文面が嘘になった。**別のタスクの変更が別のコマンドを壊した
- kumo の監査は全コンポーネントにまたがる

**1 ゴールに閉じた subcommander はこれを見つけられない。**

人間の提案は「atct の概念として `knowledge` を作り atct DB に保存するか、各プロジェクトの
`doc` に溜めて AI がそれを見るか」。**決定は「atct に `knowledge` は作らない」。**

### なぜ DB ではないのか

上に挙げた 4 件は、**どれもコードの現在の事実**である。**この種の事実はコードが変わった
瞬間に古くなる。**DB に置いた知識には、古くなったことを気づく仕組みが無い。
**黙って嘘になる。**

このリポジトリには既に答えがある。`colors.test.ts` / `i18n.test.ts` / `ui.test.ts` /
`events.test.ts` は **ソースを `?raw` で読んで突き合わせる検査**である。事実が成り立た
なくなった瞬間にテストが落ちる。**落ちる注意書きの方が、落ちない注意書きより強い。**

### 3 つに分ける

| 種類 | 置き場 | 古くなったとき |
|---|---|---|
| 検査できる事実（件数、クラス名、イベント名の対応） | **テスト**（既存の慣行） | テストが落ちる |
| 判断とその理由 | **`doc/specs/`** | コードと一緒に版が進む。人間が読める |
| atct 自身が行動に使うもの | atct（通知に載せる等） | 既存の仕組みの中 |

### 発見の問題は新機能なしで解ける

本当の穴は「subcommander はどの spec を読めばいいか分からない」である。
**`atct context` はゴールの内容をそのまま出す。**ゴールの記述に「`doc/specs/X` を
読むこと」と書けば subcommander に届く。**2026-08-22 の依頼 39 で実際にそうした**
（「spec の該当節だけ読め」と書いた）。新しいテーブルは要らない。

### 残る担当

**B-1 のマージ衝突の解決者は、この穴に落ちる。**衝突の解決はゴールをまたぐ作業で、
テストにも spec にも書けない。**commander が持つ。**

## B-3. 決定の宛先

いまは 1 つの commander が全部の決定を出し、人間の受信箱に 1 本の流れで届く。
**subcommander が N 人になると N 本になる。**決定には `goal_id` が入っているので
ダッシュボードは分けて表示できるが、**「誰が人間に聞くか」を決める必要がある。**

候補: subcommander が直接聞く（速い。窓口が増える）/ commander を通す（窓口 1 つ。
遅い、かつ commander が内容を理解する必要が出て太る）。

commander の推奨は **subcommander が直接聞く。**決定は `goal_id` を持っているので
出どころは分かる。commander を通すと、通すために内容を読む必要が出て、
commander を薄く保つ目的と衝突する。

## B-4. 担当の記録 → 撤回。ゴールに claim を持たせる

**この節の結論は撤回された（人間の判断、2026-08-22）。**「タスクと同じように、ゴールも
claim を持てばよい」。

**撤回の理由**: 下記の結論は**名前に依存していた。**`herdr agent list` に
`*-<goal8>-subcommander` が居るかを見る形だったが、それは herdr を見ることであり、
名前の付け方が守られている前提に立っていた。**ゴールに claim があれば atct 自身が
答えられる。**

これで dotfiles の `single-subcommander.sh` も不要になる見込みである（あのフックは名前の
一致で判定し、絶対パス呼び出しと名前より前のフラグを意図的に素通しする）。
**判定が名前でなくなるので、その穴も消える。**

ゴールとして切り出した（`ba452792`、`f7a8661b` から派生）。**入れ子の扱い**
（ゴールを claim していない者がそのタスクを claim できるか）を先に決める必要がある。

以下は撤回された検討の記録である。**判断の理由に価値があるので残すが、作業の指示では
ない。**

### 旧: atct には持たない（撤回）

commander は「どのゴールに人手が付いていて、どれが空いているか」を知る必要がある。
当初 commander は「atct にゴールの担当を記録する場所が要るのではないか」と書いた。

人間の判断: **「別に持たなくていい。claim 見れば作業されているか分かる。」**

**そのとおりで、しかも理由はもう 1 つ強い。**A-1 で決めた命名の帰結として、
**エージェント名にゴール ID が入る。**

```
herdr agent list  →  atct-c22a6d79-commander が居るか
```

**atct に聞く必要がない。**そして atct は herdr を知らないままで済む（既存の方針を
崩さない）。

### 2 つの信号は別のことを答える

| 見るもの | 答えること | 効く場面 |
|---|---|---|
| `herdr agent list` | **commander が付いているか** | タスクを宣言する前の窓でも分かる |
| claim | **実際に作業が進んでいるか** | 宣言後 |

claim だけだと、commander が立ってタスクを宣言するまでの窓で二重に立てる余地が残る。
その窓は 2026-08-22 の実測では 1 分程度だが、調査を伴うゴールでは 10 分以上になる。
**名前で見れば、その窓も埋まる。**

## B-5. 古い worktree の後片付け → commander（決定済み）

人間の判断: **commander。**

2026-08-22 時点で `atct-wt1` と `atct-wt2` が残っており、**`atct-wt2` には未コミットの
変更がある**（`internal/store/migrations.go`）。**未コミットの変更がある worktree を
黙って消してはいけない**ので、片付けの手順には「中身を確認して人間に出す」段が要る。

`git worktree remove` は clean でなければ `--force` を要求する。**`--force` を既定に
しない。**

## B-5b. 最終成果物は commander がレビューする（決定済み）

人間の判断: **「最終的な成果物は commander がレビューするようにして。主な観点は
ゴールを跨いだ知識を用いたレビュー。」**

これは commander が挙げた「速さのために質を下げうる」という懸念への答えである。

### どこで行うか — リリースが関門になる

**新しい仕組みを足さない。**commander は既にリリースを持つ（B-1 の決定: リリースは
本体ツリーでしか通らないので commander が行う）。**リリースの前に、そこまでに
溜まった差分をレビューする。**

```
subcommander (goal A) ─┐
subcommander (goal B) ─┼─▶ 本体に着地した差分 ─▶ commander のレビュー ─▶ リリース
subcommander (goal C) ─┘                              │
                                                   └─▶ 差し戻し or ゴールに切り出す
```

**指示ではなく通り道にする。**2026-08-22 の実測（ゴール 44ae6e4e）で、指示で止まった
ことは一度も無く、止まったのは通り道に置いたフックだけだった。**レビューを「やるべき
こと」として書くと、今日と同じで飛ばされる。**リリースの手順の中に置く。
この決定により、`script/release.sh` は `--reviewed` がないと非ゼロで終了するリリース関門になった。

### 観点（今日の 4 件から起こした）

抽象的な「ゴール横断の知識でレビュー」では判定できない。**4 つの問いにする。**
どれも 2026-08-22 に実際に起きたもので、1 ゴールに閉じた subcommander では見つからなかった。

| # | 問い | 今日の実例 |
|---|---|---|
| 1 | **数を根拠にした変更が、別のゴールで増減していないか** | `text-xs` を 34 件と書いた依頼を出した後、別のゴールが行を足して 36 件になっていた |
| 2 | **公開されている名前の意味が変わり、別の呼び出し元が嘘になっていないか** | `UnstartedTaskCount` が「claim できる数」から「合計」に変わり、`pending.go` の「no work lock」が嘘になった |
| 3 | **既存の計測・検証の手順を壊していないか** | SSE を入れたゴールの副作用で、playwright の `waitUntil: networkidle` が永久に待つようになった |
| 4 | **横断規則に新しい違反を持ち込んでいないか** | kumo の 15 規則。1 つのコンポーネントを直す差分が、別の規則を破ることがある |

**1 と 4 は検査に落とせる**（B-2 の決定どおり、ソースを読んで数える検査）。
**2 と 3 は落としにくい。**ここが commander が人手で見る部分である。

### 差し戻しの行き先

レビューで問題が出たとき、**その場で直さない。**

- そのゴールの範囲内なら、subcommander の space へ差し戻す
- 範囲を超えるなら、**別のゴールとして切り出す**（既存の規則。残作業を報告に書いて
  終わらせない）

### レビューできない状態を作らない

**リリースまでに溜める差分を大きくしない。**今日 1 回のリリースに 3 ゴール分が混在し、
`git add` をパス指定で切り出す必要があった。**ゴールが着地したら出す**のを既定にする。

## B-6. 先に決めること

**B-1 は worktree で、B-2 は「knowledge を作らない」で決着した。着手を止める論点は
残っていない。**

決まったことの一覧:

| 論点 | 決定 |
|---|---|
| ファイル単位の直列化 | ゴールごとに worktree。atct 側の変更は不要（正規化済みを実測） |
| ゴール横断の知識 | atct に `knowledge` を作らない。事実はテスト、判断は `doc/specs/` |
| 発見の経路 | ゴールの記述に読むべき spec を書く（`atct context` が運ぶ） |
| 決定の宛先 | subcommander が直接聞く（`goal_id` で出どころが分かる） |
| マージ衝突の解決 | commander |
| リリース | commander が本体ツリーで |
| 担当の記録 | **ゴールに claim を持たせる**（B-4。名前に依存する旧案は撤回） |
| 古い worktree の片付け | commander。未コミットの変更があるものは人間に出す |
| 最終成果物のレビュー | commander。**リリースを関門にする。**観点は 4 つ（B-5b） |
| 1 ゴールあたりの subcommander | **1 台。**作る前に `herdr agent list` で確認する（A-1） |
| worktree の準備 | **`script/worktree-setup.sh` を使う。`pnpm install` は走らせない**（B-1） |
| 通知の受け口 | **wakeup に統一。Stop hook を廃止。間隔 3 分**（A-3b） |
| workspace 名の制約 | **無い。**32 文字の規約は agent 名だけ（A-1） |
| 役割定義の置き場 | `orchestration` スキル（`b66acc5` で着地。`chezmoi apply` は承認待ち） |
| atct 固有の名前の置き場 | **atct 側で持つ。**`orchestration` には書かない |

## B-7. 残っているもの

**設計はすべて決着した。**残るのは実装と、その置き場の作業だけである。

| 残り | 担当 | 待っているもの |
|---|---|---|
| `chezmoi apply`（役割定義） | dotfiles | 人間の承認 |

**この表から 2 行消した（2026-08-22）。**どちらも本文では決着していたのに、ここだけ
古いままだった。**一覧しか読まない者は古い方を信じる。**実際 `dotfiles-commander` は
`ATCT_SCOPE_GOAL` を承認待ちだと読んで executor に指示を出していた。
A-1 の穴を 2 か所に書いたのと同じ理由が、ここでは逆向きに効いた。

| 消した行 | 実際は |
|---|---|
| `ATCT_SCOPE_GOAL` の実装 | **撤回済み。**241 行の「撤回された案」を見よ。実装しない |
| `single-subcommander.sh` の apply | **apply しない。**人間の判断（2026-08-22）「防波堤は不要。早く atct 側で対応を進めて」。置き換えは `ba452792`（ゴールに claim を持たせる） |

**強制されていないものを 2 つ、A-1 に書いた。**フックは絶対パス呼び出しと
名前より前のフラグを素通しする。**意図的である。**過大に見積もらないこと。

## 3 層は現在の atct の上に乗らない（2026-08-22 に実測）

`ba452792` の最後のタスク `0b79b6a3`「稼働版で 2 人目が入れないことを実測する」で
分かったこと。**ゴールの claim は守れない。**そして理由は claim の側ではない。

### 実測

稼働版 0.41.0、空の DB の一時 daemon。生きた pid を持つ 2 つのセッション。

```
probeX（pid 30176・kill -0 で生存確認）が goal.claim  → claimed_by = probeX
probeY（pid 30176・同じく生存）が goal.claim         → claimed_by = probeY
```

**2 人目が通る。**単体テスト `TestClaimGoalRejectsLiveClaimFromOtherSession` は緑のまま。
あれは `store` を直接叩いており、**daemon の経路を通らない。**

### 原因

`internal/store/store.go:141`、`AssociateAgentSessionWithProject` の末尾。

```sql
DELETE FROM agent_sessions WHERE project_id = ? AND id <> ? AND registered_at < ?
```

**セッションをプロジェクトへ紐づけると、そのプロジェクトの古いセッションが全部消える。**

`goal.claim` は `ClaimGoal` の前に `ensureAgentSessionProject` を通す。2 人目がそこで
紐づいた瞬間に 1 人目の行が消え、`claimIsRunning(1 人目)` が `false` を返し、
「死んだ claim は引き継ぐ」の枝に落ちる。

実測で確認した: `probeY` の claim 後、`probeX` の行は DB から消えていた。

### これは claim の穴ではなく、設計の前提の食い違いである

**この削除は「1 プロジェクトに 1 セッション」を強制している。**

この文書が設計している 3 層は、同じプロジェクトに commander 1 台と subcommander 複数台が
**同時に居る**形である。**いまの atct はその状態を保持できない。**後から来たセッションが
先に居たセッションの記録を消す。`ba452792` の claim も、`f7a8661b` の 3 層化も、
この削除の上には乗らない。

期限による掃除は同じ関数の中で `DeleteExpiredAgentSessionsExcept`（保持 30 日）が
別に行っている。**この削除は期限を見ず、新しいセッションが来たというだけで消す。**
何のために入れたかを確かめてから外すこと。

### 測り方の教訓

**store を直接叩くテストでは通り抜けた。**daemon の経路には
`ensureAgentSessionProject` があり、そこが状態を壊していた。
**同じ関数を呼んでいても、前後に挟まるものが違えば結果が変わる。**
稼働版で測って初めて出た。

## 通知の受け口はもう commander に寄っている（2026-08-22 に実測）

`f7a8661b` のタスク `9b4b98e1`「通知の受け口を commander に寄せる」の記録。
完了条件は「`atct watch` の Monitor と Stop hook が commander の space で動き、
subcommander の space では動かないことを実測する」だった。

**半分は今日消え、残り半分は既に満たされていた。作業は要らない。**

### Stop hook の側は前提ごと消えた

`aa6a9eb` で削除した。0.41.0 の `plugin/hooks/hooks.json` は
`['PreToolUse', 'SessionStart']` である。**space ごとに入れるか切るかを確かめる、という
未検証点そのものが無くなった。**

### Monitor の側は最初から寄っている

**プラグインは Monitor を張らない。**`plugin/hooks/hooks.json` に `watch` の記述は 0 件。
張るのは `atct:start` スキルを呼んだセッション自身である（`skills/start/SKILL.md:11-17`
が「最初に Monitor を張れ」と指示している）。

したがって **subcommander が `atct:start` を呼ばなければ通知は届かない。**
これは設定ではなく、スキルを呼ぶかどうかで決まる。
`ATCT_SCOPE_GOAL` のような絞り込みが不要になったのと同じ理由である。

### 残る 2 つのフックは subcommander でも害がない

```
SessionStart  matcher=startup|clear|compact   ATCT context を出す
PreToolUse    matcher=AskUserQuestion         チャットで聞くのを止め atct へ寄せる
```

`session-start` は subcommander も自分のゴールを知る必要がある。
`pre-ask` はチャットで判断を仰がない規則（`fa888894`）で、subcommander にも
適用したいものである。**どちらも切る理由がない。**

### 3 層で守るべきことは 1 つに減った

**「subcommander の space で `atct:start` を呼ばない」だけ。**
呼べば Monitor が張られ、通知に反応して設計を始める。呼ばなければ届かない。
これは commander が space を作るときの手順の話で、atct 側の設定ではない。

## ファイル衝突の検査は 1 セッション運用では効かない（2026-08-22 に実測）

`rejectTaskFileConflict`（`internal/store/task.go:551`）は、claim しようとしたタスクの
`files` が既に claim されている別タスクの `files` と重なると拒む。**設計は正しい。**

**しかし今日それをすり抜けた。**

`9f0af794`（派生元を画面からたどる）と `007c3e78`（画面から本文を書き換える）は
どちらも `web/src/components/GoalDetail.tsx` を `files` に持つ。**両方 claim できた。**
私が executor-2 に渡す直前に、executor-1 が同じファイルを編集中であることに
気づいて止めた。**atct は止めなかった。**

### 理由

`ListClaimedTasksForConflict`（`internal/store/queries/task.sql:70`）の `WHERE` 句。

```sql
WHERE id <> ?
  AND claimed_by <> ''
  AND claimed_by <> ?        -- ← 自分のセッションを除外する
  AND status NOT IN (?, ?)
```

**自分が claim しているタスクは衝突の相手として数えない。**同じセッションが両方を
claim している限り、重なりは見えない。

### 3 層では効く。いまは効かない

3 層では commander と subcommander が別セッションなので、2 人目の claim で拒まれる。
**しかし今日の運用では commander が全ゴールの全タスクを claim している**
（`ba452792` の本文が指摘しているとおり）。**その状態ではこの防御が丸ごと無効である。**

これは `ba452792` の「入れ子の扱いをいつ入れるか」と同じ構造の話で、
**「3 層に移ってから効く仕組み」が、移る前は守ってくれない**という形である。

### 当面の代償

**commander が自分で気づくしかない。**タスクの `files` を宣言しておく価値は残る
（読めば重なりが分かる）。実際、今日は `atct_goal_list` の応答で 2 つのタスクの
`files` を並べて見て気づいた。**宣言していなければ気づけなかった。**

## 追記: 消し合いは 1 回では終わらない（2026-08-22・追加の実測）

上で「2 人目が入ると 1 人目の生存記録が消える」と書いたが、**症状はもっと重い。**

`AssociateAgentSessionWithProject` を呼ぶ場所を全部数えたところ、
**`goal.list` の中にあった**（`internal/daemon/handler.go:210`）。

```
case "goal.list":
    ...
    if err := d.store.AssociateAgentSessionWithProject(ctx, p.AgentSessionID, ns.ID); err != nil {
```

**`goal.list` は `atct:start` の 1 手目である。**さらにセッションが状況を見るたびに呼ぶ。

したがって 3 層ではこうなる。

```
1. subcommander が起動して goal.list      → commander の記録が消える
2. commander が次に goal.list             → 自分を登録し直し、subcommander の記録が消える
3. subcommander がまた goal.list          → 1 に戻る
```

**両方が動いている限り、交互に消し合い続ける。**「2 人目が入った瞬間だけ」ではない。

`dotfiles-commander` の指摘のとおり、**worktree の用意が楽になったぶん subcommander を
作る回数が増え、この不具合に当たる回数も増える。**3 層を展開する前に直すべきである。
