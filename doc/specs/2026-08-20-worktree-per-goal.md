# ゴールごとに worktree を分ける

日付: 2026-08-20
ゴール: 各ゴールは worktree で作業されるようにし、各ゴールが干渉しないようにしたい

## いま何で干渉を防いでいるか

**commander が依頼書に「触らないもの」を書いて防いでいる。** 仕組みではなく文章である。

全 executor が同じチェックアウトの `main` で作業する。`git status` は全員の変更が
混ざって見える。誰も他人の変更を区別できない。

## 今日実際に起きたこと

| 起きたこと | 何で防いだか |
|---|---|
| executor-13 の `git status` に executor-12 の `cmd/atct/pending_test.go` が見えた | executor-13 が報告に「既存の変更は未操作」と書いて自衛した |
| commander が web の変更をコミットするとき、executor-12 の作業中のファイルが同じツリーにあった | `git add -A` を使わず、**ファイルを 1 つずつ名指しして** add した |
| executor-15 に履歴の上限を直させるとき、web 側の 1 ファイルが executor-14 の担当だった | 依頼書で「省略件数はサーバ側だけ直せ。その web ファイルは触るな」と指示した |
| commander が `web/package.json` に依存を足したとき、executor-14 が同じ `web/` で作業していた | 人間の指摘で戻した。その間 executor-14 は変わった `package.json` を見ていた |
| executor-14 に「cmd/ と internal/ の変更は別の executor のものだから保持せよ」と毎回書いた | 依頼書の定型文 |

**どれも「commander が正しく書けば防げる」形である。** 書き忘れれば壊れる。

**実測（2026-08-14・別 space）**: 分離が無い状態で executor が同じ依頼を並行実装し、
**同じ 8 ファイルを二重に編集した。** 人間の指摘まで誰も気づかなかった。

## いま払っているコスト

- **依頼書に毎回「触らないもの」の一覧を書く。** 他の executor の担当ファイルを
  commander が把握し続ける必要がある
- **並列にできる仕事が「ファイルが交差しないもの」に限られる。** 今日、
  `internal/httpapi/server.go` と `queries/*.sql` を 1 台が触っていたため、
  **衝突しない実装が残っておらず 3 台目を立てられなかった**
- **コミットのたびに、誰の変更かを人間が判定する。** `git add -A` が使えない

## 確かめる必要があること

### ATCT は worktree を主リポジトリに写す（実装済み・未実測）

`internal/store/project.go:66` の `ResolveProject` が `normalizeWorktreePath` を通し、
コメントに「これが worktree を同じリポジトリの 2 つ目のプロジェクトにするのを防ぐ」と
書いてある。**設計上は対応済みだが、worktree から実際に叩いて測っていない。**

写せていないと、**worktree ごとに別プロジェクトができ、タスクと claim が散る。**

### worktree では Go のテストが落ちる可能性が高い

`internal/daemon/web_test.go` が `dist/goals/_/index.html` を `fs.ReadFile` で読む。
`web/dist` は gitignore されており、新しい worktree には `.gitkeep` しか無い。
`node_modules` も空である。

**つまり worktree を作っただけでは `go test ./...` が通らない。** 何が必要か
（`pnpm install` と `pnpm build`、その所要時間とディスク）を測る。

## 実測の結果（2026-08-20）

| 測ったこと | 結果 |
|---|---|
| ATCT のプロジェクト | **main と worktree で同一。** `pending` も `context` も一致した |
| 素の worktree の `go test ./...` | **落ちる。** `pnpm install` と `pnpm build` が必要 |
| `pnpm install` | 16.2 秒 / **node_modules 433MB** |
| `pnpm build` | 90.4 秒 / dist 652KB |
| 準備後の `go test ./...` | 全通過 |
| 2 つの worktree の分離 | **分離した。** wt1 をコミットしても wt2 は未コミットのまま、main には出ない |
| 主チェックアウト | 変更なし（他の executor の分は残っていた） |

**worktree 1 つあたり 433MB と約 107 秒。** これがこの設計のいちばん重いコストである。

## 決定

### 1 ゴール 1 worktree にしない。1 executor 1 worktree にする

**準備が 107 秒・433MB かかるので、ゴールごとに作ると使い捨てのコストが大きい。**
executor は 3 台までなので、**worktree も 3 つを作り置きして使い回す。**

