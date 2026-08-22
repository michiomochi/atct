# worktree の用意を自動にする

2026-08-22。ゴール `44433505`「worktree の用意を手でやっているので、忘れると主
チェックアウトで壊れる」の設計。

**既存の spec `doc/specs/2026-08-20-worktree-per-goal.md`（211 行）が前提。**
`script/worktree-setup.sh` は既にある。この文書はその**呼び出し方**だけを扱う。

## 起きたこと

**commander は `script/worktree-setup.sh` が既にあることを知らずに 1 日を過ごした。**
executor 2 台を主チェックアウトで並行して動かし、
`internal/store/task.go:147: unknown field SnoozedUntil` でビルドが壊れた。
どちらの変更が原因かを切り分ける手間が出た。**道具はあったのに使わなかった。**

## 見張る対象は `workspace create` である

commander は当初「`herdr agent start` を見る」と書き、次に「`pane split` を見る」と
書いたが、**どちらも誤りだった（人間の指摘、2026-08-22）。**

**subcommander は pane ではなく space に立つ。**3 層の設計では「作業単位ごとに space」
なので、cwd が決まるのは **`workspace create` の時点**である。

```
herdr agent start --help
  Usage: herdr agent start <NAME> --kind <KIND> --pane <ID> [OPTIONS]
  → cwd の指定が無い。既存の pane に立てるだけ
```

**cwd は pane が持っている。**決まるのは `pane split --cwd <dir>` の時点である。

```
herdr workspace create --cwd <PATH> --label <TEXT>    ← ここで cwd が決まる
herdr pane split --cwd <PATH>                        ← 同じ space の中で増やすとき
herdr agent start --pane <ID>                        ← cwd の指定が無い
```

| 見る対象 | 判定 |
|---|---|
| `herdr workspace create --cwd <主チェックアウト>` | **止める** |
| `herdr workspace create --cwd <worktree>` | 通す |
| `--cwd` が無い | 呼び出し元の cwd を継ぐので、**主なら止める** |

**`pane split` も同じ形で見る。**同じ space の中で executor を増やすときに使うので、
そこも主チェックアウトを向いていたら止める。ただし**主たる関門は
`workspace create`** である。

**これで「cwd を書き換えられるか」という未検証点が消えた。**書き換えは要らない。

## 形: 止める前に用意する

**止めるだけでは弱い。**止められた側が `worktree-setup.sh` を走らせる必要があり、
**忘れる余地が残る。**

**フックが `worktree-setup.sh` を走らせてから止める。**そうすると止められた側は
**もう用意されている worktree を `--cwd` に指定するだけ**になる。忘れる要素が
「引数を直す」だけに縮む。

```
1. pane split を見つける
2. cwd が主チェックアウトなら、worktree-setup.sh を走らせる
3. exit 2 で止め、「用意した。--cwd を <path> に向けろ」と言う
```

### 関係ない space の作成を止めないこと（人間の指摘、2026-08-22）

**フックは全 space の `workspace create` で走る。**`stock-data` や `HQ` の space を作る
ときにも発火する。**そこで止めてはいけない。**

**今日それで事故が起きている。**`claim-before-delegate` は `atct pending` が失敗すると
`exit 2` を返し、**atct に登録されていない space でも条件に当たる委譲を全部止めていた。**

判定の順序:

| `--cwd` の状態 | どうするか |
|---|---|
| git リポジトリでない | **通す**（worktree の話ではない） |
| **atct に登録されていない** | **通す**（atct の作業ではない） |
| worktree である | 通す |
| **登録済みだが `worktree-setup.sh` が無い** | **通す**（このフックの管轄外。下の訂正を見よ） |
| atct 管理下の主チェックアウト | **止める** |

### 訂正: 「atct 管理下」は「atct リポジトリ」ではない（2026-08-22・`dotfiles-commander` の実測）

**この表は当初 4 行で、上から 4 行目が無かった。**そのせいで実害が出た。

`dotfiles-commander` が apply 直後に実環境を通したところ、**`stock-data` の space 作成が
`exit 2` で止まった。**

