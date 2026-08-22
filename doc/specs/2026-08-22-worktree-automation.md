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

## 見張る対象は `pane split` である（`agent start` ではない）

commander は当初「`herdr agent start` を見る」と書いたが、**誤りだった。**

```
herdr agent start --help
  Usage: herdr agent start <NAME> --kind <KIND> --pane <ID> [OPTIONS]
  → cwd の指定が無い。既存の pane に立てるだけ
```

**cwd は pane が持っている。**決まるのは `pane split --cwd <dir>` の時点である。

| 見る対象 | 判定 |
|---|---|
| `herdr pane split --cwd <主チェックアウト>` | **止める** |
| `herdr pane split --cwd <worktree>` | 通す |
| `--cwd` が無い | 呼び出した pane の cwd を継ぐので、**主なら止める** |

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

**判断できないときは通す。**`HERDR_ENV` 未設定 / `herdr` 不在 / 入力 JSON 不正 /
`worktree-setup.sh` が失敗 → すべて `exit 0`。
`claim-before-delegate` が `exit 2` で条件に当たる委譲を全部止めた事故と同型に
しないため。

## atct 側に要る変更

**`worktree-setup.sh` は番号を引数に取る**（`atct-wt1`、`atct-wt2`）。3 層ではゴール単位
なので、**`atct-wt-<goal8>` のような命名に変える必要がある。**

そのとき決めること: 既存の `atct-wt<番号>` をどう扱うか。**2026-08-22 に commander が
`atct-wt1` と `atct-wt2` を片付けたが、ブランチ（`wt/executor-1`、`wt/executor-2`）は
残した。**

## 塞がない穴（`single-subcommander.sh` と同じ判断）

```
/usr/bin/herdr pane split ...          絶対パス呼び出し → 素通り
```

スキルが定める形は「素の `herdr`」であり、**実際に通られたときが直す合図。**
先回りして広げると誤検知の面が増える。

## 置き場

**dotfiles。**`herdr pane split` を見る規則なので、この環境の設定である。
**atct には置けない**（atct は公開物で herdr を知らない。`claim-before-delegate` を
外したのと同じ理由）。

ただし **`worktree-setup.sh` の命名の変更は atct 側**である。両方が揃って初めて動く。