ゴールが変わっても worktree は変えない。**分離したいのは executor 同士であり、
ゴール同士ではない**（同じゴールを 2 台で分担することもある）。

### ブランチは executor ごとの固定名にする

    wt/executor-1  wt/executor-2  wt/executor-3

ゴール名を入れない。**使い回すので、名前にゴールを入れると嘘になる。**

### 変更は commander が主チェックアウトで受け取る

executor は自分の worktree でコミットしない。**いまと同じく、commander が
レビューしてコミットする。**

受け取り方は `git -C <worktree> diff` を主チェックアウトに適用する形にする。
**`merge` を使わない**理由: executor のブランチに履歴を作ると、commander が
書いているコミットメッセージ（何をなぜ変えたか）が二重になる。

### claim の強制と委譲ガードは変わらない

commander が claim を持ち、executor に渡す形は同じ。**worktree は作業場所の分離であり、
責任の分離ではない。**

### 準備は 1 回だけにする

worktree を作ったら `pnpm install` と `pnpm build` を 1 回走らせ、以後は使い回す。
**`web/dist` は gitignore なので worktree ごとに必要**（`internal/daemon/web_test.go`
が `dist/goals/_/index.html` を読む）。

## やらないこと

- **ゴールごとに worktree を作らない**（コストが見合わない）
- **executor にコミットさせない**（claim の強制と委譲ガードの前提が変わる）
- `herdr worktree create` は使わない。**素の `git worktree` で足りる**
  （herdr の worktree は pane と結びつくが、ここで必要なのは作業ツリーの分離だけ）

## 未決（測ってから決める）

- 1 ゴール 1 worktree か、1 executor 1 worktree か
- ブランチの名づけ（`goal/<id の先頭 8 桁>` など）
- **変更を `main` に戻す手順。** いまは commander が主チェックアウトでレビューして
  コミットしている。`script/release.sh` は作業ツリーが汚れていると拒否する
- 誰がコミットするか。worktree の中で executor がコミットするなら、
  **claim の強制と委譲ガードの前提が変わる**
- `herdr worktree create` を使うか、素の `git worktree` を使うか

## 検証

- worktree 内で `atct pending` が主リポジトリと同じプロジェクトを返すこと
- **2 つの worktree で同じファイルを別の内容に変え、互いに上書きしないこと**
- **それぞれの `git status` が自分の変更しか見ないこと**（これが本題）
- worktree で `go test ./...` が通ること（通すために必要な手順を明記する）

## 内訳: 107 秒の中身（2026-08-21 の実測）

人間から「なんで worktree 作成にそんなに時間がかかるの？」と差し戻された。
**合計しか測っていなかったのが問題である。** 分けて測り直した。

| 手順 | 実時間 | 生まれるもの |
|---|---|---|
| `git worktree add` | **0.2 秒** | 2.0MB |
| `pnpm install` | 20.4 秒 | node_modules 431MB |
| `pnpm build`（`astro check && astro build`） | **69.9 秒** | dist 696KB |
| `go test ./...` | 11.4 秒 | - |

**worktree の作成そのものは 0.2 秒である。** 遅いのは worktree ではなく、
**gitignore されている `web/node_modules` と `web/dist` を毎回作り直していること**である。
90.3 秒のうち 69.9 秒が `pnpm build` で、その大半は `astro check`（型検査）が占める。

### 431MB は本当に増えたディスクである

pnpm は普通グローバルの store から hardlink するので見かけほど増えない、と考えていた。
**測ったら違った。**

    find web/node_modules -type f -links +1 | wc -l  ->      0
    find web/node_modules -type f -links 1  | wc -l  ->  43467

**hardlink が 1 つも無い。** 43467 ファイルすべてが実体である。worktree ごとに 431MB
増える。3 台なら 1.3GB。

## 決定: 作り直さず、主チェックアウトから借りる

`pnpm install` と `pnpm build` を worktree で走らせない。

| 作るもの | やり方 | 実時間 |
|---|---|---|
| `web/node_modules` | 主チェックアウトへの **symlink** | 0.0 秒 |
| `web/dist` | 主チェックアウトから `cp -R web/dist/. <wt>/web/dist/` | 0.05 秒 |

**実測: これで `go test ./...` が全通過する**（wt2 で 3.9 秒、rc=0）。
準備は **90.5 秒 → 0.8 秒**、ディスクは **431MB → 700KB** になる。