```
stock-data で atct context   → 1356 バイト返る（登録済み）
script/worktree-setup.sh     → 無い
```

**atct は複数のプロジェクトを管理するが、`worktree-setup.sh` は atct 自身にしかない。**
本番の登録は 4 件（`atct` / `stock-data` / `HQ` / `stock-ai`）で、
**script を持つのは `atct` だけである。**

**この表を書いたのは私（commander）で、「atct 管理下」を「atct リポジトリ」の意味で
書いていた。**読み手には区別がつかない。`dotfiles-commander` のレビューも同じ読み替えを
しており、**shim では原理的に出なかった**（テスト用のリポジトリには script を置いていたため）。

### 3 つの状態を分ける

```
worktree-setup.sh が無い    → 通す   このプロジェクトは worktree 運用をしていない
                                    （git でない・未登録と同じ「管轄外」）
あるが失敗した              → 止める  運用しているのに手当てが失敗した
判断できない                → 通す   HERDR_ENV 未設定 / herdr 不在 / 入力 JSON 不正
```

**「無い」を「失敗」として扱うと、worktree を使わないプロジェクトを永久に止める。**
下の「判断できないときは通す」の節と合わせて、**通す理由が 2 種類あることに注意する。**
管轄外だから通すのと、判断できないから通すのは別である。

修正後、実環境の全 space で `exit 0` を確認済み
（`dotfiles` / `stock-data` / `HQ` / `stock-ai` / `tmp` / `atct-wt1`）。

**「atct に登録されているか」の判定は手段がある。**2026-08-22 に `pre-ask` フックで
使った形をそのまま流用できる。

```
atct context の出力が空でなければ、atct が管理している
```

`atct context` は **DB を直接開く**（daemon を必要としない）。プロジェクトが未登録なら
**空を返して `exit 0`** する（`cmd/atct/context.go` の `ErrProjectNotFound` の枝）。

### 残る穴: 主チェックアウトに space を作りたい正当な場合

**commander 自身の space がそれである。**atct を管理下に持ち、かつ主チェックアウトで
作業する。**この形を止めてはいけない。**

**未決**: どう区別するか。候補は (1) 最初の 1 つは通す（既に主の space があるかを見る）
(2) 環境変数などで明示する (3) label に規約を持たせる。
**確かめる前に決めないこと。**

**判断できないときは通す。**`HERDR_ENV` 未設定 / `herdr` 不在 / 入力 JSON 不正
→ `exit 0`。`claim-before-delegate` が `exit 2` で条件に当たる委譲を全部止めた事故と
同型にしないため。

**ただし `worktree-setup.sh` の失敗はここに含めない**（`dotfiles-commander` の指摘、
2026-08-22。当初この文書は含めていた）。**あれは判断の失敗ではなく手当ての失敗である。**
対象が atct 管理下の主チェックアウトだと**判定できた後**に起きる。通せば、このフックが
防ごうとしている事故がそのまま起きる。しかも「用意した」と言えないまま通すので、
止められた側は何も知らずに主で作業を始める。**静かに壊れる形になる。**

実際 `script/worktree-setup.sh` の失敗 4 箇所は、どれも判定後である。2 つは
「主チェックアウトに `web/node_modules` が無い」「`web/dist/index.html` が無い」で、
人間が直せる環境の不備でしかない。

  判断できない            → `exit 0`（通す）
  判断できた + 用意成功    → `exit 2`「用意した。`--cwd` を <path> に向けろ」
  判断できた + 用意失敗    → `exit 2`「用意に失敗した。理由は <stderr>。手で worktree を
                            作るか、主で作業するなら `--env ATCT_MAIN_CHECKOUT=1` を付けろ」

**永久に詰まることはない。**`--env ATCT_MAIN_CHECKOUT=1` が逃げ道になる。これは
「主チェックアウトの space をどう区別するか」で決めた印がそのまま使えるからである。
`claim-before-delegate` の事故は**判断できない場所で止めた**ことが原因であり、
ここは判断できている場所なので同型ではない。

## atct 側に要る変更