### `cp -R` の宛先に注意（実測で踏んだ）

`web/dist` は `.gitkeep` を持つので worktree にも**既に存在する**。
`cp -R main/web/dist <wt>/web/dist` と書くと `<wt>/web/dist/dist` に入れ子になり、
`internal/daemon/web_test.go` が `dist/index.html` を見つけられずに落ちる。
**`cp -R main/web/dist/. <wt>/web/dist/` と書くこと。**

### symlink は `.gitignore` に別の行が要る（2026-08-22 に実測）

**`node_modules/` は末尾スラッシュなのでディレクトリだけを指し、symlink には一致しない。**
git は symlink をファイルとして扱う。そのため worktree の `git status` に
`?? web/node_modules` が出続け、**executor が毎回「これは自分の変更ではない」と
判断する手間が生まれる。**

```
$ git -C <worktree> check-ignore -v web/node_modules
（何も返さない。exit 1）

$ git -C <worktree> check-ignore -v web/node_modules/
fatal: pathspec 'web/node_modules/' is beyond a symbolic link
```

`.gitignore` に **`web/node_modules`（スラッシュ無し）** の行を足して解決した。
**末尾スラッシュを外すだけでは効かない**（`node_modules` にしても一致しなかった）。
パスを明示した行が必要である。

**`.gitignore` は追跡ファイルなので、worktree に届けるにはコミットが要る。**
作業中の worktree では `reset --hard` が使えないので、
`git -C <worktree> checkout <主の HEAD> -- .gitignore` で 1 ファイルだけ取る。

### 移行の番号は worktree で分離できない（2026-08-22 に実測）

**移行は線形の連番でなければならない。**`internal/store/migrations.go:559` が
`number != i+1` で落ちる。

```
embedded migrations are not a linear sequence at "0011_goal_claims.sql": expected 0010
```

**worktree で移行を足している間に、主に新しい移行が入ると飛び番になる。**
番号は全体で 1 つの列なので、**worktree の分離が効かない唯一の資源である。**

実際に起きたこと: worktree で `0011_goal_claims.sql` を書いている間に、主に
`0010_task_snooze.sql` が入った。worktree には `0010` が無いので、
`newTestStore` が動かなくなった。

**対処**: worktree 側は**主の最後の番号 + 1** を使う。ずれたら**着地時に commander が
振り直す。**executor に主の変更を取り込ませない（sqlc の生成物や `schema.sql` が
重なって作業が消える）。

**worktree で移行を足す依頼は、同時に 1 つだけにするのが安全である。**

### 借りることの代償

- **`node_modules` は共有である。** worktree で `pnpm install` を走らせると
  主チェックアウトごと変わる。**worktree で `pnpm install` を禁止する**
- **`dist` は主チェックアウトのビルド時点のものである。** worktree で `web/src` を
  変えたなら、その worktree で `pnpm build` を 1 回走らせる必要がある
  （Go 側だけを触る作業では要らない）

## executor は worktree を作れない（実測）

`git worktree add` を executor（Codex）に実行させたら **0.01 秒で失敗**した。

    unable to create directory for refs/heads/wt/measure-1

主チェックアウトから同じコマンドを叩くと 0.2 秒で成功する。**Codex の sandbox が
`.git` への書き込みを止めている。** worktree の作成と準備は commander が行う。

## 準備は `script/worktree-setup.sh` 1 本にする

    script/worktree-setup.sh 1     # -> ../atct-wt1、ブランチ wt/executor-1

**冪等である。**既にあれば作り直さず、借り直しだけを行って終わる。
主チェックアウトに `web/node_modules` か `web/dist/index.html` が無ければ、
**何を先に走らせればよいかを書いて** exit 2 で止まる（理由だけ書いて手順を書かないと、
止められた側は同じことを繰り返す）。

実測（2026-08-21・commander が主チェックアウトで実行）:

| 実行 | 実時間 |
|---|---|
| 初回（`git worktree add` 込み） | 1.42 秒 |
| 2 回目（冪等の借り直しだけ） | 0.27 秒 |
| 準備後の `go test ./...` | 21.4 秒、rc=0、9 パッケージ ok |

**executor はこのスクリプトを実行できない**（sandbox が `.git` への書き込みを止める）。
worktree の作成と準備は commander が行う。