**`worktree-setup.sh` は番号を引数に取る**（`atct-wt1`、`atct-wt2`）。3 層ではゴール単位
なので、**`atct-wt-<goal8>` のような命名に変える必要がある。**

そのとき決めること: 既存の `atct-wt<番号>` をどう扱うか。**2026-08-22 に commander が
`atct-wt1` と `atct-wt2` を片付けたが、ブランチ（`wt/executor-1`、`wt/executor-2`）は
残した。**

## 塞がない穴（`single-subcommander.sh` と同じ判断）

```
/usr/bin/herdr workspace create ...    絶対パス呼び出し → 素通り
```

スキルが定める形は「素の `herdr`」であり、**実際に通られたときが直す合図。**
先回りして広げると誤検知の面が増える。

## 置き場

**dotfiles。**`herdr workspace create` を見る規則なので、この環境の設定である。
**atct には置けない**（atct は公開物で herdr を知らない。`claim-before-delegate` を
外したのと同じ理由）。

ただし **`worktree-setup.sh` の命名の変更は atct 側**である。両方が揃って初めて動く。

## 主チェックアウトの space をどう区別するか（実測して決めた。2026-08-22）

上の「残る穴」の未決を埋める。**確かめてから決めた。**

### 実測

```
herdr workspace list      cwd フィールドが存在しない（キー自体が無い）
                          返るのは workspace_id / label / number / pane_count /
                          tab_count / active_tab_id / agent_status / focused

herdr pane list           cwd と foreground_cwd を pane ごとに持つ。workspace_id も持つ

herdr workspace create    --cwd / --label / --env <KEY=VALUE> / --focus / --no-focus

現存する 5 space の label   dotfiles / stock-data / HQ / stock-ai / atct
                          プロジェクト名そのもの。規約は入っていない
```

**フックは引数を全部見られる。**`single-subcommander.sh`（21〜33 行）が既に
`tool_input.command` を丸ごと受け取り `shlex.split` している。**`--cwd` も `--label` も
`--env` も見える。**この点は未検証だったが、既存フックの実装で確認した。

### 決定: `--env` で明示する（候補 2）

```
herdr workspace create --cwd <主チェックアウト> --env ATCT_MAIN_CHECKOUT=1
```

フックはこの引数があれば通す。無ければ worktree を用意して `exit 2` で止める。

### 候補 1（最初の 1 つは通す）を採らない理由

`pane list` が cwd を持つので**実装はできる。**採らないのは別の理由である。

**判定が「今どの pane が存在するか」に乗るため、状態依存になる。**
`pane list` で「主チェックアウトを向いた pane が既にあるか」を見る形になるが、
**pane を閉じた瞬間に「1 つ目」の枠が空く。**commander の pane が落ちている間に
executor 用の space を主へ作ると**通ってしまう。**同じコマンドが日によって通ったり
止まったりする。

**さらに、忘れたときに倒れる向きが逆である。**

| | 明示を忘れた / 誤って主に作った |
|---|---|
| 候補 1 | 1 つ目なら**通る**（事故がすり抜ける） |
| 候補 2 | **止まる**。ただし worktree は用意済みなので直すのは引数だけ |

`claim-before-delegate` の事故は「止めすぎ」だったが、**ここで欲しいのは
「止め漏らさない」側である。**止まる側の代償が「引数を直す」だけになる形
（用意してから止める）を先に入れたので、安全側へ倒して損がない。

### 候補 3（label に規約）を採らない理由

現存 5 space の label はプロジェクト名そのもので、規約は入っていない。
**入れるには 5 つ全部の改名が要る。**かつ label は人間が読む名前であり、
そこへ機械の規約を混ぜると表示が汚れる。`--env` は人間が読む場所ではない。

### 副産物

`--env ATCT_MAIN_CHECKOUT=1` は**引数として見えるだけでなく、実際にその space の
環境変数になる。**space 自身が「自分は主チェックアウトだ」と答えられるので、
後で別の規則を足すときにも使える。フックは引数を見るので、環境変数が
子プロセスへ伝わるかには依存しない。
